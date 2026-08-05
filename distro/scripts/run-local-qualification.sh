#!/usr/bin/env bash
# Run and retain the exact-commit qualification matrix without hosted CI.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT/distro/scripts/release-version.sh"
VERSION="${1:-}"
CHANNEL="${2:-}"
COMMIT="${3:-}"
OUTPUT="${4:-$ROOT/distro/out/qualification/cloudless-ci-qualification.json}"

usage() {
    echo "Usage: $0 VERSION stable|beta FULL_COMMIT [OUTPUT]" >&2
}
cloudless_is_release_version "$VERSION" || { usage; exit 2; }
case "$CHANNEL" in stable|beta) ;; *) usage; exit 2 ;; esac
[[ "$COMMIT" =~ ^[0-9a-f]{40}$ ]] || { usage; exit 2; }

for command in docker git python3 sha256sum tar tee; do
    command -v "$command" >/dev/null || { echo "Missing required command: $command" >&2; exit 1; }
done
[ "$(git -C "$ROOT" rev-parse HEAD)" = "$COMMIT" ] || {
    echo "Local qualification commit does not match the checked-out source." >&2
    exit 1
}
git -C "$ROOT" diff --quiet && git -C "$ROOT" diff --cached --quiet || {
    echo "Local qualification requires a clean tracked worktree." >&2
    exit 1
}

run_id="$(date -u +%s%N)"
attempt=1
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
runs_root="$ROOT/distro/out/qualification/local-runs"
work="$runs_root/.incomplete-$run_id"
final="$runs_root/$run_id"
rm -rf "$work"
mkdir -p "$work" "$work/visual" "$work/soak" "$work/package"
chmod 0700 "$work"

job() {
    local slug="$1"
    local label="$2"
    shift 2
    echo "==> Local qualification: $label"
    (
        set -o pipefail
        "$@" 2>&1 | tee "$work/$slug.log"
    )
}

source_job() {
    docker run --rm -v "$ROOT:/src" -w /src/orchestrator \
        cloudless-release-builder go test -race -count=1 ./...
    docker run --rm -v "$ROOT:/src" -w /src/orchestrator \
        cloudless-release-builder go vet ./...
    for architecture in amd64 arm64; do
        docker run --rm -e GOOS=linux -e GOARCH="$architecture" -e CGO_ENABLED=0 \
            -v "$ROOT:/src" -w /src/orchestrator cloudless-release-builder \
            go build -buildvcs=false ./cmd/cloudlessd ./cmd/cloudless-updater
    done
    docker run --rm -v "$ROOT:/src" -w /src \
        node:22-bookworm node distro/scripts/test-web-js.js
    bash "$ROOT/distro/scripts/test-preinstall.sh"
    bash "$ROOT/distro/scripts/test-configure-release-env.sh"
    bash "$ROOT/distro/scripts/test-release-env.sh"
    bash "$ROOT/distro/scripts/test-release-container-platforms.sh"
    bash "$ROOT/distro/scripts/test-release-preflight.sh"
    bash "$ROOT/distro/scripts/test-release-isolation.sh"
    bash "$ROOT/distro/scripts/test-protected-signing.sh"
    python3 "$ROOT/distro/scripts/test-security-readiness.py"
    python3 "$ROOT/distro/scripts/test-ci-qualification.py"
    python3 "$ROOT/distro/scripts/test-beta-promotion.py"
    bash "$ROOT/distro/scripts/test-prepare-beta-promotion.sh"
    bash "$ROOT/distro/scripts/test-secret-hygiene.sh"
    bash "$ROOT/distro/scripts/test-service-hardening.sh"
    docker run --rm -v "$ROOT:/src" -w /src \
        cloudless-release-builder bash distro/scripts/test-trust-inventory.sh
    docker run --rm -v "$ROOT:/src" -w /src \
        cloudless-release-builder bash distro/scripts/test-sbom.sh
    bash "$ROOT/distro/scripts/scan-vulnerabilities.sh" --check
    bash "$ROOT/distro/scripts/test-incident-response.sh"
    bash "$ROOT/distro/scripts/test-qualification-contract.sh"
    bash "$ROOT/distro/scripts/test-browser-agent.sh"
    bash "$ROOT/distro/scripts/test-soak-contract.sh"
    python3 "$ROOT/distro/scripts/test-visual-regression-contract.py"
    bash -n "$ROOT/distro/scripts/test-installed-session.sh"
}

