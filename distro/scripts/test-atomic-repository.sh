#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="/usr/local/go/bin:$PATH"
export GNUPGHOME="$(mktemp -d)"
work="$(mktemp -d)"
TEST_VERSION="0.0.0-test-only"
trap 'rm -rf "$GNUPGHOME" "$work"' EXIT
chmod 0700 "$GNUPGHOME"

gpg --batch --passphrase '' --quick-generate-key \
    'Cloudless Publish Test <test@invalid>' ed25519 sign 1d
fingerprint="$(gpg --batch --with-colons --list-keys | awk -F: '$1 == "fpr" {print $10; exit}')"
gpg --batch --export "$fingerprint" > "$work/test-keyring.pgp"
printf '%s\n' "$fingerprint" > "$work/test-fingerprint.txt"
cat > "$work/test-notes.json" <<'EOF'
{"title":"Test release","summary":"Signed test notes.","changes":["Shows verified release notes before installation."]}
EOF
matrix_sha="$(sha256sum "$ROOT/distro/release/validation-matrix.json" | awk '{print $1}')"
cat > "$work/test-physical-qualification.json" <<EOF
{"schema":"cloudless.physical-release.v1","status":"not-qualified","required":false,"version":"$TEST_VERSION","channel":"stable","sourceCommit":"0123456789abcdef0123456789abcdef01234567","matrixSha256":"$matrix_sha","preparedAt":"2000-01-01T00:00:00Z","reason":"Test release has no physical evidence."}
EOF
cat > "$work/test-security-readiness.json" <<EOF
{"schema":"cloudless.security-readiness.v1","status":"not-operational","required":false,"version":"$TEST_VERSION","channel":"stable","sourceCommit":"0123456789abcdef0123456789abcdef01234567","preparedAt":"2000-01-01T00:00:00Z","reason":"Test release has no operational security attestation."}
EOF
cat > "$work/test-ci-qualification.json" <<EOF
{"schema":"cloudless.ci-qualification.v1","status":"not-qualified","required":false,"version":"$TEST_VERSION","channel":"stable","sourceCommit":"0123456789abcdef0123456789abcdef01234567","preparedAt":"2000-01-01T00:00:00Z","reason":"Test release has no CI qualification evidence."}
EOF
cat > "$work/test-release-gates.json" <<EOF
{"schema":"cloudless.release-gates.v1","version":"$TEST_VERSION","channel":"stable","sourceCommit":"0123456789abcdef0123456789abcdef01234567","completedAt":"2000-01-01T00:00:00Z","matrixSha256":"$matrix_sha","targets":[{"platform":"generic","architecture":"amd64"},{"platform":"generic","architecture":"arm64"},{"platform":"dgx-spark","architecture":"arm64"}],"passedGates":["go-tests","go-vet","web-javascript","app-manifest-v2","backup-recovery","installer-preflight","platform-matrix","package-architecture","package-contents","package-lifecycle","release-isolation","release-preflight","secret-hygiene","service-hardening","sbom","trust-inventory","vulnerability-scan","updater-workload-continuity","atomic-repository"],"physicalQualification":$(cat "$work/test-physical-qualification.json"),"securityReadiness":$(cat "$work/test-security-readiness.json"),"ciQualification":$(cat "$work/test-ci-qualification.json")}
EOF

CLOUDLESS_APT_REPO_OUT="$work/repository" \
CLOUDLESS_PACKAGE_OUT="$work/packages" \
CLOUDLESS_APT_BASE_URL=https://invalid.invalid \
CLOUDLESS_ARCHIVE_KEY="$work/test-keyring.pgp" \
CLOUDLESS_ARCHIVE_FINGERPRINT_FILE="$work/test-fingerprint.txt" \
CLOUDLESS_RELEASE_NOTES="$work/test-notes.json" \
CLOUDLESS_RELEASE_GATES="$work/test-release-gates.json" \
CLOUDLESS_PHYSICAL_QUALIFICATION="$work/test-physical-qualification.json" \
CLOUDLESS_SECURITY_READINESS="$work/test-security-readiness.json" \
CLOUDLESS_CI_QUALIFICATION="$work/test-ci-qualification.json" \
CLOUDLESS_SBOM="$work/cloudless-$TEST_VERSION.spdx.json" \
CLOUDLESS_SOURCE_COMMIT=0123456789abcdef0123456789abcdef01234567 \
    bash "$ROOT/distro/scripts/build-apt-repository.sh" "$TEST_VERSION" stable

