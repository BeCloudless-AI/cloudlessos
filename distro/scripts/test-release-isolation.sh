#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
atomic="$ROOT/distro/scripts/test-atomic-repository.sh"
builder="$ROOT/distro/scripts/build-apt-repository.sh"
release="$ROOT/distro/scripts/release.sh"
matrix="$ROOT/distro/scripts/validate-release-matrix.sh"

grep -Fq 'TEST_VERSION="0.0.0-test-only"' "$atomic"
grep -Fq 'CLOUDLESS_PACKAGE_OUT="$work/packages"' "$atomic"
grep -Fq 'CLOUDLESS_APT_REPO_OUT="$work/repository"' "$atomic"
! grep -Fq '9.9.9' "$atomic"

grep -Fq 'PACKAGE_OUT="${CLOUDLESS_PACKAGE_OUT:-$DISTRO/out/packages}"' "$builder"
grep -Fq 'CLOUDLESS_PACKAGE_OUT="$PACKAGE_OUT" "$DISTRO/scripts/build-packages.sh"' "$builder"
grep -Fq 'candidate="$PACKAGE_OUT/${package}_${VERSION}_${arch}.deb"' "$builder"
grep -Fq 'includedeb "$CHANNEL" "$PACKAGE_OUT/${package}_${VERSION}_${arch}.deb"' "$builder"
if grep -F '$DISTRO/out/packages/${package}_${VERSION}_${arch}.deb' "$builder"; then
    echo "Repository builder still bypasses the isolated package output." >&2
    exit 1
fi

grep -Fq 'TEST ONLY: atomic publication simulation' "$release"
grep -Fq 'PRODUCTION: clean rebuild and signing of CloudlessOS $VERSION' "$release"
grep -Fq 'CLOUDLESS_SKIP_PACKAGE_BUILD=1' "$release"
grep -Fq 'CLOUDLESS_SOURCE_COMMIT="$full_commit"' "$release"
grep -Fq 'commit="${CLOUDLESS_SOURCE_COMMIT:-}"' "$matrix"
grep -Fq 'if [ -z "$commit" ]; then' "$matrix"

echo "Release test isolation checks passed."
