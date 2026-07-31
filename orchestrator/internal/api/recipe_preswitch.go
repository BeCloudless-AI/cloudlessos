package api

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

// revalidateRecipeBeforeSwitch repeats every identity or availability check
// that can become stale during a long build/download. It runs while the current
// Cloudless model is still available and therefore fails without causing an
// outage.
func (s *Server) revalidateRecipeBeforeSwitch(ctx context.Context, recipe localrecipes.Recipe, operation recipeops.Operation, checkout string, env map[string]string) error {
	if s.recipeRevalidate != nil {
		return s.recipeRevalidate(ctx, recipe, operation, checkout, env)
	}
	if operation.Preflight == nil || !operation.Preflight.Runnable {
		return recipeops.ErrPreflightRequired
	}
	if operation.ResolvedSourceRevision != operation.Preflight.SourceRevision {
		return fmt.Errorf("recipe source changed after Check: checked %s, prepared %s", operation.Preflight.SourceRevision, operation.ResolvedSourceRevision)
	}

	currentCluster, err := sparkcluster.Status(ctx)
	if err != nil && recipe.Distributed.Nodes > 1 {
		return fmt.Errorf("refresh cluster topology before switch: %w", err)
	}
	currentCluster, err = selectRecipeCluster(recipe, currentCluster)
	if err != nil {
		return fmt.Errorf("select checked Spark placement before switch: %w", err)
	}
	clusterFingerprint, err := recipeClusterFingerprint(currentCluster)
	if err != nil {
		return fmt.Errorf("fingerprint current cluster: %w", err)
	}

	modelBytes, err := checkedRecipeModelBytes(*operation.Preflight)
	if err != nil {
		return err
	}
	accelerators, err := s.preflightRecipeAccelerators(ctx, recipe, currentCluster, checkout, env, modelBytes)
	if err != nil {
		return fmt.Errorf("accelerator state changed after Check: %w", err)
	}
	platformFingerprint, err := recipePlatformFingerprint(accelerators)
	if err != nil {
		return fmt.Errorf("fingerprint current platform: %w", err)
	}
	if !operation.Preflight.Matches(operation.RecipeRevision, platformFingerprint, clusterFingerprint) {
		return errors.New("the checked platform or Spark cluster changed during preparation; run Check again")
	}

	var image recipeImagePreflight
	if recipe.Runtime.Lifecycle.Build.Program != "" {
		image, err = inspectLocalRecipeImage(ctx, s.eng, recipe.Engine.Image)
	} else {
		image, err = inspectRecipeRegistryImage(ctx, s.eng, recipe.Engine.Image)
	}
	if err != nil {
		return fmt.Errorf("inspect prepared inference image: %w", err)
	}
	if expected := operation.Preflight.ImageDigest; expected != "" && image.Digest != expected {
		return fmt.Errorf("inference image changed after Check: checked %s, prepared %s", expected, image.Digest)
	}
	if recipe.Distributed.Nodes > 1 && recipe.Runtime.Lifecycle.Build.Program == "" {
		peers, peerErr := recipeDistributionPeers(recipe, currentCluster, env)
		if peerErr != nil {
			return peerErr
		}
		for _, peer := range peers {
			digest, digestErr := inspectPeerRecipeRegistryDigest(ctx, checkout, env, peer, recipe.Engine.Image)
			if digestErr != nil {
				return digestErr
			}
			if digest != image.Digest {
				return fmt.Errorf("%s resolves image %s while the coordinator resolves %s", peer.Name, digest, image.Digest)
			}
		}
	}
	if _, err := s.preflightRecipePorts(ctx, recipe, currentCluster, checkout, env); err != nil {
		return fmt.Errorf("port state changed after Check: %w", err)
	}
	if _, err := preflightRecipeFabric(ctx, recipe, currentCluster, checkout, env, recipe.Engine.Image); err != nil {
		return fmt.Errorf("cluster fabric changed after Check: %w", err)
	}
	workdir := filepath.Join(checkout, recipe.Runtime.WorkingDir)
	if _, err := s.probePreparedRecipeContract(ctx, operation.ID, checkout, workdir, env, recipe, recipe.Engine.Image); err != nil {
		return fmt.Errorf("runtime API contract changed after Check: %w", err)
	}
	jobManifest, err := verifyRecipeModelCache(ctx, s.eng, recipe)
	if err != nil {
		return fmt.Errorf("prepared model integrity changed before switch: %w", err)
	}
	if recipe.Distributed.Nodes > 1 {
		peers, peerErr := recipeDistributionPeers(recipe, currentCluster, env)
		if peerErr != nil {
			return peerErr
		}
		volume, volumeErr := recipeCacheVolume(recipe)
		if volumeErr != nil {
			return volumeErr
		}
		cacheName, mapped := modelCacheName(recipe.Model.ID)
		if !mapped {
			return errors.New("recipe model ID cannot be mapped to a Hugging Face cache")
		}
		relative := filepath.ToSlash(filepath.Join("hub", cacheName))
		for _, peer := range peers {
			digest, digestErr := recipeSnapshotSignature(ctx, checkout, env, &peer, recipe.Engine.Image, volume, relative, recipe.Model.Revision)
			if digestErr != nil || digest != jobManifest.Digest {
				return fmt.Errorf("prepared model integrity on %s changed before switch", peer.Name)
			}
		}
	}
	attestations, err := attestRecipeNodes(ctx, recipe, operation, currentCluster, checkout, env, image.Digest, jobManifest.Digest)
	if err != nil {
		return fmt.Errorf("node consistency attestation failed: %w", err)
	}
	if _, err := s.recipeOps.RecordCheck(operation.ID, recipeNodeAttestationCheck(attestations)); err != nil {
		return fmt.Errorf("persist node consistency attestation: %w", err)
	}
	return nil
}

func checkedRecipeModelBytes(artifact recipeops.PreflightArtifact) (int64, error) {
	for _, check := range artifact.Checks {
		if check.ID != "capacity" {
			continue
		}
		value := check.Values["local.modelBytes"]
		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil || bytes < 0 {
			return 0, errors.New("checked model size evidence is invalid; run Check again")
		}
		return bytes, nil
	}
	return 0, errors.New("checked model size evidence is missing; run Check again")
}
