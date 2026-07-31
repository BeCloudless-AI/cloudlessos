#!/usr/bin/env bash
# Download and verify the exact public beta generation selected for stable promotion.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${1:?version is required}"
SOURCE_COMMIT="${2:?full source commit is required}"
OUT="${3:-$ROOT/distro/out/beta-promotion}"
PUBLIC_BASE="${CLOUDLESS_APT_PUBLIC_URL:-https://updates.becloudless.ai/apt}"
PUBLIC_ROOT="${CLOUDLESS_PUBLIC_ROOT_URL:-${PUBLIC_BASE%/apt}}"
KEY="${CLOUDLESS_ARCHIVE_KEY:-$ROOT/distro/release/keys/cloudless-archive-keyring.pgp}"
MINIMUM_AGE="${CLOUDLESS_PROMOTION_MIN_AGE_SECONDS:-604800}"

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+~][0-9A-Za-z][0-9A-Za-z.+~_-]*)?$ ]] || {
    echo "Invalid promotion version: $VERSION" >&2
    exit 2
}
[[ "$SOURCE_COMMIT" =~ ^[0-9a-f]{40}$ ]] || { echo "A full source commit is required." >&2; exit 2; }
[[ "$MINIMUM_AGE" =~ ^[0-9]+$ ]] || { echo "Promotion minimum age must be seconds." >&2; exit 2; }
for command in curl gpgv python3 sha256sum; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
test -s "$KEY" || { echo "Missing CloudlessOS archive keyring: $KEY" >&2; exit 1; }
case "$OUT" in ""|/) echo "Refusing unsafe promotion output path: $OUT" >&2; exit 2 ;; esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
curl -fsS "$PUBLIC_BASE/dists/beta/InRelease" -o "$work/InRelease"
gpgv --keyring "$KEY" --output "$work/Release" "$work/InRelease" >/dev/null
read -r manifest_hash manifest_size < <(
    awk '$0 == "SHA256:" {inside=1; next} inside && $3 == "cloudless-release.json" {print $1, $2; exit}' "$work/Release"
)
[ -n "${manifest_hash:-}" ] && [ -n "${manifest_size:-}" ] || {
    echo "Signed beta metadata does not reference cloudless-release.json." >&2
    exit 1
}
curl -fsS -D "$work/manifest.headers" \
    "$PUBLIC_BASE/dists/beta/by-hash/SHA256/$manifest_hash" -o "$work/cloudless-release.json"
test "$(wc -c < "$work/cloudless-release.json" | tr -d '[:space:]')" = "$manifest_size"
printf '%s  %s\n' "$manifest_hash" "$work/cloudless-release.json" | sha256sum --check --status
available_at="$(python3 - "$work/manifest.headers" <<'PY'
import email.utils, pathlib, sys
values = []
for line in pathlib.Path(sys.argv[1]).read_text(encoding="iso-8859-1").splitlines():
    if line.lower().startswith("last-modified:"):
        values.append(line.split(":", 1)[1].strip())
if not values:
    raise SystemExit("Public beta object has no Last-Modified timestamp")
parsed = email.utils.parsedate_to_datetime(values[-1])
if parsed.tzinfo is None:
    raise SystemExit("Public beta Last-Modified timestamp has no timezone")
print(parsed.isoformat().replace("+00:00", "Z"))
PY
)"

rm -rf -- "$OUT"
install -d "$OUT/packages" "$OUT/artifacts"
install -m 0644 "$work/cloudless-release.json" "$OUT/cloudless-release.json"
python3 - "$OUT/cloudless-release.json" "$OUT/downloads.tsv" <<'PY'
import json, pathlib, sys
manifest, output = sys.argv[1:]
release = json.loads(pathlib.Path(manifest).read_text(encoding="utf-8"))
with open(output, "w", encoding="utf-8") as handle:
    for item in release.get("packages", []):
        handle.write(f"package\t{item.get('filename', '')}\t{item.get('sha256', '')}\t{item.get('size', '')}\n")
    for item in release.get("artifacts", []):
        handle.write(f"artifact\t{item.get('path', '')}\t{item.get('sha256', '')}\t{item.get('size', '')}\n")
        handle.write(f"signature\t{item.get('signature', '')}\t{item.get('signatureSha256', '')}\t{item.get('signatureSize', '')}\n")
PY

while IFS=$'\t' read -r kind path hash size; do
    case "$path" in
        pool/*) base="$PUBLIC_BASE"; target="$OUT/packages/$(basename "$path")" ;;
        artifacts/*) base="$PUBLIC_ROOT"; target="$OUT/artifacts/$(basename "$path")" ;;
        *) echo "Unsafe beta payload path: $path" >&2; exit 1 ;;
    esac
    curl -fsS "$base/$path?v=$hash" -o "$target"
    test "$(wc -c < "$target" | tr -d '[:space:]')" = "$size"
    printf '%s  %s\n' "$hash" "$target" | sha256sum --check --status
    if [ "$kind" = signature ]; then
        payload="${target%.asc}"
        test -s "$payload" || { echo "Detached signature arrived before payload: $path" >&2; exit 1; }
        gpgv --keyring "$KEY" "$target" "$payload" >/dev/null
    fi
done < "$OUT/downloads.tsv"
rm -f "$OUT/downloads.tsv"

python3 "$ROOT/distro/scripts/verify-beta-promotion.py" \
    "$OUT/cloudless-release.json" "$OUT/packages" "$OUT/artifacts" \
    "$VERSION" "$SOURCE_COMMIT" --minimum-age-seconds "$MINIMUM_AGE" \
    --publicly-available-at "$available_at" \
    --output "$OUT/cloudless-beta-promotion.json"
echo "Verified beta promotion snapshot: $OUT"
