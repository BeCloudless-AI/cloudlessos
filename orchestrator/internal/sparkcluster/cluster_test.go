package sparkcluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
)

func TestActiveLinksUsesBothLogicalInterfacesForOnePhysicalPort(t *testing.T) {
	raw := `roceP2p1s0f0 port 1 ==> enP2p1s0f0np0 (Down)
roceP2p1s0f1 port 1 ==> enP2p1s0f1np1 (Up)
rocep1s0f0 port 1 ==> enp1s0f0np0 (Down)
rocep1s0f1 port 1 ==> enp1s0f1np1 (Up)`
	got := activeLinks(raw)
	if len(got) != 2 || got[0] != "enP2p1s0f1np1" || got[1] != "enp1s0f1np1" {
		t.Fatalf("activeLinks() = %#v", got)
	}
}

func TestEmptyConnectXOutputExplainsPowerGatedHardware(t *testing.T) {
	links := activeLinks("")
	if len(links) != 0 {
		t.Fatalf("activeLinks(empty) = %#v", links)
	}
}

func TestSSHPasswordCommandInvokesSSHThroughSSHPass(t *testing.T) {
	program, args := sshCommand("192.168.50.94", "ledomaine", true)
	if program != "sshpass" {
		t.Fatalf("program = %q", program)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "-d 3 ssh ") || !strings.Contains(joined, "ledomaine@192.168.50.94") {
		t.Fatalf("password SSH args = %q", joined)
	}
	if strings.Contains(joined, "SSHPASS") {
		t.Fatalf("password SSH command exposes an environment credential: %q", joined)
	}
	program, args = sshCommand("spark.local", "cloudless", false)
	joined = strings.Join(args, " ")
	if program != "ssh" || strings.Contains(joined, "sshpass") || !strings.Contains(joined, "-i "+keyPath) || !strings.Contains(joined, "IdentitiesOnly=yes") {
		t.Fatalf("key SSH command = %q %#v", program, args)
	}
}

func TestProtectedPasswordPipeUsesFD3AndRedactsFailures(t *testing.T) {
	out, err := runWithSecretFD(
		context.Background(), "not-in-argv-or-env", nil, "sh", "-c",
		`IFS= read -r value <&3; test "$value" = "not-in-argv-or-env"; printf accepted`,
	)
	if err != nil || out != "accepted" {
		t.Fatalf("protected pipe = %q, %v", out, err)
	}
	out, err = runWithSecretFD(
		context.Background(), "must-be-redacted", nil, "sh", "-c",
		`IFS= read -r value <&3; printf '%s' "$value" >&2; exit 7`,
	)
	if err == nil || strings.Contains(out, "must-be-redacted") || strings.Contains(err.Error(), "must-be-redacted") {
		t.Fatalf("credential escaped redaction: output=%q error=%v", out, err)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("redacted failure output = %q", out)
	}
}

func TestWorkerHelperUsesOfficialRayTopology(t *testing.T) {
	for _, want := range []string{"--network host", "--device nvidia.com/gpu=all", "ray[default]>=2.9", "ray start --block", "NCCL_SOCKET_IFNAME", "--num-gpus=1", "model-progress", "remove-container", "docker rm -f", "*.incomplete", "Model loading took"} {
		if !strings.Contains(workerScript, want) {
			t.Fatalf("worker helper missing %q", want)
		}
	}
}

func TestWorkerHelperIsValidPOSIXShell(t *testing.T) {
	command := exec.Command("sh", "-n")
	command.Stdin = strings.NewReader(workerScript)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("worker helper shell syntax is invalid: %s: %v", strings.TrimSpace(string(output)), err)
	}
}

func TestWorkerHelperOwnsPersistentNFSLifecycle(t *testing.T) {
	for _, want := range []string{"nfs-boot", "nfs-apply", "nfs-local", "nfs-status", "mount.nfs4", "Type=nfs4", "rw,hard,nosuid,nodev,noatime", ".cloudless-shared-storage-v1", "/var/lib/cloudless/shared-model-storage", "/var/lib/cloudless/models-cache/hub", "Options=bind"} {
		if !strings.Contains(workerScript, want) {
			t.Fatalf("worker helper is missing NFS contract %q", want)
		}
	}
}

