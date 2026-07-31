#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DISTRO="$ROOT/distro"
VERSION="${1:-}"
CHANNEL="${2:-stable}"
REPO="${CLOUDLESS_APT_REPO_OUT:-$DISTRO/out/apt-repository}"
PACKAGE_OUT="${CLOUDLESS_PACKAGE_OUT:-$DISTRO/out/packages}"
BASE_URL="${CLOUDLESS_APT_BASE_URL:-https://updates.becloudless.ai/apt}"
KEY="${CLOUDLESS_ARCHIVE_KEY:-$DISTRO/release/keys/cloudless-archive-keyring.pgp}"
FINGERPRINT_FILE="${CLOUDLESS_ARCHIVE_FINGERPRINT_FILE:-$DISTRO/release/keys/cloudless-archive-fingerprint.txt}"
NOTES="${CLOUDLESS_RELEASE_NOTES:-$DISTRO/release/notes/$VERSION.json}"
APP_MANIFEST="${CLOUDLESS_APP_MANIFEST:-$DISTRO/release/manifests/cloudless-apps-manifest.json}"
MODEL_MANIFEST="${CLOUDLESS_MODEL_MANIFEST:-$ROOT/docs/cloudless-models.json}"
DIFFUSION_MANIFEST="${CLOUDLESS_DIFFUSION_MANIFEST:-$ROOT/docs/cloudless-diffusion.json}"
DGX_INSTALLER="${CLOUDLESS_DGX_INSTALLER:-$DISTRO/scripts/install-dgx-spark.sh}"
RELEASE_GATES="${CLOUDLESS_RELEASE_GATES:-$DISTRO/out/release-gates.json}"
SBOM="${CLOUDLESS_SBOM:-$DISTRO/out/security/cloudless-$VERSION.spdx.json}"
PHYSICAL_QUALIFICATION="${CLOUDLESS_PHYSICAL_QUALIFICATION:-$DISTRO/out/qualification/cloudless-physical-qualification.json}"
SECURITY_READINESS="${CLOUDLESS_SECURITY_READINESS:-$DISTRO/out/qualification/cloudless-security-readiness.json}"
CI_QUALIFICATION="${CLOUDLESS_CI_QUALIFICATION:-$DISTRO/out/qualification/cloudless-ci-qualification.json}"
PROMOTION="${CLOUDLESS_BETA_PROMOTION:-}"
SOURCE_COMMIT="${CLOUDLESS_SOURCE_COMMIT:-$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)}"
PACKAGES=(cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater)
read -r -a ARCHES <<< "${CLOUDLESS_ARCHES:-amd64 arm64}"

case "$REPO" in
    ""|/) echo "Refusing unsafe APT repository output path: $REPO" >&2; exit 2 ;;
esac

if [ -z "$VERSION" ] || [[ "$VERSION" == *dev* ]]; then
    echo "Usage: $0 VERSION [stable|beta] (development versions cannot be published)" >&2
    exit 2
fi
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
for arch in "${ARCHES[@]}"; do
    case "$arch" in amd64|arm64) ;; *) echo "Unsupported release architecture: $arch" >&2; exit 2 ;; esac
