package api

import (
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func TestRecipeClusterFingerprintIsOrderIndependentAndTracksGeneration(t *testing.T) {
	created := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	cluster := sparkcluster.State{
		Configured: true, Role: "coordinator", Topology: "switch", CreatedAt: created,
		LocalLinks: []string{"eth2", "eth1"}, LocalIPs: []string{"10.0.0.2", "10.0.0.1"},
		Nodes: []sparkcluster.Node{
			{Name: "b", Host: "10.0.0.4", Username: "user", Fingerprint: "bb", Links: []string{"eth2"}, IPs: []string{"10.1.0.4"}},
			{Name: "a", Host: "10.0.0.3", Username: "user", Fingerprint: "aa", Links: []string{"eth1"}, IPs: []string{"10.1.0.3"}},
		},
	}
	first, err := recipeClusterFingerprint(cluster)
	if err != nil {
		t.Fatal(err)
	}
	cluster.Nodes[0], cluster.Nodes[1] = cluster.Nodes[1], cluster.Nodes[0]
	cluster.LocalLinks[0], cluster.LocalLinks[1] = cluster.LocalLinks[1], cluster.LocalLinks[0]
	second, err := recipeClusterFingerprint(cluster)
	if err != nil || first != second {
		t.Fatalf("order changed fingerprint: %q != %q, %v", first, second, err)
	}
	cluster.CreatedAt = created.Add(time.Second)
	third, err := recipeClusterFingerprint(cluster)
	if err != nil || third == first {
		t.Fatalf("generation did not change fingerprint: %q, %v", third, err)
	}
}

func TestRecipePlatformFingerprintTracksEvidence(t *testing.T) {
	values := map[string]string{"architecture": "arm64", "driver": "580.95"}
	first, err := recipePlatformFingerprint(values)
	if err != nil {
		t.Fatal(err)
	}
	values["driver"] = "581.00"
	second, err := recipePlatformFingerprint(values)
	if err != nil || first == second {
		t.Fatalf("driver did not change platform fingerprint: %q %q, %v", first, second, err)
	}
}
