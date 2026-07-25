#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="/usr/local/go/bin:$PATH"
export GNUPGHOME="$(mktemp -d)"
work="$(mktemp -d)"
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

CLOUDLESS_APT_REPO_OUT="$work/repository" \
CLOUDLESS_APT_BASE_URL=https://invalid.invalid \
CLOUDLESS_ARCHIVE_KEY="$work/test-keyring.pgp" \
CLOUDLESS_ARCHIVE_FINGERPRINT_FILE="$work/test-fingerprint.txt" \
CLOUDLESS_RELEASE_NOTES="$work/test-notes.json" \
    bash "$ROOT/distro/scripts/build-apt-repository.sh" 9.9.9 stable

repo="$work/repository"
gpgv --keyring "$work/test-keyring.pgp" "$repo/dists/stable/InRelease"
grep -Fqx 'Acquire-By-Hash: yes' "$repo/dists/stable/Release"
grep -Fq ' cloudless-release.json' "$repo/dists/stable/Release"
cmp "$repo/releases/9.9.9.json" "$repo/dists/stable/cloudless-release.json"
grep -Fq '"architecture":"amd64"' "$repo/releases/9.9.9.json"
grep -Fq '"architecture":"arm64"' "$repo/releases/9.9.9.json"
for arch in amd64 arm64; do
    for index in \
        "$repo/dists/stable/main/binary-$arch/Packages" \
        "$repo/dists/stable/main/binary-$arch/Packages.gz"; do
        hash="$(sha256sum "$index" | awk '{print $1}')"
        cmp "$index" "$(dirname "$index")/by-hash/SHA256/$hash"
    done
done

mkdir -p "$work/bin" "$work/fake-r2"
cat > "$work/bin/aws" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_AWS_LOG"
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
hash_line="$(grep -n -- '--include \*/by-hash/\*' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
commit_line="$(grep -n 'InRelease' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
canonical_line="$(grep -n -- '--exclude \*/by-hash/\*' "$FAKE_AWS_LOG" | head -1 | cut -d: -f1)"
[ "$pool_line" -lt "$hash_line" ]
[ "$hash_line" -lt "$commit_line" ]
[ "$commit_line" -lt "$canonical_line" ]

echo "Atomic APT repository and publication test passed."
