#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPO="${CLOUDLESS_APT_REPO_OUT:-$ROOT/distro/out/apt-repository}"
CHANNEL="${1:-stable}"
PUBLIC_BASE="${CLOUDLESS_APT_PUBLIC_URL:-https://updates.becloudless.ai/apt}"
PUBLIC_ROOT="${CLOUDLESS_PUBLIC_ROOT_URL:-${PUBLIC_BASE%/apt}}"
APP_MANIFEST="${CLOUDLESS_APP_MANIFEST:-$ROOT/distro/release/manifests/cloudless-apps-manifest.json}"
APP_MANIFEST_PUBLIC="${CLOUDLESS_APP_MANIFEST_PUBLIC_URL:-$PUBLIC_ROOT/manifests/cloudless-apps-manifest.json}"
DGX_INSTALLER="${CLOUDLESS_DGX_INSTALLER:-$ROOT/distro/scripts/install-dgx-spark.sh}"
DGX_INSTALLER_PUBLIC="${CLOUDLESS_DGX_INSTALLER_PUBLIC_URL:-$PUBLIC_ROOT/install-dgx-spark.sh}"
: "${CLOUDLESS_R2_ENDPOINT:?Set CLOUDLESS_R2_ENDPOINT}"
: "${CLOUDLESS_R2_BUCKET:?Set CLOUDLESS_R2_BUCKET}"
case "$CHANNEL" in stable|beta) ;; *) echo "Channel must be stable or beta" >&2; exit 2 ;; esac
for command in aws awk cmp curl gpgv python3 sha256sum; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done

DIST="$REPO/dists/$CHANNEL"
KEY="$REPO/cloudless-archive-keyring.pgp"
DEST="s3://$CLOUDLESS_R2_BUCKET/apt"
test -s "$DIST/InRelease" -a -s "$DIST/Release" -a -s "$KEY" || {
    echo "No complete signed $CHANNEL repository found at $REPO" >&2
    exit 1
}
python3 -m json.tool "$APP_MANIFEST" >/dev/null
test -s "$DGX_INSTALLER" || { echo "Missing DGX installer: $DGX_INSTALLER" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

release_entries() {
    awk '$0 == "SHA256:" {inside=1; next} inside && /^ / {print $1, $2, $3; next} inside {exit}' "$1"
}

verify_local() {
    local extracted="$work/local-release" hash size path file by_hash filename checksum arch
    gpgv --keyring "$KEY" --output "$extracted" "$DIST/InRelease" >/dev/null 2>&1
    cmp -s "$extracted" "$DIST/Release" || { echo "InRelease does not sign the current Release file" >&2; return 1; }
    grep -Fqx 'Acquire-By-Hash: yes' "$DIST/Release" || { echo "Release does not enable Acquire-By-Hash" >&2; return 1; }
    while read -r hash size path; do
        case "$path" in main/binary-*/Packages|main/binary-*/Packages.gz|cloudless-release.json) ;; *) continue ;; esac
        file="$DIST/$path"
        test "$(wc -c < "$file" | tr -d '[:space:]')" = "$size"
        printf '%s  %s\n' "$hash" "$file" | sha256sum --check --status
        if [ "$path" != cloudless-release.json ]; then
            by_hash="$(dirname "$file")/by-hash/SHA256/$hash"
            cmp -s "$file" "$by_hash" || { echo "Missing immutable index $by_hash" >&2; return 1; }
        fi
    done < <(release_entries "$DIST/Release")
    for arch in $(awk '/^Architectures:/ {for (i=2; i<=NF; i++) print $i}' "$DIST/Release"); do
        test -s "$DIST/main/binary-$arch/Packages" || { echo "Missing $arch package index" >&2; return 1; }
        while read -r checksum filename; do
            test -s "$REPO/$filename" || { echo "Missing package $filename" >&2; return 1; }
            printf '%s  %s\n' "$checksum" "$REPO/$filename" | sha256sum --check --status
        done < <(awk 'BEGIN {RS=""} {filename=""; checksum=""; for (i=1; i<=NF; i++) {if ($i == "Filename:") filename=$(i+1); if ($i == "SHA256:") checksum=$(i+1)} if (filename != "" && checksum != "") print checksum, filename}' "$DIST/main/binary-$arch/Packages")
    done
}

release_version() {
    python3 - "$DIST/cloudless-release.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    print(json.load(handle)["version"])
PY
}

