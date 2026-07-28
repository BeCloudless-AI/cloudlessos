package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func TestSparkRunHostsUsesLocalSparkForSingleNodeRecipe(t *testing.T) {
	t.Setenv("CLOUDLESS_SPARKRUN_USER", "owner")
	recipe := localrecipes.Recipe{Distributed: localrecipes.Distributed{Nodes: 1}}
	hosts, username, err := sparkRunHosts(recipe, sparkcluster.State{})
	if err != nil {
		t.Fatal(err)
	}
	if username != "owner" || len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("hosts=%v username=%q", hosts, username)
	}
}

func TestSparkRunHostsRequiresExactHealthyClusterCapacity(t *testing.T) {
	t.Setenv("CLOUDLESS_SPARKRUN_USER", "owner")
	recipe := localrecipes.Recipe{Distributed: localrecipes.Distributed{Nodes: 3}}
	cluster := sparkcluster.State{Configured: true, Healthy: true, Nodes: []sparkcluster.Node{{Host: "10.100.0.2", Username: "owner", Healthy: true}}}
	if _, _, err := sparkRunHosts(recipe, cluster); err == nil {
		t.Fatal("three-node recipe accepted a two-node cluster")
	}
	cluster.Nodes = append(cluster.Nodes, sparkcluster.Node{Host: "10.100.0.3", Username: "owner", Healthy: true})
	hosts, username, err := sparkRunHosts(recipe, cluster)
	if err != nil {
		t.Fatal(err)
	}
	if username != "owner" || len(hosts) != 3 || hosts[1] != "10.100.0.2" || hosts[2] != "10.100.0.3" {
		t.Fatalf("hosts=%v username=%q", hosts, username)
	}
}

func TestSparkRunHostsRejectsMixedClusterUsers(t *testing.T) {
	recipe := localrecipes.Recipe{Distributed: localrecipes.Distributed{Nodes: 3}}
	cluster := sparkcluster.State{Configured: true, Healthy: true, Nodes: []sparkcluster.Node{
		{Host: "10.100.0.2", Username: "owner", Healthy: true},
		{Host: "10.100.0.3", Username: "other", Healthy: true},
	}}
	if _, _, err := sparkRunHosts(recipe, cluster); err == nil {
		t.Fatal("mixed SSH users were accepted")
	}
}