func TestWorkerNFSSetupDoesNotTreatSuccessfulPreparationAsFailure(t *testing.T) {
	if strings.Contains(workerScript, `if ! systemctl enable --now "$nfs_unit" || {`) {
		t.Fatal("NFS setup uses an OR condition that rolls back after a successful preparation step")
	}
	for _, want := range []string{`setup_failed=0`, `if ! systemctl enable "$boot_unit"`, `elif ! systemctl start "$nfs_unit"`, `elif [ "$layout" = external ]`, `elif ! systemctl start "$hub_unit"`} {
		if !strings.Contains(workerScript, want) {
			t.Fatalf("NFS setup is missing staged failure handling %q", want)
		}
	}
}

func TestWorkerNFSBootWaitsForFabricAndRetriesFailures(t *testing.T) {
	for _, expected := range []string{
		"cloudless-cluster-model-storage.service",
		`ip route get "$server"`,
		`systemctl reset-failed "$nfs_unit" "$hub_unit"`,
		"Restart=on-failure",
		"RestartSec=5",
		"TimeoutStartSec=120",
		`systemctl disable --now "$boot_unit"`,
	} {
		if !strings.Contains(workerScript, expected) {
			t.Fatalf("worker NFS boot recovery is missing %q", expected)
		}
	}
	if strings.Index(workerScript, `ip route get "$server"`) > strings.Index(workerScript, `systemctl start "$nfs_unit"`) {
		t.Fatal("worker starts NFS before the private fabric route is ready")
	}
}

func TestClusterEnrollmentKeepsWorkerSSHAvailableAfterReboot(t *testing.T) {
	source, err := os.ReadFile("cluster.go")
	if err != nil {
		t.Fatal(err)
	}
	command := string(source)
	if !strings.Contains(command, "systemctl enable ssh.service") || !strings.Contains(command, "systemctl enable sshd.service") {
		t.Fatal("cluster enrollment does not persist the worker SSH service")
	}
}

func TestWorkerUpgradeSelfHealsSSHPersistence(t *testing.T) {
	for _, expected := range []string{
		"ensure-ssh)",
		"systemctl enable ssh.service",
		"systemctl enable sshd.service",
		"OpenSSH server service is not installed",
	} {
		if !strings.Contains(workerScript, expected) {
			t.Fatalf("worker helper is missing SSH persistence behavior %q", expected)
		}
	}

	source, err := os.ReadFile("cluster.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `workerPath+" ensure-ssh"`) {
		t.Fatal("worker helper upgrades do not enforce SSH persistence")
	}
}

func TestStorageRemovalCannotDiscardWorkerUpgradeFailure(t *testing.T) {
	source, err := os.ReadFile("model_storage.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if strings.Contains(text, "if err := upgradeStorageWorker") {
		t.Fatal("worker upgrade error is shadowed during local-storage restoration")
	}
	for _, expected := range []string{
		"peerErr := upgradeStorageWorker(ctx, node)",
		"status.Error = cleanStoragePeerError(peerErr)",
		"errors.Join(result, fmt.Errorf(\"restore local model storage on %s: %w\", node.Name, peerErr))",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("storage removal does not preserve worker failure evidence %q", expected)
		}
	}
}

func TestCoordinatorUsesRayAndWaitsForWorker(t *testing.T) {
	spec := CoordinatorSpec(engine.RunSpec{Args: []string{"vllm", "serve", "Qwen/Test"}})
	joined := strings.Join(spec.Args, " ")
	for _, want := range []string{"ray start --head", "cloudless-ray-worker", "--distributed-executor-backend ray", "--tensor-parallel-size 2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("coordinator command missing %q: %s", want, joined)
		}
	}
}