platform_job() {
    local platform="$1"
    local architecture="$2"
    docker run --rm -e CLOUDLESS_PLATFORM="$platform" -e CLOUDLESS_ARCH="$architecture" \
        -v "$ROOT:/src" -w /src/orchestrator cloudless-release-builder \
        go test -count=1 ./internal/catalog ./internal/modelfit ./internal/platform \
            ./internal/capabilities ./internal/provision
}

package_job() {
    docker run --rm -e CLOUDLESS_VERSION="$VERSION" -v "$ROOT:/src" -w /src \
        cloudless-package-builder bash distro/scripts/test-architectures.sh
    docker run --rm -e CLOUDLESS_VERSION="$VERSION" -e CLOUDLESS_SKIP_PACKAGE_BUILD=1 \
        -v "$ROOT:/src" -w /src cloudless-package-builder bash distro/scripts/test-packages.sh
    for architecture in amd64 arm64; do
        CLOUDLESS_VERSION="$VERSION" CLOUDLESS_TEST_ARCH="$architecture" \
            bash "$ROOT/distro/scripts/test-package-lifecycle.sh"
    done
    mapfile -t candidates < <(find "$ROOT/distro/out/packages" -maxdepth 1 -type f \
        -name "*_${VERSION}_*.deb" -print | sort)
    [ "${#candidates[@]}" -eq 12 ] || {
        echo "Expected exactly 12 AMD64/ARM64 release candidates, found ${#candidates[@]}." >&2
        return 1
    }
    sha256sum "${candidates[@]}" > "$work/package/packages.sha256"
}

visual_job() {
    local output_windows
    if command -v powershell.exe >/dev/null; then
        output_windows="$(wslpath -m "$work/visual")"
        powershell.exe -NoProfile -ExecutionPolicy Bypass -File \
            "$(wslpath -m "$ROOT/scripts/visual-regression.ps1")" \
            -OutputDirectory "$output_windows"
    elif command -v pwsh >/dev/null; then
        pwsh -NoProfile -File "$ROOT/scripts/visual-regression.ps1" -OutputDirectory "$work/visual"
    else
        echo "Full local qualification requires PowerShell and Microsoft Edge for visual capture." >&2
        return 1
    fi
}

soak_job() {
    local count="${CLOUDLESS_LOCAL_SOAK_COUNT:-3}"
    [[ "$count" =~ ^[1-9][0-9]*$ ]] || { echo "Invalid CLOUDLESS_LOCAL_SOAK_COUNT" >&2; return 1; }
    printf 'commit=%s\nchannel=%s\ncount=%s\n' "$COMMIT" "$CHANNEL" "$count" > "$work/soak/run.txt"
    docker run --rm -e SOAK_COUNT="$count" -v "$ROOT:/src" -w /src/orchestrator \
        cloudless-release-builder bash -lc \
        'set -o pipefail; export PATH="/usr/local/go/bin:$PATH"; go test -json -race -timeout=40m -count="$SOAK_COUNT" ./internal/jobs ./internal/recipeops ./internal/sparkcluster' \
        | tee "$work/soak/core.jsonl"
    docker run --rm -e SOAK_COUNT="$count" -v "$ROOT:/src" -w /src/orchestrator \
        cloudless-release-builder bash -lc \
        'set -o pipefail; export PATH="/usr/local/go/bin:$PATH"; go test -json -race -timeout=40m -count="$SOAK_COUNT" ./internal/api -run "Test(PublishedApplicationLifecycleMatrix|UnrelatedAppRemovalsRunConcurrently|InferenceJobObserverPersistsFailureAndRollbackTarget|AbortOperationCannotBeOverwrittenByCanceledLaunch|InterruptedPhaseSurvivesRepeatedRecoveryRestart|RollbackFailedRecipeCleansCandidateAndPersistsTruthfulFallback|RecipeAbortReturnsTrackableCleanupAndTransitionsStopping|ModelDownloadObserverJournalsProgressAndKeepsFailure|CanceledModelDownloadClearsJournalButKeepsCacheForResume|ManagedEngineDigestPullDoesNotRepeatUpdate|InactiveManagedEngineUpdatePullsInBackgroundWithoutRestart|TailscaleRoutesKeepCredentialsOutsideCloudless|BrowserRequestIsValidatedAndQueued|BrowserStatusAndRestoreRequest|TerminalHidePreservesSessionsAndTabs|TerminalProxyConcurrentLocalSessionsRemainIsolated|ClusterFailureMatrixAcrossInferenceLifecyclePhases|ClusterDisconnectFallsBackFromClusterOnlyModel|ClusterDisconnectKeepsSingleSparkCompatibleModel)"' \
        | tee "$work/soak/api.jsonl"
    for iteration in $(seq 1 "$count"); do
        printf 'iteration=%s\n' "$iteration" | tee -a "$work/soak/browser.log"
        bash "$ROOT/distro/scripts/test-browser-agent.sh" | tee -a "$work/soak/browser.log"
    done
}

