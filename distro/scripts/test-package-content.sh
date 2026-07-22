#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

make_package() {
    local root="$1" version="$2" comment="$3"
    mkdir -p "$root/DEBIAN" "$root/usr/share/cloudless"
    cat > "$root/DEBIAN/control" <<EOF
Package: cloudless-test
Version: $version
Architecture: amd64
Maintainer: Cloudless <hello@becloudless.ai>
Description: Selective release comparison test
EOF
    printf 'same payload\n' > "$root/usr/share/cloudless/value.txt"
    convert -size 1x1 xc:red -set comment "$comment" "$root/usr/share/cloudless/pixel.png"
}

make_package "$work/old" 1.0 old-metadata
make_package "$work/new" 2.0 new-metadata
dpkg-deb --root-owner-group --build "$work/old" "$work/old.deb" >/dev/null
dpkg-deb --root-owner-group --build "$work/new" "$work/new.deb" >/dev/null
bash "$ROOT/distro/scripts/package-content-equal.sh" "$work/old.deb" "$work/new.deb"

printf 'changed payload\n' > "$work/new/usr/share/cloudless/value.txt"
dpkg-deb --root-owner-group --build "$work/new" "$work/changed.deb" >/dev/null
if bash "$ROOT/distro/scripts/package-content-equal.sh" "$work/old.deb" "$work/changed.deb"; then
    echo "Changed package payload was incorrectly treated as unchanged" >&2
    exit 1
fi
echo "Selective package-content comparison passed"
