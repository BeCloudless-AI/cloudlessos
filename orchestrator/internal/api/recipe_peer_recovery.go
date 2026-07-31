package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

type recipePeerRecoveryTarget struct {
	Alias           string
	Name            string
	ComposeProjects []string
}

func recipePeerRecoveryTargets(operation recipeops.Operation, localNode string) []recipePeerRecoveryTarget {
	byAlias := make(map[string]*recipePeerRecoveryTarget)
	for _, resource := range operation.Resources {
		if resource.Node == "" || resource.Node == localNode || strings.TrimSpace(resource.Locator) == "" {
			continue
		}
		target := byAlias[resource.Locator]
		if target == nil {
			target = &recipePeerRecoveryTarget{Alias: resource.Locator, Name: resource.Node}
			byAlias[resource.Locator] = target
		}
		if resource.Kind == "compose-project" && !containsString(target.ComposeProjects, resource.ID) {
			target.ComposeProjects = append(target.ComposeProjects, resource.ID)
		}
	}
	targets := make([]recipePeerRecoveryTarget, 0, len(byAlias))
	for _, target := range byAlias {
		sort.Strings(target.ComposeProjects)
		targets = append(targets, *target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Alias < targets[j].Alias })
	return targets
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// cleanupInterruptedRecipePeers attempts every journaled peer independently.
// An unreachable node remains in the operation inventory and therefore blocks
// conflicting launches; a failure on one peer never prevents cleanup attempts
// on the others.
func (s *Server) cleanupInterruptedRecipePeers(ctx context.Context, operation recipeops.Operation, recipe localrecipes.Recipe) error {
	targets := recipePeerRecoveryTargets(operation, localRecipeNodeName())
	if len(targets) == 0 {
		return nil
	}
	home := filepath.Join(recipeCheckout(recipe), ".cloudless-home")
	env := map[string]string{"HOME": home, "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	const cleanup = `set -eu
operation="$1"
shift
ids="$(docker ps -aq --filter "label=cloudless.recipe.operation=$operation" 2>/dev/null || true)"
for project in "$@"; do
  project_ids="$(docker ps -aq --filter "label=com.docker.compose.project=$project" 2>/dev/null || true)"
  ids="$ids $project_ids"
done
for id in $ids; do docker rm -f "$id" >/dev/null 2>&1 || true; done
for environment in /proc/[0-9]*/environ; do
  if tr '\000' '\n' < "$environment" 2>/dev/null | grep -Fxq "CLOUDLESS_RECIPE_OPERATION_ID=$operation"; then
    pid="${environment#/proc/}"; pid="${pid%/environ}"; kill -TERM "$pid" 2>/dev/null || true
  fi
done`
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(targets))
	var group sync.WaitGroup
	for _, target := range targets {
		target := target
		group.Add(1)
		go func() {
			defer group.Done()
			peerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			args := []string{"/bin/sh", "-c", cleanup, "cloudless-peer-recovery", operation.ID}
			args = append(args, target.ComposeProjects...)
			_, err := recipeCommandOutput(recipeSSHCommand(peerCtx, recipeCheckout(recipe), env,
				recipePeer{Alias: target.Alias, Name: target.Name}, args...))
			results <- result{name: target.Name, err: err}
		}()
	}
	group.Wait()
	close(results)
	var combined error
	for result := range results {
		if result.err != nil {
			combined = errors.Join(combined, fmt.Errorf("cleanup %s: %w", result.name, result.err))
		}
	}
	return combined
}