done
for command in curl dpkg dpkg-deb go gpg gpgv python3 reprepro sha256sum; do command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 1; }; done
test -s "$KEY" || { echo "Initialize the archive signing key first." >&2; exit 1; }
test -s "$FINGERPRINT_FILE" || { echo "Missing archive fingerprint file." >&2; exit 1; }
test -s "$NOTES" || { echo "Missing release notes: $NOTES" >&2; exit 1; }
test -s "$APP_MANIFEST" || { echo "Missing application manifest: $APP_MANIFEST" >&2; exit 1; }
test -s "$MODEL_MANIFEST" || { echo "Missing model manifest: $MODEL_MANIFEST" >&2; exit 1; }
test -s "$DIFFUSION_MANIFEST" || { echo "Missing diffusion manifest: $DIFFUSION_MANIFEST" >&2; exit 1; }
test -s "$DGX_INSTALLER" || { echo "Missing DGX Spark installer: $DGX_INSTALLER" >&2; exit 1; }
test -s "$RELEASE_GATES" || { echo "Missing release-gate attestation. Run distro/scripts/release.sh so every required gate executes before signing." >&2; exit 1; }
test -s "$PHYSICAL_QUALIFICATION" || { echo "Missing physical qualification descriptor: $PHYSICAL_QUALIFICATION" >&2; exit 1; }
test -s "$SECURITY_READINESS" || { echo "Missing security readiness descriptor: $SECURITY_READINESS" >&2; exit 1; }
test -s "$CI_QUALIFICATION" || { echo "Missing CI qualification descriptor: $CI_QUALIFICATION" >&2; exit 1; }
if [ -n "$PROMOTION" ]; then
    [ "$CHANNEL" = stable ] || { echo "Beta promotion input is valid only for stable releases." >&2; exit 1; }
    test -s "$PROMOTION/cloudless-release.json" -a -s "$PROMOTION/cloudless-beta-promotion.json" \
        -a -d "$PROMOTION/packages" -a -d "$PROMOTION/artifacts" || {
        echo "Incomplete beta promotion snapshot: $PROMOTION" >&2
        exit 1
    }
    promotion_available_at="$(python3 - "$PROMOTION/cloudless-beta-promotion.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    promotion = json.load(handle)
if promotion.get("schema") != "cloudless.beta-promotion.v1" or not promotion.get("betaAvailableAt"):
    raise SystemExit("invalid beta promotion availability evidence")
print(promotion["betaAvailableAt"])
PY
)"
    python3 "$DISTRO/scripts/verify-beta-promotion.py" \
        "$PROMOTION/cloudless-release.json" "$PROMOTION/packages" "$PROMOTION/artifacts" \
        "$VERSION" "$SOURCE_COMMIT" --publicly-available-at "$promotion_available_at" \
        --output "$PROMOTION/cloudless-beta-promotion.json"
fi
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || {
    echo "A full Git source commit is required for a production release." >&2
    exit 1
}
python3 - "$NOTES" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    notes = json.load(handle)
if not isinstance(notes.get("title"), str) or not notes["title"].strip():
    raise SystemExit("Release notes require a non-empty title")
if not isinstance(notes.get("changes"), list) or not notes["changes"] or not all(isinstance(item, str) and item.strip() for item in notes["changes"]):
    raise SystemExit("Release notes require at least one non-empty change")
PY
python3 - "$RELEASE_GATES" "$VERSION" "$CHANNEL" "$SOURCE_COMMIT" "$DISTRO/release/validation-matrix.json" "$PHYSICAL_QUALIFICATION" "$SECURITY_READINESS" "$CI_QUALIFICATION" <<'PY'
import hashlib, json, sys
path, version, channel, commit, matrix_path, physical_path, security_path, ci_path = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    gates = json.load(handle)
with open(matrix_path, "rb") as handle:
    matrix_hash = hashlib.sha256(handle.read()).hexdigest()
if gates.get("schema") != "cloudless.release-gates.v1":
    raise SystemExit("invalid release-gate schema")
for field, expected in (("version", version), ("channel", channel), ("sourceCommit", commit), ("matrixSha256", matrix_hash)):
    if gates.get(field) != expected:
        raise SystemExit(f"release-gate {field} mismatch: expected {expected}, got {gates.get(field)}")
if not gates.get("passedGates") or not gates.get("targets"):
    raise SystemExit("release-gate attestation is incomplete")
physical = gates.get("physicalQualification")
if not isinstance(physical, dict) or physical.get("schema") != "cloudless.physical-release.v1":
    raise SystemExit("release-gate attestation is missing physical qualification identity")
if physical.get("version") != version or physical.get("channel") != channel or physical.get("sourceCommit") != commit:
    raise SystemExit("physical qualification identity does not match the release")
required_physical = channel == "stable" and int(version.split(".", 1)[0]) >= 1
if physical.get("required") != required_physical or physical.get("status") not in {"qualified", "not-qualified"}:
    raise SystemExit("physical qualification policy is invalid")
if required_physical and physical.get("status") != "qualified":
    raise SystemExit("CloudlessOS 1.0+ stable releases require physical qualification")
