#!/usr/bin/env bash
# Produce the exact-commit validation attestation embedded in the signed release.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
MATRIX="$ROOT/distro/release/validation-matrix.json"
OUT="$ROOT/distro/out/release-gates.json"
PACKAGES="${CLOUDLESS_PACKAGE_OUT:-$ROOT/distro/out/packages}"
SECURITY_OUT="${CLOUDLESS_SECURITY_OUT:-$ROOT/distro/out/security}"
SBOM="${CLOUDLESS_SBOM:-$SECURITY_OUT/cloudless-$VERSION.spdx.json}"
PHYSICAL_QUALIFICATION="${CLOUDLESS_PHYSICAL_QUALIFICATION:-$ROOT/distro/out/qualification/cloudless-physical-qualification.json}"
SECURITY_READINESS="${CLOUDLESS_SECURITY_READINESS:-$ROOT/distro/out/qualification/cloudless-security-readiness.json}"

[ -n "$VERSION" ] || { echo "Usage: $0 VERSION [stable|beta]" >&2; exit 2; }
case "$CHANNEL" in stable|beta) ;; *) echo "Invalid channel: $CHANNEL" >&2; exit 2 ;; esac
for command in file go python3 dpkg-deb sha256sum; do
    command -v "$command" >/dev/null || { echo "Missing release-gate command: $command" >&2; exit 1; }
done
test -s "$MATRIX" || { echo "Missing release validation matrix" >&2; exit 1; }
commit="${CLOUDLESS_SOURCE_COMMIT:-}"
if [ -z "$commit" ]; then
    command -v git >/dev/null || { echo "Git is required when CLOUDLESS_SOURCE_COMMIT is not set." >&2; exit 1; }
    commit="$(git -C "$ROOT" rev-parse HEAD)"
    git -C "$ROOT" diff --quiet && git -C "$ROOT" diff --cached --quiet || {
        echo "Release gates require a clean tracked source tree." >&2
        exit 1
    }
fi
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || { echo "Invalid source commit" >&2; exit 1; }

python3 - "$MATRIX" <<'PY'
import datetime as dt, json, sys
from urllib.parse import urlparse
with open(sys.argv[1], encoding="utf-8") as handle:
    matrix = json.load(handle)
if matrix.get("schema") != "cloudless.release-validation.v1":
    raise SystemExit("unsupported release validation matrix schema")
targets = {(item.get("platform"), item.get("architecture")) for item in matrix.get("targets", [])}
required = {("generic", "amd64"), ("generic", "arm64"), ("dgx-spark", "arm64")}
if targets != required:
    raise SystemExit(f"matrix targets must be exactly {sorted(required)}")
gates = set(matrix.get("requiredGates", []))
expected = {"go-tests", "go-vet", "web-javascript", "app-manifest-v2", "backup-recovery", "installer-preflight", "platform-matrix", "package-architecture", "package-contents", "package-lifecycle", "release-isolation", "release-preflight", "secret-hygiene", "service-hardening", "sbom", "trust-inventory", "vulnerability-scan", "updater-workload-continuity", "atomic-repository"}
if gates != expected:
    raise SystemExit("matrix gate set is incomplete or contains an unknown gate")
PY

echo "==> Validating platform behavior matrix"
while read -r platform architecture; do
    echo "    $platform / $architecture"
    (
        cd "$ROOT/orchestrator"
        CLOUDLESS_PLATFORM="$platform" CLOUDLESS_ARCH="$architecture" \
            go test -count=1 ./internal/catalog ./internal/modelfit ./internal/platform ./internal/capabilities ./internal/provision
    )
done < <(python3 - "$MATRIX" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    for target in json.load(handle)["targets"]:
        print(target["platform"], target["architecture"])
PY
)

echo "==> Validating release package matrix"
for architecture in amd64 arm64; do
    for package in cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater; do
        deb="$PACKAGES/${package}_${VERSION}_${architecture}.deb"
        test -s "$deb" || { echo "Missing gated package: $deb" >&2; exit 1; }
        test "$(dpkg-deb -f "$deb" Architecture)" = "$architecture" || {
            echo "Package architecture mismatch: $deb" >&2
            exit 1
        }
    done
    work="$(mktemp -d)"
    dpkg-deb -x "$PACKAGES/cloudless-orchestrator_${VERSION}_${architecture}.deb" "$work"
    info="$(file "$work/usr/lib/cloudless/cloudlessd")"
    rm -rf "$work"
    case "$architecture:$info" in
        amd64:*x86-64*) ;;
        arm64:*ARM\ aarch64*) ;;
        *) echo "Wrong cloudlessd binary in $architecture package: $info" >&2; exit 1 ;;
    esac