repo="$work/repository"
gpgv --keyring "$work/test-keyring.pgp" "$repo/dists/stable/InRelease"
grep -Fqx 'Acquire-By-Hash: yes' "$repo/dists/stable/Release"
grep -Fq ' cloudless-release.json' "$repo/dists/stable/Release"
release_manifest="$repo/releases/$TEST_VERSION/stable.json"
artifacts="$repo/artifacts/$TEST_VERSION/stable"
cmp "$release_manifest" "$repo/dists/stable/cloudless-release.json"
grep -Fq '"architecture":"amd64"' "$release_manifest"
grep -Fq '"architecture":"arm64"' "$release_manifest"
grep -Fq '"schema":"cloudless.release.v2"' "$release_manifest"
test -s "$artifacts/cloudless-trust-inventory.json"
test -s "$artifacts/cloudless-trust-inventory.json.asc"
test -s "$artifacts/cloudless-physical-qualification.json.asc"
test -s "$artifacts/cloudless-security-readiness.json.asc"
test -s "$artifacts/cloudless-ci-qualification.json.asc"
grep -Fq '"schema":"cloudless.trust-inventory.v1"' "$artifacts/cloudless-trust-inventory.json"
grep -Fq '"sourceCommit":"0123456789abcdef0123456789abcdef01234567"' "$release_manifest"
grep -Fq '"install-dgx-spark.sh"' "$release_manifest"
for artifact in cloudless-apps-manifest.json cloudless-models.json cloudless-diffusion.json; do
    grep -Fq "\"$artifact\"" "$release_manifest"
done
grep -Fq "\"cloudless-$TEST_VERSION.spdx.json\"" "$release_manifest"
python3 - "$release_manifest" "$repo" <<'PY'
import hashlib, json, pathlib, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    release = json.load(handle)
if len(release["packages"]) != 12 or release["rollbackPackages"]:
    raise SystemExit("first release package/rollback inventory is incomplete")
if release["compatibility"]["matrixSha256"] != release["validation"]["matrixSha256"]:
    raise SystemExit("compatibility matrix is not bound to validation")
if release["physicalQualification"] != release["validation"]["physicalQualification"]:
    raise SystemExit("physical qualification is not bound to validation")
if release["securityReadiness"] != release["validation"]["securityReadiness"]:
    raise SystemExit("security readiness is not bound to validation")
if release["ciQualification"] != release["validation"]["ciQualification"]:
    raise SystemExit("CI qualification is not bound to validation")
root = pathlib.Path(sys.argv[2])
for artifact in release["artifacts"]:
    prefix = f"artifacts/{release['version']}/{release['channel']}/"
    if not artifact["path"].startswith(prefix) or not artifact["signature"].startswith(prefix):
        raise SystemExit(f"artifact is not channel scoped: {artifact['name']}")
for package in release["packages"]:
    data = (root / package["filename"]).read_bytes()
    if len(data) != package["size"] or hashlib.sha256(data).hexdigest() != package["sha256"]:
        raise SystemExit(f"package inventory mismatch: {package['filename']}")
PY
for artifact in install-dgx-spark.sh cloudless-apps-manifest.json cloudless-models.json cloudless-diffusion.json cloudless-trust-inventory.json cloudless-physical-qualification.json cloudless-security-readiness.json cloudless-ci-qualification.json "cloudless-$TEST_VERSION.spdx.json"; do
    gpgv --keyring "$work/test-keyring.pgp" \
        "$artifacts/$artifact.asc" \
        "$artifacts/$artifact"
done
tampered="$work/tampered-repository"
cp -a "$repo" "$tampered"
printf '\n# tampered\n' >> "$tampered/artifacts/$TEST_VERSION/stable/install-dgx-spark.sh"
if CLOUDLESS_APT_REPO_OUT="$tampered" \
   CLOUDLESS_R2_ENDPOINT=https://invalid.invalid \
   CLOUDLESS_R2_BUCKET=invalid \
   CLOUDLESS_PUBLISH_VERIFY_ONLY=1 \
   bash "$ROOT/distro/scripts/publish-apt-r2.sh" stable >/dev/null 2>&1; then
    echo "Publisher accepted a tampered signed artifact" >&2
    exit 1
