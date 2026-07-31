package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func selectRecipeCluster(recipe localrecipes.Recipe, cluster sparkcluster.State) (sparkcluster.State, error) {
	return selectRecipeClusterWithHealth(recipe, cluster, true)
}

func selectRecipeClusterWithHealth(recipe localrecipes.Recipe, cluster sparkcluster.State, requireHealthy bool) (sparkcluster.State, error) {
	required := max(0, recipe.Distributed.Nodes-1)
	if required == 0 {
		selected := cluster
		selected.Nodes = nil
		selected.NodeCount = 1
		selected.WorkerReady = true
		selected.Healthy = true
		return selected, nil
	}
	if len(cluster.Nodes) < required {
		return sparkcluster.State{}, fmt.Errorf("recipe requires %d worker Sparks but only %d are enrolled", required, len(cluster.Nodes))
	}
	selectors := recipe.Distributed.SelectedNodes
	if len(selectors) > 0 && len(selectors) != required {
		return sparkcluster.State{}, fmt.Errorf("recipe requires exactly %d selected worker Sparks, found %d", required, len(selectors))
	}
	selectedNodes := make([]sparkcluster.Node, 0, required)
	used := make(map[int]struct{})
	if len(selectors) > 0 {
		for _, selector := range selectors {
			matched := -1
			for index, node := range cluster.Nodes {
				if selector == node.Fingerprint || strings.EqualFold(selector, node.Name) || strings.EqualFold(selector, node.Host) {
					if matched >= 0 {
						return sparkcluster.State{}, fmt.Errorf("worker selector %q is ambiguous", selector)
					}
					matched = index
				}
			}
			if matched < 0 {
				return sparkcluster.State{}, fmt.Errorf("selected worker %q is not enrolled", selector)
			}
			if _, exists := used[matched]; exists {
				return sparkcluster.State{}, fmt.Errorf("selected worker %q refers to a Spark that is already selected", selector)
			}
			used[matched] = struct{}{}
			selectedNodes = append(selectedNodes, cluster.Nodes[matched])
		}
	} else {
		candidates := append([]sparkcluster.Node(nil), cluster.Nodes...)
		sort.SliceStable(candidates, func(i, j int) bool {
			left, right := candidates[i].Fingerprint, candidates[j].Fingerprint
			if left == right {
				left, right = candidates[i].Host, candidates[j].Host
			}
			return left < right
		})
		for _, node := range candidates {
			if !requireHealthy || (node.Healthy && node.WorkerReady) {
				selectedNodes = append(selectedNodes, node)
				if len(selectedNodes) == required {
					break
				}
			}
		}
		if len(selectedNodes) != required {
			return sparkcluster.State{}, errors.New("not enough healthy worker Sparks are available for automatic placement")
		}
	}
	selected := cluster
	selected.Nodes = selectedNodes
	selected.NodeCount = 1 + len(selectedNodes)
	selected.WorkerReady, selected.Healthy = true, cluster.Configured
	for _, node := range selectedNodes {
		selected.WorkerReady = selected.WorkerReady && node.WorkerReady
		selected.Healthy = selected.Healthy && node.Healthy && node.WorkerReady
	}
	if len(selectedNodes) > 0 {
		first := selectedNodes[0]
		selected.PeerName, selected.PeerHost, selected.Username = first.Name, first.Host, first.Username
		selected.Fingerprint = first.Fingerprint
		selected.PeerLinks, selected.PeerIPs = append([]string(nil), first.Links...), append([]string(nil), first.IPs...)
	}
	return selected, nil
}

func recipeClusterNodeIdentities(cluster sparkcluster.State) []string {
	identities := make([]string, 0, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		identity := strings.TrimSpace(node.Fingerprint)
		if identity == "" {
			identity = strings.TrimSpace(node.Name)
		}
		if identity == "" {
			identity = strings.TrimSpace(node.Host)
		}
		identities = append(identities, identity)
	}
	return identities
}
