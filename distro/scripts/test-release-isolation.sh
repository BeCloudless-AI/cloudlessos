#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
atomic="$ROOT/distro/scripts/test-atomic-repository.sh"
builder="$ROOT/distro/scripts/build-apt-repository.sh"
release="$ROOT/distro/scripts/release.sh"
matrix="$ROOT/distro/scripts/validate-release-matrix.sh"
runner="$ROOT/distro/scripts/run-local-qualification.sh"

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
! grep -Fq 'SignWith:' "$builder"
! grep -Fq 'ask-passphrase' "$builder"
grep -Fq 'gpg_sign --clearsign' "$builder"
grep -Fq 'CLOUDLESS_GPG_PASSPHRASE_FILE=/run/secrets/cloudless-archive-passphrase' "$ROOT/distro/scripts/sign-release-interactive.sh"
grep -Fq 'candidate="${PROMOTED_DEB[$key]}"' "$builder"
grep -Fq -- "-w '%{http_code}'" "$builder"
grep -Fq 'return 3' "$builder"
grep -Fq 'refusing to publish' "$builder"
grep -Fq 'python3 -m http.server' "$atomic"
if grep -F '$DISTRO/out/packages/${package}_${VERSION}_${arch}.deb' "$builder"; then
    echo "Repository builder still bypasses the isolated package output." >&2
    exit 1
fi

grep -Fq 'TEST ONLY: atomic publication simulation' "$release"
grep -Fq 'PRODUCTION: clean rebuild and signing of CloudlessOS $VERSION' "$release"
grep -Fq 'CLOUDLESS_SKIP_PACKAGE_BUILD=1' "$runner"
grep -Fq 'CLOUDLESS_SOURCE_COMMIT="$full_commit"' "$release"
grep -Fq 'release-preflight.sh' "$release"
grep -Fq 'test-beta-promotion.py' "$runner"
grep -Fq 'test-security-readiness.py' "$runner"
grep -Fq 'test-ci-qualification.py' "$runner"
grep -Fq 'test-configure-release-env.sh' "$runner"
grep -Fq 'test-prepare-beta-promotion.sh' "$runner"
grep -Fq 'test-secret-hygiene.sh' "$runner"
grep -Fq 'test-service-hardening.sh' "$runner"
grep -Fq 'test-trust-inventory.sh' "$runner"
grep -Fq 'test-sbom.sh' "$runner"
grep -Fq 'test-incident-response.sh' "$runner"
grep -Fq 'test-package-lifecycle.sh' "$runner"
grep -Fq 'begin_interrupted_recipe_check' "$ROOT/distro/scripts/test-package-lifecycle.sh"
grep -Fq 'assert_durable_recipe_check' "$ROOT/distro/scripts/test-package-lifecycle.sh"
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
grep -Fq 'cloudless-security-readiness.json' "$builder"
grep -Fq 'cloudless-ci-qualification.json' "$builder"
grep -Fq 'prepare-security-readiness.py' "$release"
grep -Fq 'run-local-qualification.sh' "$release"
grep -Fq 'prepare-ci-qualification.py' "$runner"
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
