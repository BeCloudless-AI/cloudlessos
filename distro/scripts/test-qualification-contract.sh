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
required_targets = {
    (item.get("platform"), item.get("architecture"), item.get("nodes"))
    for item in matrix.get("targets", []) if item.get("required")
}
expected_required = {
    ("virtualbox", "amd64", 1),
    ("generic-nvidia", "amd64", 1),
    ("dgx-spark", "arm64", 1),
    ("dgx-spark", "arm64", 2),
}
if required_targets != expected_required:
    raise SystemExit(f"required physical targets differ: {required_targets ^ expected_required}")
for item in matrix.get("targets", []):
    expected_tier = "supported" if item.get("required") else "preview"
    if item.get("supportTier") != expected_tier:
        raise SystemExit(f"invalid physical support tier for {item.get('id')}")
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
print("Physical qualification requires VM, NVIDIA and one/two-Spark targets; three-to-eight Sparks remain preview.")
PY
python3 "$ROOT/distro/scripts/test-physical-qualification.py"
python3 "$ROOT/distro/packages/cloudless-firstboot/cloudless-qualify" --help | grep -Fq '{begin,boot,boot-active,record,collect,soak,update-rollback,backup-restore,cluster-failure,status,plan,next,export,verify-export,verify-set}'
for phase in pending preparing downloading starting-workers loading optimizing verifying stopping rollback; do
    grep -Fq "waitQualificationPhaseGate(ctx, \"$phase\")" "$ROOT/orchestrator/internal/api/server.go"
done
grep -Fq 'qualificationPhaseGateOwnerUID uint32 = 0' "$ROOT/orchestrator/internal/api/qualification_gate.go"
grep -Fq 'qualificationRollbackRequested()' "$ROOT/orchestrator/internal/api/server.go"
python3 "$ROOT/distro/scripts/test-physical-release.py"

runner="$ROOT/distro/scripts/run-local-qualification.sh"
grep -Fq 'package-qualification-$run_id-$attempt' "$runner"
grep -Fq 'name "*_${VERSION}_*.deb"' "$runner"
grep -Fq 'Expected exactly 12 AMD64/ARM64 release candidates' "$runner"
grep -Fq 'visual-regression-$run_id-$attempt' "$runner"
grep -Fq 'lifecycle-soak-$run_id-$attempt' "$runner"
grep -Fq 'prepare-ci-qualification.py' "$runner"
grep -Fq 'go build -buildvcs=false' "$runner"
grep -Fq 'Source changed while qualification was running' "$runner"
grep -Fq 'CLOUDLESS_VERSION="$VERSION" CLOUDLESS_TEST_ARCH="$architecture"' "$runner"
if [ "$(grep -Fc 'export PATH="/usr/local/go/bin:$PATH"; go test -json -race' "$runner")" -ne 2 ]; then
    echo "Both local soak commands must pin the release-builder Go toolchain." >&2
    exit 1
fi
if [ -d "$ROOT/.github/workflows" ] && find "$ROOT/.github/workflows" -type f \( -name '*.yml' -o -name '*.yaml' \) -print -quit | grep -q .; then
    echo "Hosted GitHub Actions workflows must remain disabled." >&2
    exit 1
fi
echo "Local exact-commit qualification retains hashed package, visual and lifecycle-soak evidence."