fi
for arch in amd64 arm64; do
    for index in \
        "$repo/dists/stable/main/binary-$arch/Packages" \
        "$repo/dists/stable/main/binary-$arch/Packages.gz"; do
        hash="$(sha256sum "$index" | awk '{print $1}')"
        cmp "$index" "$(dirname "$index")/by-hash/SHA256/$hash"
    done
done
manifest_hash="$(sha256sum "$repo/dists/stable/cloudless-release.json" | awk '{print $1}')"
cmp "$repo/dists/stable/cloudless-release.json" \
    "$repo/dists/stable/by-hash/SHA256/$manifest_hash"

# Produce a second, independently signed metadata generation without rebuilding
# package payloads. Its only change is the signed release summary, which is
# enough to prove that interrupted promotion never mixes mutable metadata with
# an immutable generation.
next_repo="$work/next-repository"
cp -a "$repo" "$next_repo"
python3 - "$next_repo/dists/stable/cloudless-release.json" "$next_repo/releases/$TEST_VERSION/stable.json" <<'PY'
import json, os, sys
for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as handle:
        release = json.load(handle)
    release["summary"] = "A distinct atomic-publication generation."
    temporary = path + ".tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        json.dump(release, handle, ensure_ascii=False, separators=(",", ":"))
        handle.write("\n")
    os.replace(temporary, path)
PY
next_manifest="$next_repo/dists/stable/cloudless-release.json"
next_release="$next_repo/dists/stable/Release"
next_hash="$(sha256sum "$next_manifest" | awk '{print $1}')"
next_size="$(wc -c < "$next_manifest" | tr -d '[:space:]')"
install -Dm0644 "$next_manifest" "$next_repo/dists/stable/by-hash/SHA256/$next_hash"
sed -i '/ cloudless-release\.json$/d' "$next_release"
sed -i "/^SHA256:/a\\ $next_hash $next_size cloudless-release.json" "$next_release"
rm -f "$next_repo/dists/stable/Release.gpg" "$next_repo/dists/stable/InRelease"
gpg --batch --yes --local-user "$fingerprint" --armor --detach-sign \
    --output "$next_repo/dists/stable/Release.gpg" "$next_release"
gpg --batch --yes --local-user "$fingerprint" --clearsign \
    --output "$next_repo/dists/stable/InRelease" "$next_release"

mkdir -p "$work/bin" "$work/fake-r2"
cat > "$work/bin/aws" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_AWS_LOG"
if [ -n "${FAKE_AWS_COUNT_FILE:-}" ]; then
    count=0
    [ ! -f "$FAKE_AWS_COUNT_FILE" ] || read -r count < "$FAKE_AWS_COUNT_FILE"
    count=$((count + 1))
    printf '%s\n' "$count" > "$FAKE_AWS_COUNT_FILE"
    if [ -n "${FAKE_AWS_FAIL_AT:-}" ] && [ "$count" -eq "$FAKE_AWS_FAIL_AT" ]; then
        echo "Injected object-store failure at upload $count" >&2
        exit 75
    fi
fi
[ "$1 $2" = "s3 cp" ] || { echo "Unsupported fake AWS command: $*" >&2; exit 2; }
src="${3%/}"
dest="$4"
relative="${dest#s3://}"
relative="${relative#*/}"
target="$FAKE_R2/$relative"
if [ -d "$src" ]; then
    mkdir -p "$target"
    if [[ " $* " == *" --include */by-hash/* "* ]]; then
        while IFS= read -r -d '' file; do
            rel="${file#"$src"/}"
            install -Dm0644 "$file" "$target/$rel"
        done < <(find "$src" -type f -path '*/by-hash/*' -print0)
    elif [[ " $* " == *" --exclude */by-hash/* "* ]]; then
        while IFS= read -r -d '' file; do
            rel="${file#"$src"/}"
            install -Dm0644 "$file" "$target/$rel"
        done < <(find "$src" -type f ! -path '*/by-hash/*' -print0)
    else
        cp -a "$src"/. "$target"/
    fi
else
    install -Dm0644 "$src" "$target"
fi
EOF
cat > "$work/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
url=""
out=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) out="$2"; shift 2 ;;
        http://*|https://*) url="$1"; shift ;;
        *) shift ;;
    esac
