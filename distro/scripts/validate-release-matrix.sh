#!/usr/bin/env bash
# Produce the exact-commit validation attestation embedded in the signed release.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
MATRIX="$ROOT/distro/release/validation-matrix.json"
OUT="$ROOT/distro/out/release-gates.json"
PACKAGES="${CLOUDLESS_PACKAGE_OUT:-$ROOT/distro/out/packages}"

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
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    matrix = json.load(handle)
if matrix.get("schema") != "cloudless.release-validation.v1":
    raise SystemExit("unsupported release validation matrix schema")
targets = {(item.get("platform"), item.get("architecture")) for item in matrix.get("targets", [])}
required = {("generic", "amd64"), ("generic", "arm64"), ("dgx-spark", "arm64")}
if targets != required:
    raise SystemExit(f"matrix targets must be exactly {sorted(required)}")
gates = set(matrix.get("requiredGates", []))
expected = {"go-tests", "go-vet", "web-javascript", "app-manifest-v2", "platform-matrix", "package-architecture", "package-contents", "release-isolation", "atomic-repository"}
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

matrix_sha="$(sha256sum "$MATRIX" | awk '{print $1}')"
mkdir -p "$(dirname "$OUT")"
python3 - "$OUT.tmp" "$VERSION" "$CHANNEL" "$commit" "$matrix_sha" "$MATRIX" <<'PY'
import datetime, json, os, sys
output, version, channel, commit, matrix_sha, matrix_path = sys.argv[1:]
with open(matrix_path, encoding="utf-8") as handle:
    matrix = json.load(handle)
document = {
    "schema": "cloudless.release-gates.v1",
    "version": version,
    "channel": channel,
    "sourceCommit": commit,
    "completedAt": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
    "matrixSha256": matrix_sha,
    "targets": matrix["targets"],
    "passedGates": matrix["requiredGates"],
}
with open(output, "w", encoding="utf-8") as handle:
    json.dump(document, handle, ensure_ascii=False, separators=(",", ":"))
    handle.write("\n")
os.replace(output, output.removesuffix(".tmp"))
PY
echo "Release gate attestation ready: $OUT"
