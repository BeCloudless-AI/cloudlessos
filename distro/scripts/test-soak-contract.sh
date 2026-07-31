#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
runner="$ROOT/distro/scripts/run-local-qualification.sh"
browser="$ROOT/distro/scripts/test-browser-agent.sh"
cluster="$ROOT/orchestrator/internal/sparkcluster/cluster_test.go"
terminal="$ROOT/orchestrator/internal/api/terminal_proxy_test.go"

for required in "$runner" "$browser" "$cluster" "$terminal"; do
  test -f "$required" || {
    echo "Missing soak contract input: $required" >&2
    exit 1
  }
done

grep -Fq 'CLOUDLESS_LOCAL_SOAK_COUNT:-3' "$runner"
grep -Fq 'go test -json -race -timeout=40m -count="$SOAK_COUNT"' "$runner"
grep -Fq './internal/jobs ./internal/recipeops ./internal/sparkcluster' "$runner"
grep -Fq 'TerminalProxyConcurrentLocalSessionsRemainIsolated' "$runner"
grep -Fq 'test-browser-agent.sh' "$runner"
grep -Fq 'core.jsonl' "$runner"
grep -Fq 'api.jsonl' "$runner"
grep -Fq 'lifecycle-soak-' "$runner"
grep -Fq 'sha256' "$ROOT/distro/scripts/prepare-ci-qualification.py"
grep -Fq 'TestClusterStateChurnAcrossTwoToEightNodes' "$cluster"
grep -Fq 'TestTerminalProxyConcurrentLocalSessionsRemainIsolated' "$terminal"
grep -Fq 'TestClusterFailureMatrixAcrossInferenceLifecyclePhases' "$ROOT/orchestrator/internal/api/cluster_compute_test.go"
grep -Fq 'seq 1 24' "$browser"
grep -Fq 'burst request completion after restart' "$browser"

echo "Local lifecycle soak contract covers repetition, retained evidence, browser restart bursts, terminal concurrency, failure-phase recovery and 2-8 Spark churn."