if physical.get("status") == "qualified":
    qualification_set = physical.get("qualificationSet")
    canonical = json.dumps(qualification_set, sort_keys=True, separators=(",", ":")).encode()
    if not isinstance(qualification_set, dict) or hashlib.sha256(canonical).hexdigest() != physical.get("qualificationSetSha256"):
        raise SystemExit("physical qualification set digest is invalid")
with open(physical_path, encoding="utf-8") as handle:
    if json.load(handle) != physical:
        raise SystemExit("physical qualification descriptor changed after release gates were created")
security = gates.get("securityReadiness")
if not isinstance(security, dict) or security.get("schema") != "cloudless.security-readiness.v1":
    raise SystemExit("release-gate attestation is missing security readiness identity")
required_security = channel == "stable" and int(version.split(".", 1)[0]) >= 1
if security.get("required") != required_security or security.get("status") not in {"operational", "not-operational"}:
    raise SystemExit("security readiness policy is invalid")
for field, expected in (("version", version), ("channel", channel), ("sourceCommit", commit)):
    if security.get(field) != expected:
        raise SystemExit(f"security readiness {field} mismatch")
if required_security and security.get("status") != "operational":
    raise SystemExit("CloudlessOS 1.0+ stable releases require operational security response ownership")
with open(security_path, encoding="utf-8") as handle:
    if json.load(handle) != security:
        raise SystemExit("security readiness descriptor changed after release gates were created")
ci = gates.get("ciQualification")
if not isinstance(ci, dict) or ci.get("schema") != "cloudless.ci-qualification.v1":
    raise SystemExit("release-gate attestation is missing CI qualification identity")
required_ci = channel == "stable" and int(version.split(".", 1)[0]) >= 1
if ci.get("required") != required_ci or ci.get("status") not in {"qualified", "not-qualified"}:
    raise SystemExit("CI qualification policy is invalid")
for field, expected in (("version", version), ("channel", channel), ("sourceCommit", commit)):
    if ci.get(field) != expected:
        raise SystemExit(f"CI qualification {field} mismatch")
if required_ci and ci.get("status") != "qualified":
    raise SystemExit("CloudlessOS 1.0+ stable releases require exact-commit CI qualification")
if ci.get("status") == "qualified":
    if ci.get("workflow") != ".github/workflows/multiarch.yml" or not isinstance(ci.get("runId"), int) or ci["runId"] <= 0 or not isinstance(ci.get("runAttempt"), int) or ci["runAttempt"] <= 0:
        raise SystemExit("CI qualification workflow evidence is invalid")
    jobs = {"Source, concurrency and security policies", "generic / amd64", "generic / arm64", "dgx-spark / arm64", "AMD64 and ARM64 package payloads", "Interface capture smoke test", "Durable lifecycle soak"}
    if set(ci.get("jobs", [])) != jobs:
        raise SystemExit("CI qualification job evidence is incomplete")
    artifacts = {f"{prefix}-{ci['runId']}-{ci['runAttempt']}" for prefix in ("package-qualification", "visual-regression", "lifecycle-soak")}
    if set(ci.get("artifacts", [])) != artifacts:
        raise SystemExit("CI qualification evidence is incomplete")
with open(ci_path, encoding="utf-8") as handle:
    if json.load(handle) != ci:
        raise SystemExit("CI qualification descriptor changed after release gates were created")
PY
fingerprint="$(tr -d '[:space:]' < "$FINGERPRINT_FILE")"
if [ "${CLOUDLESS_RELEASE_DRY_RUN:-0}" != "1" ]; then
    gpg --batch --list-secret-keys "$fingerprint" >/dev/null 2>&1 || {
        echo "The secret signing key is not loaded in GNUPGHOME." >&2
        exit 1
    }
fi

for arch in "${ARCHES[@]}"; do
    CLOUDLESS_VERSION="$VERSION" CLOUDLESS_ARCH="$arch" CLOUDLESS_PACKAGE_OUT="$PACKAGE_OUT" "$DISTRO/scripts/build-packages.sh"