func TestCoordinatorUsesAllEightSparkRanks(t *testing.T) {
	nodes := make([]Node, 7)
	spec := coordinatorSpec(engine.RunSpec{Args: []string{"vllm", "serve", "Qwen/Test"}}, State{Configured: true, Nodes: nodes})
	if joined := strings.Join(spec.Args, " "); !strings.Contains(joined, "--tensor-parallel-size 8") {
		t.Fatalf("eight-Spark coordinator command = %s", joined)
	}
}

func TestCoordinatorUsesExactSelectedSubset(t *testing.T) {
	nodes := []Node{
		{Name: "spark-b", Host: "b.local", Fingerprint: "bbb"},
		{Name: "spark-c", Host: "c.local", Fingerprint: "ccc"},
		{Name: "spark-d", Host: "d.local", Fingerprint: "ddd"},
	}
	state := State{Configured: true, Nodes: nodes, SelectedHosts: []string{"bbb", "ddd"}}
	spec := coordinatorSpec(engine.RunSpec{Args: []string{"vllm", "serve", "Qwen/Test"}}, state)
	if joined := strings.Join(spec.Args, " "); !strings.Contains(joined, "--tensor-parallel-size 3") {
		t.Fatalf("selected coordinator command = %s", joined)
	}
	normalized := normalizeState(state)
	if normalized.ComputeNodeCount != 3 || !normalized.Nodes[0].Selected || normalized.Nodes[1].Selected || !normalized.Nodes[2].Selected {
		t.Fatalf("selected state = %#v", normalized)
	}
}

func TestCoordinatorSupportsEveryClusterSizeFromTwoThroughEight(t *testing.T) {
	nodes := make([]Node, 7)
	for i := range nodes {
		nodes[i] = Node{
			Name: "worker-" + strconv.Itoa(i+2), Host: "worker-" + strconv.Itoa(i+2) + ".local",
			Fingerprint: "fingerprint-" + strconv.Itoa(i+2), Healthy: true, WorkerReady: true,
		}
	}
	for total := 2; total <= 8; total++ {
		selectors := make([]string, total-1)
		for i := range selectors {
			selectors[i] = nodes[i].Fingerprint
		}
		state := State{Configured: true, Healthy: true, Nodes: nodes, SelectedHosts: selectors}
		spec := coordinatorSpec(engine.RunSpec{Args: []string{"vllm", "serve", "Qwen/Test"}}, state)
		want := "--tensor-parallel-size " + strconv.Itoa(total)
		if joined := strings.Join(spec.Args, " "); !strings.Contains(joined, want) {
			t.Fatalf("%d-Spark coordinator command missing %q: %s", total, want, joined)
		}
	}
}

func TestEmptySelectionUsesEveryEnrolledWorker(t *testing.T) {
	state := normalizeState(State{Configured: true, Nodes: []Node{{Host: "b"}, {Host: "c"}, {Host: "d"}}})
	if state.ComputeNodeCount != 4 {
		t.Fatalf("compute node count = %d", state.ComputeNodeCount)
	}
	for _, node := range state.Nodes {
		if !node.Selected {
			t.Fatalf("legacy automatic selection omitted %#v", node)
		}
	}
}

func TestUnselectedWorkerLossDoesNotFalselyDegradeSelectedCompute(t *testing.T) {
	state := normalizeState(State{
		Configured: true, Healthy: false,
		Checks:        []Check{{ID: "local-config", OK: true}, {ID: "local-links", OK: true}},
		SelectedHosts: []string{"good"},
		Nodes: []Node{
			{Host: "good", Healthy: true, WorkerReady: true},
			{Host: "standby", Healthy: false, WorkerReady: false},
		},
	})
	if !state.ComputeHealthy || !state.ComputeWorkerReady || state.ComputeNodeCount != 2 {
		t.Fatalf("selected compute was degraded by standby loss: %#v", state)
	}
	state.SelectedHosts = []string{"standby"}
	state = normalizeState(state)
	if state.ComputeHealthy || state.ComputeWorkerReady {
		t.Fatalf("failed selected worker was reported ready: %#v", state)
	}
}