done

echo "==> Validating release security evidence"
test -s "$SBOM" || { echo "Missing SPDX SBOM: $SBOM" >&2; exit 1; }
test -s "$SECURITY_OUT/trivy-source.json" || { echo "Missing source vulnerability report" >&2; exit 1; }
test -s "$SECURITY_OUT/trivy-packages.json" || { echo "Missing package vulnerability report" >&2; exit 1; }
python3 - "$SBOM" "$SECURITY_OUT/trivy-source.json" "$SECURITY_OUT/trivy-packages.json" "$VERSION" <<'PY'
import json, sys

sbom_path, source_report_path, package_report_path, version = sys.argv[1:]
with open(sbom_path, encoding="utf-8") as handle:
    sbom = json.load(handle)
if sbom.get("spdxVersion") != "SPDX-2.3":
    raise SystemExit("release SBOM must use SPDX 2.3")
described = set(sbom.get("documentDescribes", []))
packages = sbom.get("packages", [])
cloudless = [
    item for item in packages
    if item.get("name", "").startswith("cloudless-") and item.get("versionInfo") == version
]
expected = {
    (name, architecture)
    for architecture in ("amd64", "arm64")
    for name in (
        "cloudless-orchestrator", "cloudless-shell", "cloudless-branding",
        "cloudless-hardware", "cloudless-firstboot", "cloudless-updater",
    )
}
actual = {
    (item.get("name"), next(
        (ref["referenceLocator"].split("arch=", 1)[1] for ref in item.get("externalRefs", [])
         if "arch=" in ref.get("referenceLocator", "")),
        "",
    ))
    for item in cloudless
}
if actual != expected:
    raise SystemExit("release SBOM does not describe the exact 12-package architecture matrix")
if not all(item.get("SPDXID") in described and item.get("checksums") for item in cloudless):
    raise SystemExit("release SBOM package descriptions are incomplete")
for report_path in (source_report_path, package_report_path):
    with open(report_path, encoding="utf-8") as handle:
        report = json.load(handle)
    if not isinstance(report.get("Results", []), list):
        raise SystemExit(f"invalid Trivy report: {report_path}")
    findings = [
        vulnerability
        for result in report.get("Results", [])
        for vulnerability in (result.get("Vulnerabilities") or [])
        if vulnerability.get("Severity") in {"HIGH", "CRITICAL"}
    ]
    if findings:
        raise SystemExit(f"security report contains {len(findings)} unresolved high/critical vulnerabilities")
PY

echo "==> Validating physical qualification identity"
test -s "$PHYSICAL_QUALIFICATION" || { echo "Missing physical qualification descriptor" >&2; exit 1; }
python3 - "$PHYSICAL_QUALIFICATION" "$VERSION" "$CHANNEL" "$commit" "$ROOT/distro/release/physical-validation-matrix.json" <<'PY'
import hashlib, json, sys
path, version, channel, commit, matrix_path = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    physical = json.load(handle)
with open(matrix_path, "rb") as handle:
    matrix_hash = hashlib.sha256(handle.read()).hexdigest()
required = channel == "stable" and int(version.split(".", 1)[0]) >= 1
if physical.get("schema") != "cloudless.physical-release.v1":
    raise SystemExit("invalid physical qualification descriptor schema")
for field, expected in (
    ("version", version), ("channel", channel), ("sourceCommit", commit),
    ("matrixSha256", matrix_hash), ("required", required),
):
    if physical.get(field) != expected:
        raise SystemExit(f"physical qualification {field} mismatch")
if physical.get("status") not in {"qualified", "not-qualified"}:
    raise SystemExit("physical qualification status is invalid")
if required and physical.get("status") != "qualified":
    raise SystemExit("CloudlessOS 1.0+ stable releases require physical qualification")