verify_artifact_set() {
    local manifest="$1" base="$2"
    python3 - "$manifest" "$base" <<'PY'
import hashlib, json, pathlib, sys
manifest_path, base_path = sys.argv[1:]
base = pathlib.Path(base_path)
with open(manifest_path, encoding="utf-8") as handle:
    release = json.load(handle)
artifacts = release.get("artifacts")
if not isinstance(artifacts, list) or {item.get("name") for item in artifacts} != {
    "install-dgx-spark.sh", "cloudless-apps-manifest.json"
}:
    raise SystemExit("Signed release does not describe the complete standalone artifact set")
for item in artifacts:
    for field in ("path", "sha256", "size", "signature", "signatureSha256", "signatureSize"):
        if field not in item:
            raise SystemExit(f"Artifact {item.get('name')} is missing {field}")
    for path_field, hash_field, size_field in (
        ("path", "sha256", "size"),
        ("signature", "signatureSha256", "signatureSize"),
    ):
        path = base / item[path_field]
        data = path.read_bytes()
        if len(data) != item[size_field] or hashlib.sha256(data).hexdigest() != item[hash_field]:
            raise SystemExit(f"Artifact integrity mismatch: {item[path_field]}")
PY
    local version artifact
    version="$(release_version)"
    for artifact in install-dgx-spark.sh cloudless-apps-manifest.json; do
        gpgv --keyring "$KEY" \
            "$REPO/artifacts/$version/$artifact.asc" \
            "$REPO/artifacts/$version/$artifact" >/dev/null 2>&1 || {
            echo "Invalid detached signature for $artifact" >&2
            return 1
        }
    done
}

verify_public() {
    local cache_bust live_inrelease="$work/live-InRelease" live_release="$work/live-Release"
    local hash size path target filename checksum attempt arch
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
        case "$path" in main/binary-*/Packages|main/binary-*/Packages.gz|cloudless-release.json) ;; *) continue ;; esac
        target="$work/$(basename "$path")-$hash"
        if [ "$path" = cloudless-release.json ]; then
            curl -fsS "$PUBLIC_BASE/dists/$CHANNEL/cloudless-release.json?v=$cache_bust" -o "$target"
        else
            curl -fsS "$PUBLIC_BASE/dists/$CHANNEL/$(dirname "$path")/by-hash/SHA256/$hash?v=$cache_bust" -o "$target"
        fi
        test "$(wc -c < "$target" | tr -d '[:space:]')" = "$size"
        printf '%s  %s\n' "$hash" "$target" | sha256sum --check --status
    done < <(release_entries "$live_release")
    for arch in $(awk '/^Architectures:/ {for (i=2; i<=NF; i++) print $i}' "$live_release"); do
        curl -fsS "$PUBLIC_BASE/dists/$CHANNEL/main/binary-$arch/Packages?v=$cache_bust" -o "$work/Packages-$arch"
        while read -r checksum filename; do
            target="$work/$arch-$(basename "$filename")"
            curl -fsS "$PUBLIC_BASE/$filename?v=$cache_bust" -o "$target"
            printf '%s  %s\n' "$checksum" "$target" | sha256sum --check --status
        done < <(awk 'BEGIN {RS=""} {filename=""; checksum=""; for (i=1; i<=NF; i++) {if ($i == "Filename:") filename=$(i+1); if ($i == "SHA256:") checksum=$(i+1)} if (filename != "" && checksum != "") print checksum, filename}' "$work/Packages-$arch")
    done
}

verify_public_artifacts() {
    local path hash size signature signature_hash signature_size target signature_target cache_bust
    while read -r path hash size signature signature_hash signature_size; do
        target="$work/public-root/$path"
        signature_target="$work/public-root/$signature"
        install -d "$(dirname "$target")" "$(dirname "$signature_target")"
        cache_bust="$hash"
        curl -fsS "$PUBLIC_ROOT/$path?v=$cache_bust" -o "$target"
        curl -fsS "$PUBLIC_ROOT/$signature?v=$signature_hash" -o "$signature_target"
        test "$(wc -c < "$target" | tr -d '[:space:]')" = "$size"
        test "$(wc -c < "$signature_target" | tr -d '[:space:]')" = "$signature_size"
        printf '%s  %s\n' "$hash" "$target" | sha256sum --check --status
        printf '%s  %s\n' "$signature_hash" "$signature_target" | sha256sum --check --status
        gpgv --keyring "$KEY" "$signature_target" "$target" >/dev/null
    done < <(python3 - "$DIST/cloudless-release.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    release = json.load(handle)
for item in release["artifacts"]:
    print(item["path"], item["sha256"], item["size"], item["signature"], item["signatureSha256"], item["signatureSize"])
PY
)
}

