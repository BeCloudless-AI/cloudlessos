#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
workflow="$ROOT/.github/workflows/multiarch.yml"
browser="$ROOT/distro/scripts/test-browser-agent.sh"
cluster="$ROOT/orchestrator/internal/sparkcluster/cluster_test.go"
terminal="$ROOT/orchestrator/internal/api/terminal_proxy_test.go"

for required in "$workflow" "$browser" "$cluster" "$terminal"; do
  test -f "$required" || {
    echo "Missing soak contract input: $required" >&2
    exit 1
  }
done

grep -Fq "SOAK_COUNT: \${{ github.event_name == 'schedule' && '25' || '3' }}" "$workflow"
grep -Fq 'go test -json -race -timeout=40m -count="$SOAK_COUNT"' "$workflow"
grep -Fq './internal/jobs ./internal/recipeops ./internal/sparkcluster' "$workflow"
grep -Fq 'TerminalProxyConcurrentLocalSessionsRemainIsolated' "$workflow"
grep -Fq 'bash ../distro/scripts/test-browser-agent.sh' "$workflow"
grep -Fq 'core.jsonl' "$workflow"
grep -Fq 'api.jsonl' "$workflow"
grep -Fq 'if: always()' "$workflow"
grep -Fq 'name: lifecycle-soak-' "$workflow"
grep -Fq 'retention-days: 30' "$workflow"
grep -Fq 'TestClusterStateChurnAcrossTwoToEightNodes' "$cluster"
grep -Fq 'TestTerminalProxyConcurrentLocalSessionsRemainIsolated' "$terminal"
grep -Fq 'TestClusterFailureMatrixAcrossInferenceLifecyclePhases' "$ROOT/orchestrator/internal/api/cluster_compute_test.go"
grep -Fq 'seq 1 24' "$browser"
grep -Fq 'burst request completion after restart' "$browser"

echo "Lifecycle soak contract covers scheduled repetition, browser restart bursts, terminal concurrency, failure-phase recovery and 2-8 Spark churn."
