#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPO="${CLOUDLESS_APT_REPO_OUT:-$ROOT/distro/out/apt-repository}"
CHANNEL="${1:-stable}"
PUBLIC_BASE="${CLOUDLESS_APT_PUBLIC_URL:-https://updates.becloudless.ai/apt}"
: "${CLOUDLESS_R2_ENDPOINT:?Set CLOUDLESS_R2_ENDPOINT}"
: "${CLOUDLESS_R2_BUCKET:?Set CLOUDLESS_R2_BUCKET}"
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
for command in aws awk cmp curl gpgv sha256sum; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done

DIST="$REPO/dists/$CHANNEL"
KEY="$REPO/cloudless-archive-keyring.pgp"
DEST="s3://$CLOUDLESS_R2_BUCKET/apt"
test -s "$DIST/InRelease" -a -s "$DIST/Release" -a -s "$KEY" || {
    echo "No complete signed $CHANNEL repository found at $REPO" >&2
    exit 1
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

release_entries() {
    awk '$0 == "SHA256:" {inside=1; next} inside && /^ / {print $1, $2, $3; next} inside {exit}' "$1"
}

verify_local() {
    local extracted="$work/local-release" hash size path file by_hash filename checksum
    gpgv --keyring "$KEY" --output "$extracted" "$DIST/InRelease" >/dev/null 2>&1
    cmp -s "$extracted" "$DIST/Release" || { echo "InRelease does not sign the current Release file" >&2; return 1; }
    grep -Fqx 'Acquire-By-Hash: yes' "$DIST/Release" || { echo "Release does not enable Acquire-By-Hash" >&2; return 1; }
    while read -r hash size path; do
        case "$path" in main/binary-amd64/Packages|main/binary-amd64/Packages.gz) ;; *) continue ;; esac
        file="$DIST/$path"
        by_hash="$(dirname "$file")/by-hash/SHA256/$hash"
        test "$(wc -c < "$file" | tr -d '[:space:]')" = "$size"
        printf '%s  %s\n' "$hash" "$file" | sha256sum --check --status
        cmp -s "$file" "$by_hash" || { echo "Missing immutable index $by_hash" >&2; return 1; }
    done < <(release_entries "$DIST/Release")
    while read -r checksum filename; do
        test -s "$REPO/$filename" || { echo "Missing package $filename" >&2; return 1; }
        printf '%s  %s\n' "$checksum" "$REPO/$filename" | sha256sum --check --status
    done < <(awk 'BEGIN {RS=""} {filename=""; checksum=""; for (i=1; i<=NF; i++) {if ($i == "Filename:") filename=$(i+1); if ($i == "SHA256:") checksum=$(i+1)} if (filename != "" && checksum != "") print checksum, filename}' "$DIST/main/binary-amd64/Packages")
}

verify_public() {
    local cache_bust live_inrelease="$work/live-InRelease" live_release="$work/live-Release"
    local hash size path target filename checksum attempt
    cache_bust="$(sha256sum "$DIST/InRelease" | awk '{print $1}')"
    for attempt in {1..15}; do
        if curl -fsS "$PUBLIC_BASE/dists/$CHANNEL/InRelease?v=$cache_bust" -o "$live_inrelease" && \
            cmp -s "$live_inrelease" "$DIST/InRelease"; then
            break
        fi
        if [ "$attempt" -eq 15 ]; then
            echo "Public InRelease did not converge to the promoted release" >&2
            return 1
        fi
        sleep 2
    done
    gpgv --keyring "$KEY" --output "$live_release" "$live_inrelease" >/dev/null 2>&1
    while read -r hash size path; do
        case "$path" in main/binary-amd64/Packages|main/binary-amd64/Packages.gz) ;; *) continue ;; esac
        target="$work/$(basename "$path")-$hash"
        curl -fsS "$PUBLIC_BASE/dists/$CHANNEL/$(dirname "$path")/by-hash/SHA256/$hash?v=$cache_bust" -o "$target"
        test "$(wc -c < "$target" | tr -d '[:space:]')" = "$size"
        printf '%s  %s\n' "$hash" "$target" | sha256sum --check --status
    done < <(release_entries "$live_release")
    curl -fsS "$PUBLIC_BASE/dists/$CHANNEL/main/binary-amd64/Packages?v=$cache_bust" -o "$work/Packages"
    while read -r checksum filename; do
        target="$work/$(basename "$filename")"
        curl -fsS "$PUBLIC_BASE/$filename?v=$cache_bust" -o "$target"
        printf '%s  %s\n' "$checksum" "$target" | sha256sum --check --status
    done < <(awk 'BEGIN {RS=""} {filename=""; checksum=""; for (i=1; i<=NF; i++) {if ($i == "Filename:") filename=$(i+1); if ($i == "SHA256:") checksum=$(i+1)} if (filename != "" && checksum != "") print checksum, filename}' "$work/Packages")
}

verify_local

# Promotion is intentionally non-destructive. Old packages and by-hash indexes
# remain available to clients that began an update before this release.
aws s3 cp "$REPO/pool/" "$DEST/pool/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$REPO/releases/" "$DEST/releases/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$KEY" "$DEST/cloudless-archive-keyring.pgp" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$DIST/main/" "$DEST/dists/$CHANNEL/main/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --exclude '*' --include '*/by-hash/*' --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$DIST/Release.gpg" "$DEST/dists/$CHANNEL/Release.gpg" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
aws s3 cp "$DIST/Release" "$DEST/dists/$CHANNEL/Release" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
# InRelease is the commit point. APT cannot observe the new generation until
# every immutable object referenced by this signed file is already present.
aws s3 cp "$DIST/InRelease" "$DEST/dists/$CHANNEL/InRelease" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
# Supported CloudlessOS clients now follow by-hash immediately. Updating the
# legacy mutable aliases after the commit keeps clients on the previous
# generation safe throughout the first migration to by-hash.
aws s3 cp "$DIST/main/" "$DEST/dists/$CHANNEL/main/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --exclude '*/by-hash/*' --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors

verify_public
echo "Published and publicly verified at $PUBLIC_BASE"
