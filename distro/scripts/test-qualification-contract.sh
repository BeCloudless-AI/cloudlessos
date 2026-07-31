#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
python3 - "$ROOT/distro/release/physical-validation-matrix.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    matrix = json.load(handle)
if matrix.get("schema") != "cloudless.physical-validation.v1":
    raise SystemExit("unsupported physical validation schema")
if matrix.get("minimumBootCycles", 0) < 10:
    raise SystemExit("physical qualification must require at least ten boot cycles")
targets = {
    (item.get("platform"), item.get("architecture"), item.get("nodes"))
    for item in matrix.get("targets", [])
}
required = {
    ("virtualbox", "amd64", 1),
    ("generic-nvidia", "amd64", 1),
    *(("dgx-spark", "arm64", nodes) for nodes in range(1, 9)),
}
if targets != required:
    raise SystemExit(f"physical targets differ: missing={required-targets}, extra={targets-required}")
checks = set(matrix.get("requiredChecks", []))
for check in {
    "clean-install", "ten-boot-cycles", "graphical-session", "display-and-scale",
    "locale-and-timezone", "power-controls", "virtual-keyboard", "browser-and-terminal",
    "app-lifecycle", "model-load-switch-abort", "update-and-rollback",
    "backup-and-restore", "support-bundle",
}:
    if check not in checks:
        raise SystemExit(f"missing physical check: {check}")
cluster = set(matrix.get("clusterChecks", []))
for check in {
    "discover-and-enroll", "fabric-and-ssh-preflight", "selected-topology",
    "distributed-model-load", "peer-loss", "disconnect-and-reconnect",
    "cluster-credential-rebind",
}:
    if check not in cluster:
        raise SystemExit(f"missing cluster check: {check}")
failures = set(matrix.get("clusterFailureDomains", []))
expected_failures = {
    "packet-loss", "management-address-change", "coordinator-loss",
    "selected-worker-loss", "coordinator-reboot", "role-reversal-rejected",
    "partial-cleanup",
}
if failures != expected_failures:
    raise SystemExit(f"cluster failure domains differ: {failures ^ expected_failures}")
phases = set(matrix.get("inferenceLifecyclePhases", []))
expected_phases = {
    "pending", "preparing", "downloading", "starting-workers", "loading",
    "optimizing", "verifying", "stopping", "rollback",
}
if phases != expected_phases:
    raise SystemExit(f"inference lifecycle phases differ: {phases ^ expected_phases}")
failure_targets = {item.get("id") for item in matrix.get("targets", []) if item.get("failureMatrix")}
if failure_targets != {"dgx-spark-arm64-2"}:
    raise SystemExit(f"representative physical failure matrix targets differ: {failure_targets}")
print("Physical qualification contract covers VM, NVIDIA and every one-to-eight-Spark topology.")
PY
python3 "$ROOT/distro/scripts/test-physical-qualification.py"
python3 "$ROOT/distro/packages/cloudless-firstboot/cloudless-qualify" --help | grep -Fq '{begin,boot,boot-active,record,collect,soak,status,plan,next,export,verify-export,verify-set}'
python3 "$ROOT/distro/scripts/test-physical-release.py"

workflow="$ROOT/.github/workflows/multiarch.yml"
grep -Fq 'name: package-qualification-${{ github.run_id }}-${{ github.run_attempt }}' "$workflow"
grep -Fq 'distro/out/packages/*.deb' "$workflow"
grep -Fq 'name: visual-regression-${{ github.run_id }}-${{ github.run_attempt }}' "$workflow"
test "$(grep -Fc 'retention-days: 30' "$workflow")" -ge 3
echo "CI retains package, visual and lifecycle-soak evidence for 30 days."
