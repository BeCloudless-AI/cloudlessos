package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

// restartActiveRecipeAfterBoot restores a previously active recipe in a
// coordinator-first order. Recipe containers use restart:no, so topology is
// checked before any node allocates model memory.
func (s *Server) restartActiveRecipeAfterBoot(ctx context.Context, job *jobs.Job, operation recipeops.Operation, recipe localrecipes.Recipe) error {
	if job == nil {
		job = jobs.NewManager().Create("recipe:" + recipe.ID + ":boot-recovery")
	}
	provision.EngineMu.Lock()
	defer provision.EngineMu.Unlock()
	if localrecipes.IsContainerAdapter(recipe.Runtime.Adapter) {
		return s.restartManagedContainerRecipeAfterBoot(ctx, job, operation, recipe)
	}
	cluster, err := sparkcluster.Status(ctx)
	if err != nil && recipe.Distributed.Nodes > 1 {
		return fmt.Errorf("reconcile cluster topology before recipe restart: %w", err)
	}
	cluster, err = selectRecipeCluster(recipe, cluster)
	if err != nil {
		return fmt.Errorf("restore the recipe's selected Spark placement: %w", err)
	}
	if recipe.Distributed.Nodes > 1 && (!cluster.Healthy || !cluster.WorkerReady || cluster.NodeCount != recipe.Distributed.Nodes) {
		return fmt.Errorf("recipe restart requires exactly %d healthy Sparks", recipe.Distributed.Nodes)
	}
	env, workdir, err := writeRecipeRuntime(recipe, recipeCheckout(recipe), cluster, true, &operation)
	if err != nil {
		return err
	}
	job.Progress("recovering", "Topology is healthy. Starting the recipe coordinator and workers in order...", -1, -1)
	if err := runConfiguredRecipeCommand(ctx, job, "recovering", "Restart inference after boot", workdir, env, recipe, recipe.Runtime.Lifecycle.Start); err != nil {
		return err
	}
	if err := waitRecipeHealth(ctx, job, recipe); err != nil {
		return err
	}
	if err := waitRecipePrivateContract(ctx, job, recipe); err != nil {
		return err
	}
	if proxy, findErr := s.eng.Find(ctx, "cloudless-cluster-engine-proxy"); findErr != nil {
		return findErr
	} else if proxy != nil {
		_ = s.eng.Remove(ctx, proxy.Name)
	}
	proxyImage := s.infraImage(ctx, "socat", catalog.SocatImage)
	if err := s.eng.Pull(ctx, proxyImage); err != nil {
		return err
	}
	spec := sparkcluster.ProxySpecTarget(recipe.Engine.ProxyHost, recipe.Engine.ContainerPort)
	spec.Image = proxyImage
	proxyID, err := s.eng.Run(ctx, spec)
	if err != nil {
		return err
	}
	if err := s.claimRecipeResource(operation.ID, recipeops.Resource{Kind: "container", ID: proxyID, Node: localRecipeNodeName()}); err != nil {
		_ = s.eng.Remove(ctx, spec.Name)
		return err
	}
	if err := waitRecipePromotion(ctx); err != nil {
		_ = s.eng.Remove(ctx, spec.Name)
		return err
	}
	return nil
}

func (s *Server) restartManagedContainerRecipeAfterBoot(ctx context.Context, job *jobs.Job, operation recipeops.Operation, recipe localrecipes.Recipe) error {
	if err := localrecipes.ValidateManagedContainerRecipe(recipe); err != nil {
		return err
	}
	runtimeName, err := recipeops.RuntimeName(operation)
	if err != nil {
		return err
	}
	image := operation.PreparedImageReference
	if image == "" {
		image = recipe.Engine.Image
	}
	container, err := s.eng.Find(ctx, runtimeName)
	if err != nil {
		return err
	}
	if container == nil || container.State != "running" {
		if container != nil {
			if err := s.eng.Remove(ctx, runtimeName); err != nil {
				return err
			}
		}
		token, _ := s.state.HuggingFaceToken()
		tokenPath := ""
		if strings.TrimSpace(token) != "" {
			tokenPath = s.state.HuggingFaceTokenPath()
		}
		spec, err := managedContainerRecipeSpec(recipe, image, runtimeName, operation.ID, tokenPath)
		if err != nil {
			return err
		}
		job.Progress("recovering", "Restoring the constrained recipe container from its pinned image...", -1, -1)
		containerID, err := s.eng.Run(ctx, spec)
		if err != nil {
			return err
		}
		if err := s.claimRecipeResource(operation.ID, recipeops.Resource{Kind: "container", ID: containerID, Node: localRecipeNodeName()}); err != nil {
			_ = s.eng.Remove(ctx, runtimeName)
			return err
		}
	}
	if err := waitRecipeHealth(ctx, job, recipe); err != nil {
		return err
	}
	if err := waitRecipePrivateContract(ctx, job, recipe); err != nil {
		return err
	}
	if proxy, findErr := s.eng.Find(ctx, "cloudless-cluster-engine-proxy"); findErr != nil {
		return findErr
	} else if proxy != nil {
		_ = s.eng.Remove(ctx, proxy.Name)
	}
	proxyImage := s.infraImage(ctx, "socat", catalog.SocatImage)
	if err := s.eng.Pull(ctx, proxyImage); err != nil {
		return err
	}
	spec := sparkcluster.ProxySpecTarget(runtimeName, recipe.Engine.ContainerPort)
	spec.Image = proxyImage
	proxyID, err := s.eng.Run(ctx, spec)
	if err != nil {
		return err
	}
	if err := s.claimRecipeResource(operation.ID, recipeops.Resource{Kind: "container", ID: proxyID, Node: localRecipeNodeName()}); err != nil {
		_ = s.eng.Remove(ctx, spec.Name)
		return err
	}
	if err := waitRecipePromotion(ctx); err != nil {
		_ = s.eng.Remove(ctx, spec.Name)
		return err
	}
	return nil
}