done
[ -n "$url" ] && [ -n "$out" ]
path="/${url#*://*/}"
path="${path%%\?*}"
install -Dm0644 "$FAKE_R2$path" "$out"
EOF
chmod +x "$work/bin/aws" "$work/bin/curl"

export FAKE_R2="$work/fake-r2"
export FAKE_AWS_LOG="$work/aws.log"
export PATH="$work/bin:$PATH"
CLOUDLESS_APT_REPO_OUT="$repo" \
CLOUDLESS_R2_ENDPOINT=https://fake-r2.invalid \
CLOUDLESS_R2_BUCKET=test-bucket \
CLOUDLESS_APT_PUBLIC_URL=https://fake.invalid/apt \
    bash "$ROOT/distro/scripts/publish-apt-r2.sh" stable

pool_line="$(grep -n 's3 cp .*/pool/' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
artifact_line="$(grep -n 's3 cp .*/artifacts/' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
hash_line="$(grep -n -- '--include \*/by-hash/\*' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
manifest_hash_line="$(grep -n 'dists/stable/by-hash/' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
commit_line="$(grep -n 'InRelease' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
alias_line="$(grep -n 'install-dgx-spark.sh s3://' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
canonical_line="$(grep -n -- '--exclude \*/by-hash/\*' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
[ "$pool_line" -lt "$hash_line" ]
[ "$artifact_line" -lt "$commit_line" ]
[ "$hash_line" -lt "$commit_line" ]
[ "$manifest_hash_line" -lt "$commit_line" ]
[ "$commit_line" -lt "$alias_line" ]
[ "$commit_line" -lt "$canonical_line" ]

verify_visible_generation() {
    local root="$1" release="$work/visible-Release" hash size path immutable
    gpgv --keyring "$work/test-keyring.pgp" --output "$release" \
        "$root/apt/dists/stable/InRelease" >/dev/null
    if ! cmp -s "$root/apt/dists/stable/InRelease" "$repo/dists/stable/InRelease" && \
       ! cmp -s "$root/apt/dists/stable/InRelease" "$next_repo/dists/stable/InRelease"; then
        echo "Visible InRelease is neither complete generation" >&2
        return 1
    fi
    while read -r hash size path; do
        case "$path" in
            cloudless-release.json)
                immutable="$root/apt/dists/stable/by-hash/SHA256/$hash"
                ;;
            main/binary-*/Packages|main/binary-*/Packages.gz)
                immutable="$root/apt/dists/stable/$(dirname "$path")/by-hash/SHA256/$hash"
                ;;
            *) continue ;;
        esac
        test "$(wc -c < "$immutable" | tr -d '[:space:]')" = "$size"
        printf '%s  %s\n' "$hash" "$immutable" | sha256sum --check --status
    done < <(awk '$0 == "SHA256:" {inside=1; next} inside && /^ / {print $1, $2, $3; next} inside {exit}' "$release")
}

# Fail every individual object-store write once. Before the InRelease commit,
# the old signed generation must remain fully readable; after it, the new
# generation and all of its immutable dependencies must already be readable.
baseline_r2="$work/baseline-r2"
cp -a "$FAKE_R2" "$baseline_r2"
upload_count="$(wc -l < "$FAKE_AWS_LOG" | tr -d '[:space:]')"
for fail_at in $(seq 1 "$upload_count"); do
    attempt="$work/interrupted-$fail_at"
    cp -a "$baseline_r2" "$attempt"
    : > "$work/aws-interrupted.log"
    rm -f "$work/aws-count"
    if FAKE_R2="$attempt" \
       FAKE_AWS_LOG="$work/aws-interrupted.log" \
       FAKE_AWS_COUNT_FILE="$work/aws-count" \
       FAKE_AWS_FAIL_AT="$fail_at" \
       CLOUDLESS_APT_REPO_OUT="$next_repo" \
       CLOUDLESS_R2_ENDPOINT=https://fake-r2.invalid \
       CLOUDLESS_R2_BUCKET=test-bucket \
       CLOUDLESS_APT_PUBLIC_URL=https://fake.invalid/apt \
       bash "$ROOT/distro/scripts/publish-apt-r2.sh" stable >/dev/null 2>&1; then
        echo "Publisher ignored injected upload failure $fail_at" >&2
        exit 1
    fi
    verify_visible_generation "$attempt"
done

echo "Atomic APT repository and publication test passed."
