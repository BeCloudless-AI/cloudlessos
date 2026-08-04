package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebIncludesSparkClusterWizard(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{"id=\"dgx-cluster-card\"", "id=\"cluster-surface\"", "NOT CONFIGURED", "CONNECT NOW", "MANAGE", "function openSparkClusterSurface", "function renderSparkClusterPage", "Find a Spark", "Choose a DGX Spark", "Plug in the high-speed cable", "One cable. The same port on both Sparks.", "Yes, the cable is plugged into both Sparks", "Check my connection", "Spark ${p.nodeIndex", "Add Spark to cluster", "cluster-connect-progress", "Verify node", "cluster-add", "Add another Spark", "2–8 DGX Sparks", "Managed RoCE switch", "clearSparkClusterSecrets", "X-Cloudless-Action':'spark-cluster-create'", "X-Cloudless-Action':'spark-cluster-disconnect'", "never saved"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded UI missing %q", want)
		}
	}
	for _, want := range []string{"Disconnect and switch to local", "Stop distributed AI", "Remove private fabric", "Start local AI", "cluster-disconnect-eta", "formatRemaining", "engineExecutionMode = 'local'"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded cluster UI missing safe local fallback progress %q", want)
		}
	}
	for _, want := range []string{"Compute subset:", "data-cluster-node", "Save selection", "function openSparkAddressUpdate", "Update Spark address", "X-Cloudless-Action':'spark-cluster-rebind'", "storage free", "physicalLink"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded cluster UI missing subset, telemetry, or address recovery %q", want)
		}
	}
	for _, want := range []string{"clusterCheckFailureLabels", "The other Spark has a private network conflict", "will be reused"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded cluster UI missing stale-address guidance %q", want)
		}
	}
	for _, want := range []string{"const createFailed = Boolean(p.ready && w.error && !w.busy)", "The Spark was not added", "Try adding again"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded cluster UI missing connection failure state %q", want)
		}
	}
	for _, want := range []string{"id=\"home-cluster\"", "function renderHomeCluster", "-Spark compute is ready", "computeReady ? 'Manage'", "host.querySelector('button').onclick = openSparkClusterSurface", "Runs across ${clusterNodes} Sparks", "Use all ${clusterNodes} Sparks", "mode: selectedMode", "memoryAccountingSummary(cluster, true)", "Powered by Hermes Agent", "Active across ${engineNodes} Sparks", "served by your Spark cluster", "${runtimeLabel} · ${sparkCount(connectedSpark)} Sparks", "executionMode === 'cluster'"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded UI missing system-wide cluster experience %q", want)
		}
	}
	if strings.Contains(html, "Models for both Sparks") {
		t.Fatal("ready-state cluster card must open management, not Model Manager")
	}
	if strings.Contains(html, "{ id: 'spark-cluster'") {
		t.Fatal("Spark Cluster must live inside DGX Dashboard, not as a separate Settings item")
	}
	for _, want := range []string{"clusterRadarSweep", "clusterPeerBreathe", "clusterCableFlow", "clusterLinkFlow", "clusterCableFlowVertical", "cluster-find-button", "cluster-radar-status", "cluster-busy-panel", "@media (max-width: 480px)", "prefers-reduced-motion: reduce", ".motion-disabled .cluster-radar-sweep", ".render-inference-active .cluster-radar-sweep", ".cluster-cable-guide:has(#cluster-cable:checked) .cluster-cable-flow"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded cluster UI missing lightweight motion safeguard %q", want)
		}
	}
	for _, obsolete := range []string{"cluster-lan-visual", "clusterLanPing", "clusterDiscoveryGlow"} {
		if strings.Contains(html, obsolete) {
			t.Fatalf("embedded cluster UI still contains obsolete discovery animation %q", obsolete)
		}
	}
	for _, want := range []string{"role=\"dialog\" aria-modal=\"true\"", "aria-live=\"polite\"", "role=\"status\"", "role=\"alert\"", "e.key === 'Enter'", "e.key !== 'Tab'", "CloudlessOS could not inspect Spark clustering.", "id=\"cluster-status-retry\""} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded cluster UI missing accessibility or recovery behavior %q", want)
		}
	}
}

func TestSparkClusterLayoutKeepsDeliberateCardSpacing(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{
		".cluster-page { display: flex; flex-direction: column; gap: 20px; }",
		".cluster-page > .machine-card { padding: 24px; }",
		".cluster-page .machine-card > * + * { margin-top: 18px; }",
		".cluster-form { display: grid; gap: 16px; }",
		"width: min(1180px, 100%)",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("Spark cluster layout missing spacing rule %q", want)
		}
	}
}

func TestEmbeddedWebShowsAllSparkGPUs(t *testing.T) {
	data, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, want := range []string{"Connected Spark", "inf-node-gpus", "connected Sparks", "gpu.peers", "unavailablePeers", "Telemetry unavailable"} {
		if !strings.Contains(html, want) {
			t.Fatalf("embedded UI missing peer hardware visibility %q", want)
		}
	}
	if strings.Contains(html, "peerMissing") {
		t.Fatal("embedded UI still references the removed single-peer telemetry flag")
	}
}