verify_local
verify_artifact_set "$DIST/cloudless-release.json" "$REPO"
VERSION="$(release_version)"
cmp -s "$APP_MANIFEST" "$REPO/artifacts/$VERSION/cloudless-apps-manifest.json" || {
    echo "The application manifest changed after release signing; sign again." >&2
    exit 1
}
cmp -s "$DGX_INSTALLER" "$REPO/artifacts/$VERSION/install-dgx-spark.sh" || {
    echo "The DGX installer changed after release signing; sign again." >&2
    exit 1
}
if [ "${CLOUDLESS_PUBLISH_VERIFY_ONLY:-0}" = "1" ]; then
    echo "Locally verified signed release $VERSION ($CHANNEL)."
    exit 0
fi

# Promotion is intentionally non-destructive. Old packages and by-hash indexes
# remain available to clients that began an update before this release.
aws s3 cp "$REPO/pool/" "$DEST/pool/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$REPO/releases/" "$DEST/releases/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$REPO/artifacts/" "s3://$CLOUDLESS_R2_BUCKET/artifacts/" --recursive \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$KEY" "$DEST/cloudless-archive-keyring.pgp" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$DIST/main/" "$DEST/dists/$CHANNEL/main/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --exclude '*' --include '*/by-hash/*' --cache-control 'public,max-age=31536000,immutable' --only-show-errors
aws s3 cp "$DIST/cloudless-release.json" "$DEST/dists/$CHANNEL/cloudless-release.json" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
aws s3 cp "$DIST/Release.gpg" "$DEST/dists/$CHANNEL/Release.gpg" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
aws s3 cp "$DIST/Release" "$DEST/dists/$CHANNEL/Release" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
# InRelease is the commit point. APT cannot observe the new generation until
# every immutable object referenced by this signed file is already present.
aws s3 cp "$DIST/InRelease" "$DEST/dists/$CHANNEL/InRelease" --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
# Mutable convenience aliases are promoted only after signed APT metadata has
# committed the immutable, versioned artifact hashes.
aws s3 cp "$REPO/artifacts/$VERSION/cloudless-apps-manifest.json" \
    "s3://$CLOUDLESS_R2_BUCKET/manifests/cloudless-apps-manifest.json" \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
aws s3 cp "$REPO/artifacts/$VERSION/cloudless-apps-manifest.json.asc" \
    "s3://$CLOUDLESS_R2_BUCKET/manifests/cloudless-apps-manifest.json.asc" \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
aws s3 cp "$REPO/artifacts/$VERSION/install-dgx-spark.sh" \
    "s3://$CLOUDLESS_R2_BUCKET/install-dgx-spark.sh" \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
aws s3 cp "$REPO/artifacts/$VERSION/install-dgx-spark.sh.asc" \
    "s3://$CLOUDLESS_R2_BUCKET/install-dgx-spark.sh.asc" \
    --endpoint-url "$CLOUDLESS_R2_ENDPOINT" --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors
# Supported CloudlessOS clients now follow by-hash immediately. Updating the
# legacy mutable aliases after the commit keeps clients on the previous
# generation safe throughout the first migration to by-hash.
aws s3 cp "$DIST/main/" "$DEST/dists/$CHANNEL/main/" --recursive --endpoint-url "$CLOUDLESS_R2_ENDPOINT" \
    --exclude '*/by-hash/*' --cache-control 'no-store,max-age=0,must-revalidate' --only-show-errors

verify_public
verify_public_artifacts
curl -fsS "$APP_MANIFEST_PUBLIC?v=$(sha256sum "$APP_MANIFEST" | awk '{print $1}')" -o "$work/public-app-manifest"
curl -fsS "$APP_MANIFEST_PUBLIC.asc?v=$(sha256sum "$REPO/artifacts/$VERSION/cloudless-apps-manifest.json.asc" | awk '{print $1}')" \
    -o "$work/public-app-manifest.asc"
cmp -s "$REPO/artifacts/$VERSION/cloudless-apps-manifest.json" "$work/public-app-manifest" || {
    echo "Public app manifest does not match the published source" >&2
    exit 1
}
gpgv --keyring "$KEY" "$work/public-app-manifest.asc" "$work/public-app-manifest" >/dev/null
curl -fsS "$DGX_INSTALLER_PUBLIC?v=$(sha256sum "$DGX_INSTALLER" | awk '{print $1}')" -o "$work/public-dgx-installer"
curl -fsS "$DGX_INSTALLER_PUBLIC.asc?v=$(sha256sum "$REPO/artifacts/$VERSION/install-dgx-spark.sh.asc" | awk '{print $1}')" \
    -o "$work/public-dgx-installer.asc"
cmp -s "$REPO/artifacts/$VERSION/install-dgx-spark.sh" "$work/public-dgx-installer" || {
    echo "Public DGX installer does not match the published source" >&2
    exit 1
}
gpgv --keyring "$KEY" "$work/public-dgx-installer.asc" "$work/public-dgx-installer" >/dev/null
echo "Published and publicly verified at $PUBLIC_BASE"
