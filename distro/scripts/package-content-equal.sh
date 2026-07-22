#!/usr/bin/env bash
# Compare the installed contents of two Debian packages while ignoring their
# release version and non-semantic PNG metadata added by image tooling.
set -euo pipefail

OLD_DEB="${1:-}"
NEW_DEB="${2:-}"
if [ ! -s "$OLD_DEB" ] || [ ! -s "$NEW_DEB" ]; then
    echo "Usage: $0 OLD.deb NEW.deb" >&2
    exit 2
fi
for command in dpkg-deb diff mogrify; do
    command -v "$command" >/dev/null || { echo "Missing command: $command" >&2; exit 2; }
done

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
dpkg-deb --raw-extract "$OLD_DEB" "$work/old"
dpkg-deb --raw-extract "$NEW_DEB" "$work/new"
sed -i '/^Version:/d' "$work/old/DEBIAN/control" "$work/new/DEBIAN/control"

while IFS= read -r -d '' image; do mogrify -strip "$image"; done \
    < <(find "$work/old" "$work/new" -type f -iname '*.png' -print0)

diff --no-dereference --brief --recursive "$work/old" "$work/new" >/dev/null