func TestCoordinatorLinkLossDegradesEveryComputeSubset(t *testing.T) {
	state := normalizeState(State{
		Configured: true, Healthy: false,
		Checks: []Check{{ID: "local-config", OK: true}, {ID: "local-links", OK: false}},
		Nodes:  []Node{{Host: "worker", Healthy: true, WorkerReady: true}},
	})
	if state.ComputeHealthy {
		t.Fatalf("coordinator link loss was reported healthy: %#v", state)
	}
}

func TestParseStorageTelemetry(t *testing.T) {
	total, available := parseStorageTelemetry("982345678901 456789012345\n")
	if total != 982345678901 || available != 456789012345 {
		t.Fatalf("storage = %d %d", total, available)
	}
	if total, available := parseStorageTelemetry("100 101"); total != 0 || available != 0 {
		t.Fatalf("invalid storage accepted = %d %d", total, available)
	}
}

func TestDiscoveryNameDecodesAvahiEscapes(t *testing.T) {
	if got := discoveryName(`spark-44f5\032SSH`, "spark-44f5.local"); got != "spark-44f5" {
		t.Fatalf("discoveryName() = %q", got)
	}
}

func TestConnectXDiagnosticReportsFirmwareCableState(t *testing.T) {
	got := connectXDiagnostic("mlx5_core: Port module event: module 0, Cable unplugged\ncx7-pcie-hotplug: Cable removal")
	for _, want := range []string{"does not detect a supported cable", "QSFP112 DAC", "Ethernet-only"} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostic missing %q: %q", want, got)
		}
	}
}

func TestNetplanCreatesSeparateSubnets(t *testing.T) {
	got := netplan([]string{"enP2p1s0f1np1", "enp1s0f1np1"}, 2)
	for _, want := range []string{"enP2p1s0f1np1:", "10.100.0.2/24", "enp1s0f1np1:", "10.100.1.2/24", "dhcp4: false", "optional: true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("netplan missing %q:\n%s", want, got)
		}
	}
}

func TestNetplanSupportsEighthSparkAddress(t *testing.T) {
	got := netplan([]string{"cx-a", "cx-b"}, 8)
	for _, want := range []string{"10.100.0.8/24", "10.100.1.8/24"} {
		if !strings.Contains(got, want) {
			t.Fatalf("eighth-node netplan missing %q: %s", want, got)
		}
	}
}

func TestNormalizeStateMigratesLegacyPair(t *testing.T) {
	state := normalizeState(State{Configured: true, WorkerReady: true, PeerName: "spark-2", PeerHost: "spark-2.local", Username: "cloudless", PeerIPs: []string{"10.100.0.2"}})
	if state.NodeCount != 2 || len(state.Nodes) != 1 || state.Nodes[0].Host != "spark-2.local" || state.Topology != "direct" {
		t.Fatalf("legacy state migration = %#v", state)
	}
}

func TestNodeCountSupportsEightSparks(t *testing.T) {
	nodes := make([]Node, 7)
	for i := range nodes {
		nodes[i] = Node{Name: "worker", WorkerReady: true}
	}
	if got := nodeCountForState(State{Configured: true, Nodes: nodes}); got != 8 {
		t.Fatalf("node count = %d", got)
	}
}

func TestEnrollmentIndexEnforcesUniqueEightSparkLimit(t *testing.T) {
	state := State{Configured: true, Nodes: []Node{{Host: "spark-2.local"}, {Host: "spark-3.local"}}}
	if got, err := enrollmentIndex(state, "spark-4.local"); err != nil || got != 4 {
		t.Fatalf("next enrollment = %d, %v", got, err)
	}
	if _, err := enrollmentIndex(state, "SPARK-2.LOCAL"); err == nil || !strings.Contains(err.Error(), "already part") {
		t.Fatalf("duplicate enrollment error = %v", err)
	}
	state.Nodes = make([]Node, 7)
	if _, err := enrollmentIndex(state, "spark-9.local"); err == nil || !strings.Contains(err.Error(), "maximum of 8") {
		t.Fatalf("cluster limit error = %v", err)
	}
}