done
CLOUDLESS_SBOM_PACKAGE_DIR="$PACKAGE_OUT" CLOUDLESS_SOURCE_COMMIT="$SOURCE_COMMIT" \
    python3 "$DISTRO/scripts/generate-sbom.py" "$VERSION" "$SBOM"
test -s "$SBOM" || { echo "Final release SBOM was not generated: $SBOM" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/baseline"

declare -A PROMOTED_DEB
if [ -n "$PROMOTION" ]; then
    python3 - "$PROMOTION/cloudless-release.json" <<'PY' > "$work/promoted-packages.tsv"
import json, pathlib, sys
release = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
for item in release["packages"]:
    print(item["architecture"], item["name"], item["version"], pathlib.PurePosixPath(item["filename"]).name, sep="\t")
PY
    while IFS=$'\t' read -r arch package package_version filename; do
        key="$arch/$package"
        candidate="$PROMOTION/packages/$filename"
        test -s "$candidate" || { echo "Missing promoted beta package: $filename" >&2; exit 1; }
        [ "$(dpkg-deb -f "$candidate" Package)" = "$package" ] && \
            [ "$(dpkg-deb -f "$candidate" Architecture)" = "$arch" ] && \
            [ "$(dpkg-deb -f "$candidate" Version)" = "$package_version" ] || {
            echo "Promoted package metadata mismatch: $filename" >&2
            exit 1
        }
        local_candidate="$PACKAGE_OUT/${package}_${VERSION}_${arch}.deb"
        test -s "$local_candidate" || { echo "Missing locally validated candidate: $local_candidate" >&2; exit 1; }
        bash "$DISTRO/scripts/package-content-equal.sh" "$candidate" "$local_candidate" || {
            echo "Published beta package payload does not match the current source candidate: $package/$arch" >&2
            exit 1
        }
        PROMOTED_DEB[$key]="$candidate"
    done < "$work/promoted-packages.tsv"
fi

fetch_remote_baseline() {
    local arch="$1" inrelease="$work/InRelease" verified="$work/InRelease.verified" index="$work/Packages-$1" index_hash index_size
    echo "==> Recovering current $CHANNEL/$arch package baseline from $BASE_URL"
    if [ ! -s "$verified" ]; then
        rm -f "$inrelease" "$verified"
        curl -fsS "$BASE_URL/dists/$CHANNEL/InRelease" -o "$inrelease" || return 1
        if ! gpgv --keyring "$KEY" "$inrelease" >/dev/null 2>&1; then
            rm -f "$inrelease"
            return 1
        fi
        printf '%s\n' "$fingerprint" > "$verified"
    fi
    # A valid repository may not publish this architecture yet. Distinguish
    # that first-architecture case from a network or signature failure.
    if ! grep -Eq "^ [0-9a-f]+ +[0-9]+ +main/binary-${arch}/Packages$" "$inrelease"; then
        return 2
    fi
    curl -fsS "$BASE_URL/dists/$CHANNEL/main/binary-$arch/Packages" -o "$index" || return 1
    index_hash="$(sha256sum "$index" | awk '{print $1}')"
    index_size="$(wc -c < "$index" | tr -d '[:space:]')"
    grep -Eq "^ ${index_hash} +${index_size} +main/binary-${arch}/Packages$" "$inrelease" || return 1

    local package paragraph filename checksum target
    mkdir -p "$work/baseline/$arch"
    for package in "${PACKAGES[@]}"; do
        paragraph="$(awk -v package="$package" 'BEGIN {RS=""} $0 ~ "(^|\\n)Package: " package "(\\n|$)" {print; exit}' "$index")"
        [ -n "$paragraph" ] || continue
        filename="$(printf '%s\n' "$paragraph" | awk '/^Filename: / {print $2; exit}')"
        checksum="$(printf '%s\n' "$paragraph" | awk '/^SHA256: / {print $2; exit}')"
        [ -n "$filename" ] && [ -n "$checksum" ] || return 1
        target="$work/baseline/$arch/$(basename "$filename")"
        curl -fsS "$BASE_URL/$filename" -o "$target" || return 1
        printf '%s  %s\n' "$checksum" "$target" | sha256sum --check --status || return 1
        printf '%s\n' "$filename" > "$target.filename"
    done
}

declare -A REMOTE_BASELINE PREVIOUS_DEB PREVIOUS_FILENAME PREVIOUS_VERSION CHANGED
for arch in "${ARCHES[@]}"; do
    # The signed public repository is the release baseline. A local repository
    # can contain packages left behind by an interrupted or repeated signing
    # attempt, so using it first can incorrectly hide a package from the
    # release manifest.
    if fetch_remote_baseline "$arch"; then
        REMOTE_BASELINE[$arch]=true
    else
        status=$?
        REMOTE_BASELINE[$arch]=false
        if [ "$status" -ne 2 ] && [ -s "$REPO/db/packages.db" ]; then
            echo "Unable to verify the public $CHANNEL/$arch baseline; refusing to use cached local packages." >&2
            exit 1
        fi
        echo "==> No published $CHANNEL/$arch baseline found; treating it as a first architecture release"
        rm -rf "$work/baseline/$arch"
    fi
done

changed_count=0
for arch in "${ARCHES[@]}"; do
    for package in "${PACKAGES[@]}"; do
        key="$arch/$package"
        previous=""
        if ${REMOTE_BASELINE[$arch]}; then
            previous="$(find "$work/baseline/$arch" -type f -name "${package}_*_${arch}.deb" -print -quit 2>/dev/null || true)"
        fi
        if [ -n "$PROMOTION" ]; then
            candidate="${PROMOTED_DEB[$key]:-}"
        else
            candidate="$PACKAGE_OUT/${package}_${VERSION}_${arch}.deb"
        fi
        test -s "$candidate" || { echo "Missing candidate package: $candidate" >&2; exit 1; }
        if [ -n "$previous" ]; then
            PREVIOUS_DEB[$key]="$previous"
            PREVIOUS_FILENAME[$key]="$(cat "$previous.filename")"
            PREVIOUS_VERSION[$key]="$(dpkg-deb -f "$previous" Version)"
            if [ -n "$PROMOTION" ] && cmp -s "$previous" "$candidate"; then
                echo "==> Unchanged: $package/$arch (${PREVIOUS_VERSION[$key]})"
                CHANGED[$key]=false
                continue
            fi
            if [ -z "$PROMOTION" ] && bash "$DISTRO/scripts/package-content-equal.sh" "$previous" "$candidate"; then
                echo "==> Unchanged: $package/$arch (${PREVIOUS_VERSION[$key]})"
                CHANGED[$key]=false
                continue
            fi
            candidate_version="$(dpkg-deb -f "$candidate" Version)"
            dpkg --compare-versions "$candidate_version" gt "${PREVIOUS_VERSION[$key]}" || {
                echo "$candidate_version must be newer than ${PREVIOUS_VERSION[$key]} for $package/$arch" >&2
                exit 1
            }
        fi
        echo "==> Changed: $package/$arch -> $(dpkg-deb -f "$candidate" Version)"
        CHANGED[$key]=true
        changed_count=$((changed_count + 1))
    done
done

if [ "$changed_count" -eq 0 ]; then
    echo "No Cloudless package contents changed; no release is needed." >&2
    exit 3
fi
if [ "${CLOUDLESS_RELEASE_DRY_RUN:-0}" = "1" ]; then
    echo "Dry run complete: $changed_count package(s) would be released."
    exit 0
fi

# Rebuild from a clean repository on every signing attempt. Package versions
# are immutable in APT, and an interrupted attempt may have left a different
# build of this same version in the local reprepro database.
rm -rf -- "$REPO"
install -d "$REPO/conf" "$REPO/releases"
cat > "$REPO/conf/distributions" <<EOF
Origin: Cloudless
Label: CloudlessOS
Codename: $CHANNEL
Suite: $CHANNEL
Architectures: ${ARCHES[*]}
Components: main
Description: Signed CloudlessOS $CHANNEL updates
SignWith: $fingerprint
EOF
cat > "$REPO/conf/options" <<'EOF'
verbose
ask-passphrase
EOF
for arch in "${ARCHES[@]}"; do
    for package in "${PACKAGES[@]}"; do
        key="$arch/$package"
        if [ -n "${PREVIOUS_DEB[$key]:-}" ]; then
            reprepro --basedir "$REPO" includedeb "$CHANNEL" "${PREVIOUS_DEB[$key]}"
        fi
    done
done
for arch in "${ARCHES[@]}"; do
    for package in "${PACKAGES[@]}"; do
        key="$arch/$package"
        if ${CHANGED[$key]}; then
            if [ -n "$PROMOTION" ]; then
                candidate="${PROMOTED_DEB[$key]}"
            else
                candidate="$PACKAGE_OUT/${package}_${VERSION}_${arch}.deb"
            fi
            reprepro --basedir "$REPO" includedeb "$CHANNEL" "$candidate"
        fi
    done
done

packages_file="$work/packages.tsv"
for arch in "${ARCHES[@]}"; do
    for package in "${PACKAGES[@]}"; do
        key="$arch/$package"
        if [ -n "$PROMOTION" ]; then
            candidate="${PROMOTED_DEB[$key]}"
        else
            candidate="$PACKAGE_OUT/${package}_${VERSION}_${arch}.deb"
        fi
        if ${CHANGED[$key]}; then
            current_version="$(dpkg-deb -f "$candidate" Version)"
            current_deb="$candidate"
            changed=true
        else
            current_version="${PREVIOUS_VERSION[$key]}"
            current_deb="${PREVIOUS_DEB[$key]}"
            changed=false
        fi
        current_pool="$(find "$REPO/pool" -type f -name "$(basename "$current_deb")" -print -quit)"
        test -s "$current_pool" || { echo "Current package is missing from repository: $package/$arch" >&2; exit 1; }
        current_filename="${current_pool#"$REPO"/}"
        rollback_version=""
        rollback_filename=""
        rollback_hash=""
        rollback_size=""
        if $changed && [ -n "${PREVIOUS_DEB[$key]:-}" ]; then
            rollback_version="${PREVIOUS_VERSION[$key]}"
            rollback_filename="${PREVIOUS_FILENAME[$key]}"
            rollback_hash="$(sha256sum "${PREVIOUS_DEB[$key]}" | awk '{print $1}')"
            rollback_size="$(wc -c < "${PREVIOUS_DEB[$key]}" | tr -d '[:space:]')"
        fi
        printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
            "$package" "$arch" "$current_version" "$changed" "$current_filename" \
            "$(sha256sum "$current_pool" | awk '{print $1}')" \
            "$(wc -c < "$current_pool" | tr -d '[:space:]')" \
            "$rollback_version" "$rollback_filename" "$rollback_hash" "$rollback_size" \
            >> "$packages_file"
    done
done
install -d "$REPO/releases"
artifacts_dir="$REPO/artifacts/$VERSION/$CHANNEL"
rm -rf "$artifacts_dir"
install -d "$artifacts_dir"
(
    cd "$ROOT/orchestrator"
    go run ./cmd/cloudless-trust-inventory -source-commit "$SOURCE_COMMIT"
) > "$work/cloudless-trust-inventory.json"
if [ -n "$PROMOTION" ]; then
    for source_and_name in \
        "$DGX_INSTALLER|install-dgx-spark.sh" \
        "$APP_MANIFEST|cloudless-apps-manifest.json" \
        "$MODEL_MANIFEST|cloudless-models.json" \
        "$DIFFUSION_MANIFEST|cloudless-diffusion.json" \
        "$work/cloudless-trust-inventory.json|cloudless-trust-inventory.json"; do
        source="${source_and_name%%|*}"
        name="${source_and_name#*|}"
        cmp -s "$source" "$PROMOTION/artifacts/$name" || {
            echo "Published beta artifact does not match the stable candidate: $name" >&2
            exit 1
        }
        install -m 0644 "$PROMOTION/artifacts/$name" "$artifacts_dir/$name"
    done
    # The beta SBOM inventories the exact beta Debian archives. Rebuilding the
    # same package payload can legitimately change ar/tar timestamps, so the
    # stable generation retains the signed beta SBOM rather than substituting a
    # newly generated look-alike document.
    install -m 0644 "$PROMOTION/artifacts/cloudless-$VERSION.spdx.json" \
        "$artifacts_dir/cloudless-$VERSION.spdx.json"
    chmod 0755 "$artifacts_dir/install-dgx-spark.sh"
else
    install -m 0755 "$DGX_INSTALLER" "$artifacts_dir/install-dgx-spark.sh"
    install -m 0644 "$APP_MANIFEST" "$artifacts_dir/cloudless-apps-manifest.json"
    install -m 0644 "$MODEL_MANIFEST" "$artifacts_dir/cloudless-models.json"
    install -m 0644 "$DIFFUSION_MANIFEST" "$artifacts_dir/cloudless-diffusion.json"
    install -m 0644 "$SBOM" "$artifacts_dir/cloudless-$VERSION.spdx.json"
    install -m 0644 "$work/cloudless-trust-inventory.json" "$artifacts_dir/cloudless-trust-inventory.json"
fi
install -m 0644 "$PHYSICAL_QUALIFICATION" "$artifacts_dir/cloudless-physical-qualification.json"
install -m 0644 "$SECURITY_READINESS" "$artifacts_dir/cloudless-security-readiness.json"
install -m 0644 "$CI_QUALIFICATION" "$artifacts_dir/cloudless-ci-qualification.json"
artifacts_file="$work/artifacts.tsv"
for artifact in install-dgx-spark.sh cloudless-apps-manifest.json cloudless-models.json cloudless-diffusion.json "cloudless-$VERSION.spdx.json" cloudless-trust-inventory.json cloudless-physical-qualification.json cloudless-security-readiness.json cloudless-ci-qualification.json; do
    file="$artifacts_dir/$artifact"
    signature="$file.asc"
    gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign \
        --output "$signature" "$file"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$artifact" \
        "artifacts/$VERSION/$CHANNEL/$artifact" \
        "$(sha256sum "$file" | awk '{print $1}')" \
        "$(wc -c < "$file" | tr -d '[:space:]')" \
        "$(sha256sum "$signature" | awk '{print $1}')" \
        "$(wc -c < "$signature" | tr -d '[:space:]')" \
        >> "$artifacts_file"
done
install -d "$REPO/releases/$VERSION"
manifest="$REPO/releases/$VERSION/$CHANNEL.json"
promotion_attestation=""
[ -z "$PROMOTION" ] || promotion_attestation="$PROMOTION/cloudless-beta-promotion.json"
python3 - "$NOTES" "$packages_file" "$artifacts_file" "$RELEASE_GATES" "$manifest" "$VERSION" "$CHANNEL" "$SOURCE_COMMIT" "$(date -u +%FT%TZ)" "$promotion_attestation" <<'PY'
import json, sys
notes_path, packages_path, artifacts_path, gates_path, output, version, channel, source_commit, published_at, promotion_path = sys.argv[1:]
with open(notes_path, encoding="utf-8") as handle:
    notes = json.load(handle)
packages = []
rollback_packages = []
with open(packages_path, encoding="utf-8") as handle:
    for line in handle:
        fields = line.rstrip("\n").split("\t")
        if len(fields) != 11:
            raise SystemExit("invalid package inventory record")
        name, architecture, current, changed, filename, sha256, size, previous, previous_filename, previous_sha256, previous_size = fields
        rollback = None
        if previous:
            rollback = {
                "version": previous,
                "filename": previous_filename,
                "sha256": previous_sha256,
                "size": int(previous_size),
            }
            rollback_packages.append({"name": name, "architecture": architecture, **rollback})
        packages.append({
            "name": name,
            "architecture": architecture,
            "version": current,
            "changed": changed == "true",
            "filename": filename,
            "sha256": sha256,
            "size": int(size),
            "rollback": rollback,
        })
artifacts = []
with open(artifacts_path, encoding="utf-8") as handle:
    for line in handle:
        name, path, sha256, size, signature_sha256, signature_size = line.rstrip("\n").split("\t")
        artifacts.append({
            "name": name,
            "path": path,
            "sha256": sha256,
            "size": int(size),
            "signature": path + ".asc",
            "signatureSha256": signature_sha256,
            "signatureSize": int(signature_size),
        })
with open(gates_path, encoding="utf-8") as handle:
    validation = json.load(handle)
manifest = {
    "schema": "cloudless.release.v2",
    "version": version,
    "channel": channel,
    "sourceCommit": source_commit,
    "publishedAt": published_at,
    "title": notes["title"].strip(),
    "summary": str(notes.get("summary", "")).strip(),
    "changes": [item.strip() for item in notes["changes"]],
    "packages": packages,
    "rollbackPackages": rollback_packages,
    "compatibility": {
        "schema": "cloudless.compatibility.v1",
        "matrixSha256": validation["matrixSha256"],
        "targets": validation["targets"],
    },
    "artifacts": artifacts,
    "validation": validation,
    "physicalQualification": validation["physicalQualification"],
    "securityReadiness": validation["securityReadiness"],
    "ciQualification": validation["ciQualification"],
}
if promotion_path:
    with open(promotion_path, encoding="utf-8") as handle:
        promotion = json.load(handle)
    if promotion.get("schema") != "cloudless.beta-promotion.v1" or promotion.get("version") != version or promotion.get("sourceCommit") != source_commit:
        raise SystemExit("beta promotion attestation does not match the stable release")
    promoted_packages = {(item["name"], item["architecture"]): item for item in promotion["packages"]}
    for item in packages:
        promoted = promoted_packages.get((item["name"], item["architecture"]))
        if not promoted or any(item[field] != promoted[field] for field in ("version", "sha256", "size")):
            raise SystemExit(f"stable package is not byte-identical to beta: {item['name']}/{item['architecture']}")
    promoted_artifacts = {item["name"]: item for item in promotion["artifacts"]}
    for item in artifacts:
        if item["name"] in {"cloudless-physical-qualification.json", "cloudless-security-readiness.json", "cloudless-ci-qualification.json"}:
            continue
        promoted = promoted_artifacts.get(item["name"])
        if not promoted or item["sha256"] != promoted["sha256"] or item["size"] != promoted["size"]:
            raise SystemExit(f"stable artifact is not byte-identical to beta: {item['name']}")
    manifest["promotion"] = promotion
with open(output, "w", encoding="utf-8") as handle:
    json.dump(manifest, handle, ensure_ascii=False, separators=(",", ":"))
    handle.write("\n")
PY
cp "$manifest" "$REPO/dists/$CHANNEL/cloudless-release.json"

# APT's by-hash protocol makes index promotion race-free: clients fetch the
# immutable SHA-256 path named by signed metadata instead of a mutable
# Packages filename that may be changing during publication.
release="$REPO/dists/$CHANNEL/Release"
while IFS= read -r index; do
    hash="$(sha256sum "$index" | awk '{print $1}')"
    by_hash="$(dirname "$index")/by-hash/SHA256/$hash"
    install -Dm0644 "$index" "$by_hash"
done < <(find "$REPO/dists/$CHANNEL" -type f \( -name Packages -o -name Packages.gz \) ! -path '*/by-hash/*' -print)
if ! grep -Fqx 'Acquire-By-Hash: yes' "$release"; then
    sed -i '/^Suite:/a Acquire-By-Hash: yes' "$release"
fi
manifest_hash="$(sha256sum "$REPO/dists/$CHANNEL/cloudless-release.json" | awk '{print $1}')"
manifest_size="$(wc -c < "$REPO/dists/$CHANNEL/cloudless-release.json" | tr -d '[:space:]')"
install -Dm0644 "$REPO/dists/$CHANNEL/cloudless-release.json" \
    "$REPO/dists/$CHANNEL/by-hash/SHA256/$manifest_hash"
sed -i '/ cloudless-release\.json$/d' "$release"
sed -i "/^SHA256:/a\\ $manifest_hash $manifest_size cloudless-release.json" "$release"
rm -f "$release.gpg" "$REPO/dists/$CHANNEL/InRelease"
gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign --output "$release.gpg" "$release"
gpg --batch --yes --local-user "$fingerprint" --clearsign --output "$REPO/dists/$CHANNEL/InRelease" "$release"

cp "$KEY" "$REPO/cloudless-archive-keyring.pgp"
echo "Signed APT repository ready at $REPO"