if physical.get("status") == "qualified":
    qualification_set = physical.get("qualificationSet")
    if not isinstance(qualification_set, dict):
        raise SystemExit("qualified release is missing its physical qualification set")
    canonical = json.dumps(qualification_set, sort_keys=True, separators=(",", ":")).encode()
    if hashlib.sha256(canonical).hexdigest() != physical.get("qualificationSetSha256"):
        raise SystemExit("physical qualification set digest is invalid")
    if qualification_set.get("version") != version or qualification_set.get("sourceCommit") != commit:
        raise SystemExit("physical qualification set identity does not match the release")
PY

echo "==> Validating security operations readiness"
test -s "$SECURITY_READINESS" || { echo "Missing security readiness descriptor" >&2; exit 1; }
python3 - "$SECURITY_READINESS" "$VERSION" "$CHANNEL" "$commit" <<'PY'
import json, sys
path, version, channel, commit = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    readiness = json.load(handle)
required = channel == "stable" and int(version.split(".", 1)[0]) >= 1
if readiness.get("schema") != "cloudless.security-readiness.v1":
    raise SystemExit("invalid security readiness descriptor schema")
for field, expected in (
    ("version", version), ("channel", channel), ("sourceCommit", commit), ("required", required),
):
    if readiness.get(field) != expected:
        raise SystemExit(f"security readiness {field} mismatch")
if readiness.get("status") not in {"operational", "not-operational"}:
    raise SystemExit("security readiness status is invalid")
if required and readiness.get("status") != "operational":
    raise SystemExit("CloudlessOS 1.0+ stable releases require operational security response ownership")
if readiness.get("status") == "operational":
    if readiness.get("monitored") is not True or not readiness.get("securityContact") or not readiness.get("escalationOwner"):
        raise SystemExit("operational security readiness is incomplete")
    contact = urlparse(readiness["securityContact"])
    valid_contact = (
        (contact.scheme == "mailto" and "@" in contact.path and not contact.query and not contact.fragment) or
        (contact.scheme == "https" and bool(contact.netloc) and not contact.username and not contact.password)
    )
    if not valid_contact:
        raise SystemExit("operational security contact is invalid")
    try:
        verified = dt.datetime.fromisoformat(readiness.get("verifiedAt", "").replace("Z", "+00:00"))
        if verified.tzinfo is None:
            raise ValueError("timezone missing")
        verified = verified.astimezone(dt.timezone.utc)
    except (AttributeError, ValueError):
        raise SystemExit("security verification timestamp is invalid")
    now = dt.datetime.now(dt.timezone.utc)
    if verified > now + dt.timedelta(minutes=5) or now - verified > dt.timedelta(days=30):
        raise SystemExit("security verification timestamp is outside the allowed 30-day window")
    target = readiness.get("acknowledgementBusinessDays")
    if not isinstance(target, int) or isinstance(target, bool) or not 1 <= target <= 3:
        raise SystemExit("security acknowledgement target exceeds release policy")
PY

matrix_sha="$(sha256sum "$MATRIX" | awk '{print $1}')"
mkdir -p "$(dirname "$OUT")"
python3 - "$OUT.tmp" "$VERSION" "$CHANNEL" "$commit" "$matrix_sha" "$MATRIX" "$PHYSICAL_QUALIFICATION" "$SECURITY_READINESS" <<'PY'
import datetime, json, os, sys
output, version, channel, commit, matrix_sha, matrix_path, physical_path, security_path = sys.argv[1:]
with open(matrix_path, encoding="utf-8") as handle:
    matrix = json.load(handle)
with open(physical_path, encoding="utf-8") as handle:
    physical = json.load(handle)
with open(security_path, encoding="utf-8") as handle:
    security = json.load(handle)
document = {
    "schema": "cloudless.release-gates.v1",
    "version": version,
    "channel": channel,
    "sourceCommit": commit,
    "completedAt": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
    "matrixSha256": matrix_sha,
    "targets": matrix["targets"],
    "passedGates": matrix["requiredGates"],
    "physicalQualification": physical,
    "securityReadiness": security,
}
with open(output, "w", encoding="utf-8") as handle:
    json.dump(document, handle, ensure_ascii=False, separators=(",", ":"))
    handle.write("\n")
os.replace(output, output.removesuffix(".tmp"))
PY
echo "Release gate attestation ready: $OUT"
