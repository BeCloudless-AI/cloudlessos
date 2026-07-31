package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func TestRecipeSelectsExactSubsetFromLargerCluster(t *testing.T) {
	cluster := sparkcluster.State{Configured: true, Nodes: []sparkcluster.Node{
		{Name: "spark-b", Host: "b", Fingerprint: "bbb", Healthy: true, WorkerReady: true},
		{Name: "spark-c", Host: "c", Fingerprint: "ccc", Healthy: true, WorkerReady: true},
		{Name: "spark-d", Host: "d", Fingerprint: "ddd", Healthy: false, WorkerReady: false},
	}}
	recipe := localrecipes.Recipe{Distributed: localrecipes.Distributed{Nodes: 2, SelectedNodes: []string{"spark-c"}}}
	selected, err := selectRecipeCluster(recipe, cluster)
	if err != nil {
		t.Fatal(err)
	}
	if selected.NodeCount != 2 || len(selected.Nodes) != 1 || selected.Nodes[0].Name != "spark-c" || !selected.Healthy {
		t.Fatalf("selected cluster = %#v", selected)
	}
}

func TestRecipeAutoPlacementIgnoresUnselectedUnhealthySpark(t *testing.T) {
	cluster := sparkcluster.State{Configured: true, Nodes: []sparkcluster.Node{
		{Name: "bad", Fingerprint: "aaa", Healthy: false},
		{Name: "good", Fingerprint: "bbb", Healthy: true, WorkerReady: true},
	}}
	recipe := localrecipes.Recipe{Distributed: localrecipes.Distributed{Nodes: 2}}
	selected, err := selectRecipeCluster(recipe, cluster)
	if err != nil || len(selected.Nodes) != 1 || selected.Nodes[0].Name != "good" {
		t.Fatalf("automatic cluster = %#v, %v", selected, err)
	}
}

func TestRecipeSelectionRejectsTwoAliasesForSameSpark(t *testing.T) {
	cluster := sparkcluster.State{Configured: true, Nodes: []sparkcluster.Node{
		{Name: "spark-b", Host: "spark-b.local", Fingerprint: "bbb", Healthy: true, WorkerReady: true},
		{Name: "spark-c", Host: "spark-c.local", Fingerprint: "ccc", Healthy: true, WorkerReady: true},
	}}
	recipe := localrecipes.Recipe{Distributed: localrecipes.Distributed{
		Nodes: 3, SelectedNodes: []string{"spark-b", "spark-b.local"},
	}}
	if _, err := selectRecipeCluster(recipe, cluster); err == nil {
		t.Fatal("two selectors for one Spark were accepted")
	}
}

func TestRecipeRuntimeUsesOnlySelectedWorker(t *testing.T) {
	draft := localrecipes.NewDraft()
	draft.Distributed.SelectedNodes = []string{"ccc"}
	store := localrecipes.New(t.TempDir())
	recipe, err := store.Create(draft)
	if err != nil {
		t.Fatal(err)
	}
	cluster := sparkcluster.State{
		Configured: true, NodeCount: 3, LocalIPs: []string{"10.100.0.1"}, LocalLinks: []string{"enp1s0f0np0"},
		Nodes: []sparkcluster.Node{
			{Name: "spark-b", Host: "b.local", Username: "userb", Fingerprint: "bbb", IPs: []string{"10.100.0.2"}, Healthy: true, WorkerReady: true},
			{Name: "spark-c", Host: "c.local", Username: "userc", Fingerprint: "ccc", IPs: []string{"10.100.0.3"}, Healthy: true, WorkerReady: true},
		},
	}
	env, _, err := writeRecipeRuntime(recipe, t.TempDir(), cluster, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	peers, err := recipeDistributionPeers(recipe, cluster, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].Name != "spark-c" || peers[0].Checkout != "/home/userc/.local/share/cloudless/recipes-runtime/"+recipe.ID {
		t.Fatalf("selected peers = %#v", peers)
	}
	config, err := os.ReadFile(filepath.Join(env["HOME"], ".ssh", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if text := string(config); !strings.Contains(text, "HostName 10.100.0.3") || strings.Contains(text, "10.100.0.2") {
		t.Fatalf("selected SSH config = %q", text)
	}
}