job source "Source, concurrency and security policies" source_job
job generic-amd64 "generic / amd64" platform_job generic amd64
job generic-arm64 "generic / arm64" platform_job generic arm64
job dgx-spark-arm64 "dgx-spark / arm64" platform_job dgx-spark arm64
job packages "AMD64 and ARM64 package payloads" package_job
job visual "Interface capture smoke test" visual_job
job soak "Durable lifecycle soak" soak_job

package_artifact="package-qualification-$run_id-$attempt"
visual_artifact="visual-regression-$run_id-$attempt"
soak_artifact="lifecycle-soak-$run_id-$attempt"
tar -C "$work" -czf "$work/$package_artifact.tar.gz" package packages.log
tar -C "$work" -czf "$work/$visual_artifact.tar.gz" visual visual.log
tar -C "$work" -czf "$work/$soak_artifact.tar.gz" soak soak.log
completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [ "$(git -C "$ROOT" rev-parse HEAD)" != "$COMMIT" ] || \
   ! git -C "$ROOT" diff --quiet || ! git -C "$ROOT" diff --cached --quiet; then
    echo "Source changed while qualification was running; incomplete evidence remains at $work" >&2
    exit 1
fi

python3 - "$work" "$COMMIT" "$run_id" "$attempt" "$started_at" "$completed_at" <<'PY'
import hashlib, json, os, sys
from pathlib import Path

root = Path(sys.argv[1])
commit, run_id, attempt, started, completed = sys.argv[2:]
jobs = {
    "Source, concurrency and security policies": "source.log",
    "generic / amd64": "generic-amd64.log",
    "generic / arm64": "generic-arm64.log",
    "dgx-spark / arm64": "dgx-spark-arm64.log",
    "AMD64 and ARM64 package payloads": "packages.log",
    "Interface capture smoke test": "visual.log",
    "Durable lifecycle soak": "soak.log",
}

def identity(name):
    path = root / name
    data = path.read_bytes()
    if not data:
        raise SystemExit(f"qualification evidence is empty: {name}")
    return {"sha256": hashlib.sha256(data).hexdigest(), "size": len(data)}

job_rows = [
    {"name": label, "status": "success", "log": name, **identity(name)}
    for label, name in jobs.items()
]
artifacts = []
for prefix in ("package-qualification", "visual-regression", "lifecycle-soak"):
    logical = f"{prefix}-{run_id}-{attempt}"
    name = f"{logical}.tar.gz"
    artifacts.append({"name": logical, "file": name, **identity(name)})
document = {
    "schema": "cloudless.local-ci-evidence.v1",
    "sourceCommit": commit,
    "runId": int(run_id),
    "runAttempt": int(attempt),
    "startedAt": started,
    "completedAt": completed,
    "jobs": job_rows,
    "artifacts": artifacts,
}
temporary = root / ".evidence.json.tmp"
temporary.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
os.chmod(temporary, 0o600)
os.replace(temporary, root / "evidence.json")
PY

mv "$work" "$final"
python3 "$ROOT/distro/scripts/prepare-ci-qualification.py" \
    "$VERSION" "$CHANNEL" "$COMMIT" --local-evidence "$final/evidence.json" --output "$OUTPUT"
echo "Local exact-commit qualification retained at $final"
