package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

var errRecipeRunPreflightChanged = errors.New("the checked machine or Spark cluster changed; run Check again")

// validateRecipeRunPreflight recomputes the mutable environment identity bound
// by Check. It deliberately runs before image pulls, model transfers, resource
// claims, or runtime changes. The later pre-switch validation remains required
// to catch a machine change that races with preparation.
func (s *Server) validateRecipeRunPreflight(ctx context.Context, recipe localrecipes.Recipe, operation recipeops.Operation) error {
	if s.recipeRunPreflight != nil {
		return s.recipeRunPreflight(ctx, recipe, operation)
	}
	// The lifecycle failure matrix supplies its own complete deterministic
	// revalidator. Production never sets this seam.
	if s.recipeRevalidate != nil {
		return nil
	}
	if operation.Preflight == nil || !operation.Preflight.Runnable {
		return recipeops.ErrPreflightRequired
	}

	cluster, err := currentRecipeRunCluster(ctx, recipe)
	if err != nil {
		return fmt.Errorf("read the current Spark placement: %w", err)
	}
	clusterFingerprint, err := recipeClusterFingerprint(cluster)
	if err != nil {
		return fmt.Errorf("fingerprint the current Spark placement: %w", err)
	}
	if operation.Preflight.ClusterFingerprint != clusterFingerprint {
		return errRecipeRunPreflightChanged
	}
	if _, err := s.sharedModelStorageForRecipe(ctx, recipe); err != nil {
		return err
	}

	// Container recipes have no source checkout prerequisite, so their complete
	// accelerator identity can also be revalidated before the image is pulled.
	// Source-backed recipes repeat this hardware check before switching, after
	// their exact checked-out source has restored the restricted SSH context.
	if !localrecipes.IsContainerAdapter(recipe.Runtime.Adapter) {
		return nil
	}
	topology, err := prepareManagedRecipeTopology(ctx, recipe, &operation, true)
	if err != nil {
		return fmt.Errorf("prepare the checked Spark topology: %w", err)
	}
	modelBytes, err := checkedRecipeModelBytes(*operation.Preflight)
	if err != nil {
		return err
	}
	accelerators, err := s.preflightRecipeAccelerators(ctx, recipe, topology.Cluster, topology.Workdir, topology.Env, modelBytes)
	if err != nil {
		return fmt.Errorf("the checked accelerator environment changed: %w", err)
	}
	platformFingerprint, err := recipePlatformFingerprint(accelerators)
	if err != nil {
		return fmt.Errorf("fingerprint the current accelerator environment: %w", err)
	}
	clusterFingerprint, err = recipeClusterFingerprint(topology.Cluster)
	if err != nil {
		return fmt.Errorf("fingerprint the current Spark topology: %w", err)
	}
	if !operation.Preflight.Matches(operation.RecipeRevision, platformFingerprint, clusterFingerprint) {
		return errRecipeRunPreflightChanged
	}
	return nil
}

func currentRecipeRunCluster(ctx context.Context, recipe localrecipes.Recipe) (sparkcluster.State, error) {
	if localrecipes.IsContainerAdapter(recipe.Runtime.Adapter) && recipe.Distributed.Nodes <= 1 {
		return sparkcluster.State{NodeCount: 1, ComputeNodeCount: 1}, nil
	}
	cluster, err := sparkcluster.Status(ctx)
	if err != nil && recipe.Distributed.Nodes > 1 {
		return sparkcluster.State{}, err
	}
	return selectRecipeCluster(recipe, cluster)
}