func TestReusableClusterAddressesAcceptsExactStaleCloudlessAddresses(t *testing.T) {
	links := []string{"enP2p1s0f1np1", "enp1s0f1np1"}
	raw := "10: enp1s0f1np1 inet 10.100.1.2/24 brd 10.100.1.255 scope global enp1s0f1np1\n" +
		"12: enP2p1s0f1np1 inet 10.100.0.2/24 brd 10.100.0.255 scope global enP2p1s0f1np1\n"
	if !reusableClusterAddresses(raw, links, 2) {
		t.Fatal("exact stale Cloudless peer addresses should be reusable")
	}
}

func TestReusableClusterAddressesRejectsRealConflicts(t *testing.T) {
	links := []string{"enP2p1s0f1np1", "enp1s0f1np1"}
	for _, raw := range []string{
		"5: eth0 inet 10.100.0.2/24 scope global eth0\n",
		"12: enP2p1s0f1np1 inet 10.100.0.99/24 scope global enP2p1s0f1np1\n",
	} {
		if reusableClusterAddresses(raw, links, 2) {
			t.Fatalf("conflicting address inventory was accepted: %q", raw)
		}
	}
}

func TestValidateTargetRejectsShellInput(t *testing.T) {
	for _, tc := range []struct{ host, user string }{{"spark.local;reboot", "nvidia"}, {"spark.local", "nvidia;id"}, {"$(reboot)", "nvidia"}} {
		if err := validateTarget(tc.host, tc.user); err == nil {
			t.Fatalf("accepted unsafe target %#v", tc)
		}
	}
	if err := validateTarget("192.168.1.20", "ledomaine"); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
}

func TestRouteConflictDetectsReservedClusterNetworks(t *testing.T) {
	if !routeConflict("10.100.0.0/24 dev eth9 proto kernel") {
		t.Fatal("missed first reserved subnet")
	}
	if !routeConflict("10.100.1.0/24 dev eth9 proto kernel") {
		t.Fatal("missed second reserved subnet")
	}
	if routeConflict("default via 192.168.1.1 dev eth0") {
		t.Fatal("reported unrelated route")
	}
}

func TestClusterAddressOnlyMatchesReservedFabricSubnets(t *testing.T) {
	for _, address := range []string{"10.100.0.2/24", "10.100.1.1/24"} {
		if !isClusterAddress(address) {
			t.Fatalf("expected cluster address %q", address)
		}
	}
	for _, address := range []string{"10.100.2.1/24", "192.168.50.109/24", "not-an-address"} {
		if isClusterAddress(address) {
			t.Fatalf("unexpected cluster address %q", address)
		}
	}
}

func TestFabricHealthProbeAllowsAddressDiscoveryToSettle(t *testing.T) {
	got := strings.Join(fabricPingArguments("10.100.0.2"), " ")
	if got != "-c 3 -i 0.25 -W 1 10.100.0.2" {
		t.Fatalf("fabric ping arguments = %q", got)
	}
}

func TestFabricHealthRequiresZeroPacketLoss(t *testing.T) {
	if !fabricProbeHealthy("3 packets transmitted, 3 received, 0% packet loss", nil) {
		t.Fatal("zero-loss fabric was rejected")
	}
	if fabricProbeHealthy("3 packets transmitted, 2 received, 33.3333% packet loss", nil) {
		t.Fatal("partial packet loss was reported healthy")
	}
	if fabricProbeHealthy("", nil) {
		t.Fatal("missing packet-loss evidence was reported healthy")
	}
}

func TestAggregateHealthLayerKeepsFailureDomainsTruthful(t *testing.T) {
	healthy := HealthLayer{Status: "healthy"}
	attention := HealthLayer{Status: "attention"}
	if got := aggregateHealthLayer("Fabric", []HealthLayer{healthy, attention}, false); got.Status != "attention" {
		t.Fatalf("fabric aggregate = %#v", got)
	}
	if got := aggregateHealthLayer("Runtime", []HealthLayer{{Status: "idle"}, {Status: "idle"}}, true); got.Status != "idle" {
		t.Fatalf("idle runtime aggregate = %#v", got)
	}
	if got := aggregateHealthLayer("Runtime", []HealthLayer{healthy, {Status: "idle"}}, true); got.Status != "attention" {
		t.Fatalf("partially active runtime aggregate = %#v", got)
	}
}

