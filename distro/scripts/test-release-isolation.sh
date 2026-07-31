#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
atomic="$ROOT/distro/scripts/test-atomic-repository.sh"
builder="$ROOT/distro/scripts/build-apt-repository.sh"
release="$ROOT/distro/scripts/release.sh"
matrix="$ROOT/distro/scripts/validate-release-matrix.sh"
workflow="$ROOT/.github/workflows/multiarch.yml"

grep -Fq 'TEST_VERSION="0.0.0-test-only"' "$atomic"
grep -Fq 'CLOUDLESS_PACKAGE_OUT="$work/packages"' "$atomic"
grep -Fq 'CLOUDLESS_APT_REPO_OUT="$work/repository"' "$atomic"
grep -Fq 'CLOUDLESS_SBOM="$work/cloudless-$TEST_VERSION.spdx.json"' "$atomic"
! grep -Fq '9.9.9' "$atomic"

grep -Fq 'PACKAGE_OUT="${CLOUDLESS_PACKAGE_OUT:-$DISTRO/out/packages}"' "$builder"
grep -Fq 'CLOUDLESS_PACKAGE_OUT="$PACKAGE_OUT" "$DISTRO/scripts/build-packages.sh"' "$builder"
grep -Fq 'CLOUDLESS_SBOM_PACKAGE_DIR="$PACKAGE_OUT"' "$builder"
grep -Fq 'candidate="$PACKAGE_OUT/${package}_${VERSION}_${arch}.deb"' "$builder"
grep -Fq 'reprepro --basedir "$REPO" includedeb "$CHANNEL" "$candidate"' "$builder"
grep -Fq 'candidate="${PROMOTED_DEB[$key]}"' "$builder"
if grep -F '$DISTRO/out/packages/${package}_${VERSION}_${arch}.deb' "$builder"; then
    echo "Repository builder still bypasses the isolated package output." >&2
    exit 1
fi

grep -Fq 'TEST ONLY: atomic publication simulation' "$release"
grep -Fq 'PRODUCTION: clean rebuild and signing of CloudlessOS $VERSION' "$release"
grep -Fq 'CLOUDLESS_SKIP_PACKAGE_BUILD=1' "$release"
grep -Fq 'CLOUDLESS_SOURCE_COMMIT="$full_commit"' "$release"
grep -Fq 'release-preflight.sh' "$release"
grep -Fq 'test-secret-hygiene.sh' "$release"
grep -Fq 'test-service-hardening.sh' "$release"
grep -Fq 'test-trust-inventory.sh' "$release"
grep -Fq 'test-beta-promotion.py' "$release"
grep -Fq 'test-prepare-beta-promotion.sh' "$release"
grep -Fq 'python3 distro/scripts/test-beta-promotion.py' "$workflow"
grep -Fq 'bash distro/scripts/test-prepare-beta-promotion.sh' "$workflow"
grep -Fq 'test-sbom.sh' "$release"
grep -Fq 'test-incident-response.sh' "$release"
grep -Fq 'test-package-lifecycle.sh' "$release"
grep -Fq 'generate-sbom.py' "$release"
grep -Fq 'scan-vulnerabilities.sh' "$release"
grep -Fq 'prepare-physical-qualification.py' "$release"
grep -Fq 'CLOUDLESS_PHYSICAL_QUALIFICATION_DIR' "$release"
grep -Fq 'prepare-beta-promotion.sh' "$release"
grep -Fq 'CLOUDLESS_BETA_PROMOTION=/src/distro/out/beta-promotion' "$release"
grep -Fq 'CLOUDLESS_BETA_PROMOTION' "$ROOT/distro/scripts/sign-release-interactive.sh"
grep -Fq 'Stable releases must be promoted through distro/scripts/release.sh' "$ROOT/distro/scripts/sign-release-interactive.sh"
grep -Fq 'promotion.get("schema") == "cloudless.beta-promotion.v1"' "$release"
grep -Fq 'production beta-to-stable promotion requires the full seven-day soak' "$ROOT/distro/scripts/release-preflight.sh"
grep -Fq 'commit="${CLOUDLESS_SOURCE_COMMIT:-}"' "$matrix"
grep -Fq 'if [ -z "$commit" ]; then' "$matrix"
grep -Fq 'physicalQualification' "$matrix"
grep -Fq 'cloudless-physical-qualification.json' "$builder"
grep -Fq 'artifacts_dir="$REPO/artifacts/$VERSION/$CHANNEL"' "$builder"
grep -Fq '"artifacts/$VERSION/$CHANNEL/$artifact"' "$builder"
grep -Fq 'manifest="$REPO/releases/$VERSION/$CHANNEL.json"' "$builder"
grep -Fq 'ARTIFACTS="$REPO/artifacts/$VERSION/$CHANNEL"' "$ROOT/distro/scripts/publish-apt-r2.sh"
grep -Fq 'manifest["promotion"] = promotion' "$builder"
if grep -Fq 'artifacts/$VERSION/$artifact' "$builder" || \
   grep -Fq '$REPO/artifacts/$VERSION/cloudless-' "$ROOT/distro/scripts/publish-apt-r2.sh"; then
    echo "Release scripts still use a channel-ambiguous artifact path." >&2
    exit 1
fi

echo "Release test isolation checks passed."
