package sparkcluster

import (
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
	if !strings.HasPrefix(joined, "-e ssh ") || !strings.Contains(joined, "ledomaine@192.168.50.94") {
		t.Fatalf("password SSH args = %q", joined)
	}
	program, args = sshCommand("spark.local", "cloudless", false)
	joined = strings.Join(args, " ")
	if program != "ssh" || strings.Contains(joined, "sshpass") || !strings.Contains(joined, "-i "+keyPath) || !strings.Contains(joined, "IdentitiesOnly=yes") {
		t.Fatalf("key SSH command = %q %#v", program, args)
	}
}

func TestWorkerHelperUsesOfficialRayTopology(t *testing.T) {
	for _, want := range []string{"--network host", "--device nvidia.com/gpu=all", "ray[default]>=2.9", "ray start --block", "NCCL_SOCKET_IFNAME", "--num-gpus=1"} {
		if !strings.Contains(workerScript, want) {
			t.Fatalf("worker helper missing %q", want)
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