func TestClusterOperationLifecycleIsExplicit(t *testing.T) {
	operation := Operation{ID: "connect-1", Phase: "peer-network"}
	if !operation.Active() {
		t.Fatal("in-flight cluster operation was not active")
	}
	operation.Phase = "error"
	if operation.Active() {
		t.Fatal("failed cluster operation remained active")
	}
	operation.Phase = "completed"
	if operation.Active() {
		t.Fatal("completed cluster operation remained active")
	}
}

func TestInterruptedOperationRecoveryCoversServiceRestartAndMachineReboot(t *testing.T) {
	base := State{Operation: Operation{
		ID: "disconnect-1", Action: "disconnect", Phase: "peer-cleanup",
		OwnerPID: 42, OwnerBootID: "boot-a",
		Nodes: []OperationNode{
			{Name: "spark-b", Host: "b.local", Phase: "cleaned", Cleaned: true},
			{Name: "spark-c", Host: "c.local", Phase: "removing"},
		},
	}}
	for _, test := range []struct {
		name   string
		pid    int
		bootID string
	}{
		{name: "service restart", pid: 43, bootID: "boot-a"},
		{name: "machine reboot with reused pid", pid: 42, bootID: "boot-b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			recovered, changed := recoverInterruptedOperation(base, test.pid, test.bootID)
			if !changed || recovered.Operation.Phase != "error" || !recovered.Operation.RollbackRequired {
				t.Fatalf("recovered operation = %#v, changed = %v", recovered.Operation, changed)
			}
			if !recovered.Operation.Nodes[0].Cleaned || recovered.Operation.Nodes[1].Phase != "unknown" {
				t.Fatalf("per-node cleanup evidence was lost: %#v", recovered.Operation.Nodes)
			}
		})
	}
	if _, changed := recoverInterruptedOperation(base, 42, "boot-a"); changed {
		t.Fatal("live owner was mistaken for an interrupted operation")
	}
}

func TestInterruptedMutationPhasesAlwaysBecomeCleanupObligations(t *testing.T) {
	for action, phases := range map[string][]string{
		"connect":    {"preflight", "identity", "peer-network", "coordinator-network", "saving", "verifying"},
		"disconnect": {"stopping-workers", "peer-cleanup", "coordinator-network"},
	} {
		for _, phase := range phases {
			t.Run(action+"/"+phase, func(t *testing.T) {
				state := State{Operation: Operation{
					ID: action + "-1", Action: action, Phase: phase,
					OwnerPID: 10, OwnerBootID: "old-boot",
				}}
				recovered, changed := recoverInterruptedOperation(state, 11, "new-boot")
				if !changed || !recovered.Operation.RollbackRequired || recovered.Operation.Phase != "error" {
					t.Fatalf("%s/%s recovery = %#v", action, phase, recovered.Operation)
				}
			})
		}
	}
}

func TestPartialCleanupPreservesPerNodeEvidence(t *testing.T) {
	operation := Operation{Nodes: []OperationNode{
		{Name: "spark-b", Host: "b.local", Phase: "pending"},
		{Name: "spark-c", Host: "c.local", Phase: "pending"},
	}}
	if !recordCleanupResult(&operation, "b.local", nil) ||
		!recordCleanupResult(&operation, "c.local", errors.New("connection refused")) {
		t.Fatal("cleanup result did not match enrolled nodes")
	}
	if !operation.Nodes[0].Cleaned || operation.Nodes[0].Phase != "cleaned" {
		t.Fatalf("successful cleanup evidence = %#v", operation.Nodes[0])
	}
	if operation.Nodes[1].Cleaned || operation.Nodes[1].Phase != "cleanup-required" ||
		!strings.Contains(operation.Nodes[1].Error, "connection refused") {
		t.Fatalf("failed cleanup evidence = %#v", operation.Nodes[1])
	}
}

func TestConfiguredLegacyStateIsCoordinatorButRoleReversalIsExplicit(t *testing.T) {
	legacy := normalizeState(State{Configured: true, Nodes: []Node{{Host: "worker"}}})
	if legacy.Role != "coordinator" {
		t.Fatalf("legacy role = %q", legacy.Role)
	}
	reversed := normalizeState(State{Configured: true, Role: "worker", Nodes: []Node{{Host: "coordinator"}}})
	if reversed.Role != "worker" {
		t.Fatalf("explicit worker role was silently rewritten: %q", reversed.Role)
	}
}

func TestFindNodeIndexUsesStableIdentityAcrossAddressChanges(t *testing.T) {
	state := State{Nodes: []Node{
		{Name: "spark-b", Host: "192.168.1.20", Fingerprint: "SHA256:bbb"},
		{Name: "spark-c", Host: "192.168.1.21", Fingerprint: "SHA256:ccc"},
	}}
	for _, selector := range []string{"spark-b", "192.168.1.20", "sha256:BBB"} {
		index, err := findNodeIndex(state, selector)
		if err != nil || index != 0 {
			t.Fatalf("selector %q = %d, %v", selector, index, err)
		}
	}
}

func TestClusterStateChurnAcrossTwoToEightNodes(t *testing.T) {
	for cycle := 0; cycle < 100; cycle++ {
		for total := 2; total <= 8; total++ {
			nodes := make([]Node, total-1)
			selectors := make([]string, 0, total-1)
			for index := range nodes {
				identity := fmt.Sprintf("fingerprint-%d-%d", cycle, index+2)
				nodes[index] = Node{
					Name: "spark-" + strconv.Itoa(index+2), Host: fmt.Sprintf("spark-%d.local", index+2),
					Fingerprint: identity, Healthy: true, WorkerReady: true,
				}
				if (index+cycle)%2 == 0 || total == 2 {
					selectors = append(selectors, identity)
				}
			}
			connected := normalizeState(State{
				Configured: true, Healthy: true, Role: "coordinator",
				Checks:        []Check{{ID: "local-config", OK: true}, {ID: "local-links", OK: true}},
				Nodes:         nodes,
				SelectedHosts: selectors,
			})
			if connected.NodeCount != total || connected.ComputeNodeCount != 1+len(selectors) ||
				!connected.ComputeHealthy || !connected.ComputeWorkerReady {
				t.Fatalf("cycle %d total %d healthy state = %#v", cycle, total, connected)
			}

			failedIndex := cycle % len(nodes)
			connected.Nodes[failedIndex].Healthy = false
			connected.Nodes[failedIndex].WorkerReady = false
			degraded := normalizeState(connected)
			failedSelected := degraded.Nodes[failedIndex].Selected
			if failedSelected && (degraded.ComputeHealthy || degraded.ComputeWorkerReady) {
				t.Fatalf("cycle %d selected peer loss remained ready: %#v", cycle, degraded)
			}
			if !failedSelected && (!degraded.ComputeHealthy || !degraded.ComputeWorkerReady) {
				t.Fatalf("cycle %d standby peer loss degraded selected compute: %#v", cycle, degraded)
			}

			degraded.Nodes[failedIndex].Healthy = true
			degraded.Nodes[failedIndex].WorkerReady = true
			reconnected := normalizeState(degraded)
			if !reconnected.ComputeHealthy || !reconnected.ComputeWorkerReady {
				t.Fatalf("cycle %d reconnect did not restore readiness: %#v", cycle, reconnected)
			}
			disconnected := normalizeState(State{})
			if disconnected.Configured || disconnected.NodeCount != 1 || disconnected.ComputeHealthy ||
				disconnected.ComputeWorkerReady || len(disconnected.Nodes) != 0 || len(disconnected.SelectedHosts) != 0 {
				t.Fatalf("cycle %d disconnect retained cluster state: %#v", cycle, disconnected)
			}
		}
	}
}
