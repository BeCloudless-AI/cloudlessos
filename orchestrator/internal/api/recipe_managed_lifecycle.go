package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

func (s *Server) checkManagedContainerRecipe(job *jobs.Job, recipe localrecipes.Recipe, operationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	fail := func(id, summary string, err error) {
		s.failRecipeCheck(job, operationID, id, summary, err)
	}
	advanced := recipe.Runtime.Adapter == localrecipes.AdvancedContainerAdapter
	boundaryMessage := "Validating the command-free container boundary..."
	if advanced {
		boundaryMessage = "Validating the signed advanced-container command and permissions..."
	}
	job.Progress("checking-sandbox", boundaryMessage, 0, 6)
	if err := localrecipes.ValidateManagedContainerRecipe(recipe); err != nil {
		fail("sandbox", "The constrained runtime contract is invalid", err)
		return
	}
	operation, ok := s.recipeOps.Get(operationID)
	if !ok {
		fail("sandbox", "The recipe operation is unavailable", errors.New("recipe operation disappeared"))
		return
	}
	topology, err := prepareManagedRecipeTopology(ctx, recipe, &operation, true)
	if err != nil {
		fail("sandbox", "The requested Spark topology is unavailable", err)
		return
	}
	containerSpec, err := managedContainerRecipeSpec(recipe, recipe.Engine.Image, "cloudless-recipe-check", operationID, "")
	if err == nil {
		err = engine.ValidateRunSpec(containerSpec)
	}
	if err != nil {
		fail("sandbox", "Cloudless could not synthesize a safe runtime", err)
		return
	}
	boundarySummary := "Cloudless owns the command, mounts, network, privileges, and lifecycle"
	boundaryValues := map[string]string{
		"adapter": recipe.Runtime.Adapter, "readOnlyRoot": "true",
		"capabilities": "none", "hostCommands": "none", "hostMounts": "none",
	}
	if advanced {
		boundarySummary = "The signed recipe owns its in-container command and declared permissions"
		boundaryValues["readOnlyRoot"] = strconv.FormatBool(recipe.Runtime.Container.ReadOnly)
		boundaryValues["capabilities"] = strings.Join(recipe.Runtime.Container.CapAdd, ",")
		boundaryValues["entryPoint"] = recipe.Engine.EntryPoint
	}
	if err := s.recordRecipeCheck(operationID, "sandbox", recipeops.CheckPass, boundarySummary, "", boundaryValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}

	job.Progress("checking-image", "Verifying the pinned Linux image and architecture...", 1, 6)
	image, err := inspectRecipeRegistryImage(ctx, s.eng, recipe.Engine.Image)
	if err != nil {
		fail("image", "The pinned runtime image could not be verified", err)
		return
	}
	if err := s.recordRecipeCheck(operationID, "image", recipeops.CheckPass,
		"The runtime image is immutable and supports this architecture", "", map[string]string{
			"digest": image.Digest, "architecture": image.Architecture,
			"compressedBytes": strconv.FormatInt(image.CompressedBytes, 10),
		}); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}

	job.Progress("checking-capacity", "Measuring model, runtime, and storage requirements...", 2, 6)
	token, _ := s.state.HuggingFaceToken()
	modelBytes, err := recipeModelSetPreflightBytes(ctx, recipe, token)
	if err != nil {
		fail("capacity", "The immutable model revision could not be measured", err)
		return
	}
	availableBytes, measuredAt, err := filesystemAvailableBytes(s.modelVolumePath(ctx))
	if err != nil {
		fail("capacity", "Available model storage could not be measured", err)
		return
	}
	runtimeBytes := image.CompressedBytes * recipeRegistryExpansionFactor
	_, localImageErr := inspectLocalRecipeImage(ctx, s.eng, image.Reference)
	capacity, err := calculateRecipeCapacity(modelBytes, runtimeBytes, 0, availableBytes,
		recipeCachedModelSetReady(ctx, s.eng, recipe), localImageErr == nil)
	if err != nil || capacity.RequiredBytes > capacity.AvailableBytes {
		if err == nil {
			err = fmt.Errorf("requires %d bytes including reserve; %d bytes are available", capacity.RequiredBytes, capacity.AvailableBytes)
		}
		fail("capacity", "This machine does not have enough verified storage", err)
		return
	}
	capacityValues := capacity.values("local.")
	// Capacity.ModelBytes is the amount still missing from disk, which is zero
	// when the immutable model revision is already cached. Preserve the actual
	// model size separately so Run revalidates the same accelerator requirement
	// that Check used.
	capacityValues["local.modelContentBytes"] = strconv.FormatInt(modelBytes, 10)
	capacityValues["local.filesystem"] = measuredAt
	if err := s.recordRecipeCheck(operationID, "capacity", recipeops.CheckPass,
		"The model, image, and safety reserve fit on this machine", "", capacityValues); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}

	job.Progress("checking-accelerator", "Verifying the local NVIDIA runtime and memory...", 3, 6)
	accelerators, err := s.preflightRecipeAccelerators(ctx, recipe, topology.Cluster, topology.Workdir, topology.Env, modelBytes)
	if err != nil {
		fail("accelerators", "The accelerator requirements are not satisfied", err)
		return
	}
	if err := s.recordRecipeCheck(operationID, "accelerators", recipeops.CheckPass,
		"The local accelerator satisfies the recipe contract", "", accelerators); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}

	job.Progress("checking-ports", "Checking the private Cloudless inference port...", 4, 6)
	ports, err := s.preflightRecipePorts(ctx, recipe, topology.Cluster, topology.Workdir, topology.Env)
	if err != nil {
		fail("ports", "The managed inference port is unavailable", err)
		return
	}
	if err := s.recordRecipeCheck(operationID, "ports", recipeops.CheckPass,
		"The private runtime port can be managed safely", "", ports); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}

	job.Progress("binding-evidence", "Binding the verified image and machine evidence...", 5, 6)
	platformFingerprint, err := recipePlatformFingerprint(accelerators)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	clusterFingerprint, err := recipeClusterFingerprint(topology.Cluster)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	operation, err = s.recipeOps.BindPreflight(operationID, image.Digest, platformFingerprint, clusterFingerprint)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhasePrepared, nil); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if operation.Preflight == nil || !operation.Preflight.Launchable {
		s.finishRecipeOperation(job, operationID, errors.New("constrained runtime evidence was not launchable"))
		return
	}
	validatedMessage := "The recipe can run without executing recipe-supplied code."
	if advanced {
		validatedMessage = "The signed advanced container definition passed local preflight."
	}
	job.Progress("validated", validatedMessage, 6, 6)
	job.Succeed("validated")
	s.pruneRecipeOperations()
}

func validateManagedRuntimeImageIdentities(preflightDigest, registryDigest, preparedReference string, localImage engine.ImageInfo) error {
	preflightDigest = strings.ToLower(strings.TrimSpace(preflightDigest))
	registryDigest = strings.ToLower(strings.TrimSpace(registryDigest))
	preparedReference = strings.ToLower(strings.TrimSpace(preparedReference))
	if !recipeImageDigestPattern.MatchString(preflightDigest) || registryDigest != preflightDigest {
		return fmt.Errorf("runtime registry image differs from Check: expected %s, found %s", preflightDigest, registryDigest)
	}
	if !recipeImageDigestPattern.MatchString(preparedReference) || strings.ToLower(strings.TrimSpace(localImage.ID)) != preparedReference {
		return errors.New("prepared runtime content ID is unavailable or changed")
	}
	for _, reference := range localImage.RepoDigests {
		if _, digest, found := strings.Cut(strings.ToLower(strings.TrimSpace(reference)), "@"); found && digest == preflightDigest {
			return nil
		}
	}
	return errors.New("prepared runtime content is not bound to the checked registry digest")
}

func (s *Server) revalidateManagedContainerRecipe(ctx context.Context, recipe localrecipes.Recipe, registryImage string, operation recipeops.Operation) error {
	if operation.Preflight == nil || !operation.Preflight.Runnable {
		return recipeops.ErrPreflightRequired
	}
	image, err := inspectRecipeRegistryImage(ctx, s.eng, registryImage)
	if err != nil {
		return err
	}
	localImage, err := s.eng.InspectImage(ctx, operation.PreparedImageReference)
	if err != nil {
		return fmt.Errorf("inspect prepared runtime content: %w", err)
	}
	if err := validateManagedRuntimeImageIdentities(operation.Preflight.ImageDigest, image.Digest, operation.PreparedImageReference, localImage); err != nil {
		return err
	}
	modelBytes, err := checkedRecipeModelBytes(*operation.Preflight)
	if err != nil {
		return err
	}
	topology, err := prepareManagedRecipeTopology(ctx, recipe, &operation, true)
	if err != nil {
		return err
	}
	accelerators, err := s.preflightRecipeAccelerators(ctx, recipe, topology.Cluster, topology.Workdir, topology.Env, modelBytes)
	if err != nil {
		return err
	}
	platformFingerprint, err := recipePlatformFingerprint(accelerators)
	if err != nil {
		return err
	}
	clusterFingerprint, err := recipeClusterFingerprint(topology.Cluster)
	if err != nil {
		return err
	}
	if !operation.Preflight.Matches(operation.RecipeRevision, platformFingerprint, clusterFingerprint) {
		return errors.New("the checked machine changed; run Check again")
	}
	if _, err := s.preflightRecipePorts(ctx, recipe, topology.Cluster, topology.Workdir, topology.Env); err != nil {
		return err
	}
	return nil
}

func (s *Server) runManagedContainerRecipe(job *jobs.Job, recipe localrecipes.Recipe, operationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(recipe.Runtime.TimeoutMinutes)*time.Minute)
	defer cancel()
	s.registerRecipeJob(recipe.ID, operationID, cancel, job)
	defer s.unregisterRecipeJob(recipe.ID, operationID)
	provision.EngineMu.Lock()
	engineMuHeld := true
	defer func() {
		if engineMuHeld {
			provision.EngineMu.Unlock()
		}
	}()
	operation, ok := s.recipeOps.Get(operationID)
	if !ok || operation.Preflight == nil {
		s.finishRecipeOperation(job, operationID, recipeops.ErrPreflightRequired)
		return
	}
	runtimeName, err := recipeops.RuntimeName(operation)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	topology, err := prepareManagedRecipeTopology(ctx, recipe, &operation, true)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	localNode := localRecipeNodeName()
	for _, resource := range []recipeops.Resource{
		{Kind: "container-set", ID: operationID, Node: localNode},
		{Kind: "image", ID: recipe.Engine.Image, Node: localNode},
		{Kind: "model-cache", ID: recipe.Model.ID + "@" + recipe.Model.Revision, Node: localNode},
		{Kind: "private-port", ID: strconv.Itoa(recipe.Engine.ContainerPort), Node: localNode},
	} {
		if err := s.claimRecipeResource(operationID, resource); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	if recipe.Distributed.Nodes > 1 {
		if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "rendezvous-port", ID: strconv.Itoa(recipe.Distributed.MasterPort), Node: localNode}); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	for _, dependency := range recipe.Model.Dependencies {
		if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "model-cache", ID: dependency.ID + "@" + dependency.Revision, Node: localNode}); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	for _, peer := range topology.Peers {
		for _, resource := range []recipeops.Resource{
			{Kind: "container-set", ID: operationID, Node: peer.Name, Locator: peer.Alias},
			{Kind: "image", ID: recipe.Engine.Image, Node: peer.Name, Locator: peer.Alias},
			{Kind: "model-cache", ID: recipe.Model.ID + "@" + recipe.Model.Revision, Node: peer.Name, Locator: peer.Alias},
			{Kind: "private-port", ID: strconv.Itoa(recipe.Engine.ContainerPort), Node: peer.Name, Locator: peer.Alias},
			{Kind: "rendezvous-port", ID: strconv.Itoa(recipe.Distributed.MasterPort), Node: peer.Name, Locator: peer.Alias},
		} {
			if err := s.claimRecipeResource(operationID, resource); err != nil {
				s.finishRecipeOperation(job, operationID, err)
				return
			}
		}
		for _, dependency := range recipe.Model.Dependencies {
			if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "model-cache", ID: dependency.ID + "@" + dependency.Revision, Node: peer.Name, Locator: peer.Alias}); err != nil {
				s.finishRecipeOperation(job, operationID, err)
				return
			}
		}
	}
	registryImage := recipe.Engine.Image
	job.Progress("pulling-image", "Pulling the exact runtime image verified by Check...", 0, 6)
	if err := s.eng.PullStream(ctx, registryImage, func(line string) {
		job.Progress("pulling-image", line, 0, 6)
	}); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	preparedImage, err := s.eng.InspectImage(ctx, registryImage)
	if err != nil {
		s.finishRecipeOperation(job, operationID, fmt.Errorf("inspect pulled runtime image: %w", err))
		return
	}
	preparedReference, err := managedPreparedImageReference(preparedImage)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	// Keep the registry manifest digest as the signed policy evidence, but use
	// Docker's immutable local content ID for all execution after the pull. The
	// content ID survives save/load on fresh peers; a RepoDigest reference does
	// not necessarily survive and may trigger an unintended second registry pull.
	operation, err = s.recipeOps.BindPreparedImage(operationID, preparedReference, operation.Preflight.ImageDigest)
	if err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	executionImage := operation.PreparedImageReference
	if len(topology.Peers) != 0 {
		job.Progress("syncing-cluster", "Copying the pinned runtime and verified model to the selected Spark...", 1, 6)
		hfToken, _ := s.state.HuggingFaceToken()
		if err := prepareManagedRecipePeers(ctx, s.eng, job, recipe, topology, hfToken, executionImage); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	job.Progress("revalidating", "Rechecking image, GPU, and ports before switching models...", 1, 6)
	if err := s.revalidateManagedContainerRecipe(ctx, recipe, registryImage, operation); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhasePrepared, nil); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseSwitching, nil); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	rollback := func(cause error) {
		_ = s.eng.Remove(context.Background(), runtimeName)
		_ = s.eng.Remove(context.Background(), "cloudless-cluster-engine-proxy")
		if latest, ok := s.recipeOps.Get(operationID); ok {
			cause = errors.Join(cause, s.cleanupInterruptedRecipePeers(context.Background(), latest, recipe))
		}
		if engineMuHeld {
			provision.EngineMu.Unlock()
			engineMuHeld = false
		}
		cause = errors.Join(cause, s.restorePreviousOrMarkUnloaded(job, inferenceRuntimeFromRecipeOperation(operation)))
		s.finishRecipeOperation(job, operationID, cause)
	}
	job.Progress("switching", "The recipe is ready; switching from the current model...", 2, 6)
	if err := s.stopManagedEngines(ctx); err != nil {
		rollback(err)
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseStarting, nil); err != nil {
		rollback(err)
		return
	}
	token, _ := s.state.HuggingFaceToken()
	tokenPath := ""
	if strings.TrimSpace(token) != "" {
		tokenPath = s.state.HuggingFaceTokenPath()
	}
	headIP, headInterface, err := managedRecipeNodeFabric(topology, 0)
	if recipe.Distributed.Nodes <= 1 {
		headIP, headInterface = "", ""
	}
	spec, err := managedContainerRecipeNodeSpec(recipe, executionImage, runtimeName, operationID, tokenPath, 0,
		headIP, headInterface, topology.Env["NCCL_IB_HCA"], topology.Env["MASTER_ADDR"])
	if err != nil {
		rollback(err)
		return
	}
	_ = s.eng.Remove(ctx, runtimeName)
	for index, peer := range topology.Peers {
		rank := index + 1
		peerIP, peerInterface, fabricErr := managedRecipeNodeFabric(topology, rank)
		if fabricErr != nil {
			rollback(fabricErr)
			return
		}
		peerSpec, specErr := managedContainerRecipeNodeSpec(recipe, executionImage, runtimeName, operationID, "", rank,
			peerIP, peerInterface, topology.Env["NCCL_IB_HCA"], topology.Env["MASTER_ADDR"])
		if specErr != nil {
			rollback(specErr)
			return
		}
		job.Progress("starting-workers", fmt.Sprintf("Starting %s as distributed rank %d...", peer.Name, rank), 3, 6)
		containerID, launchErr := launchManagedRecipePeer(ctx, recipe, topology, peer, peerSpec)
		if launchErr != nil {
			rollback(launchErr)
			return
		}
		if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "container", ID: containerID, Node: peer.Name, Locator: peer.Alias}); err != nil {
			rollback(err)
			return
		}
	}
	job.Progress("starting", "Starting the coordinator container from the signed advanced recipe...", 3, 6)
	containerID, err := s.eng.Run(ctx, spec)
	if err != nil {
		rollback(err)
		return
	}
	if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "container", ID: containerID, Node: localNode}); err != nil {
		rollback(err)
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseVerifying, nil); err != nil {
		rollback(err)
		return
	}
	job.ProgressOperation("initializing-engine", "The container is running. Reading its startup progress while the model becomes ready…", "vLLM", 50, 0, 0)
	stopStartupProgress := observeRecipeContainerStartup(ctx, s.eng, job, runtimeName, 3*time.Second)
	healthErr := waitRecipeHealthWithoutUpdates(ctx, job, recipe)
	stopStartupProgress()
	if healthErr != nil {
		rollback(healthErr)
		return
	}
	if err := waitRecipePrivateContract(ctx, job, recipe); err != nil {
		rollback(err)
		return
	}
	job.Progress("connecting", "Connecting Cloudless apps to the verified runtime...", 5, 6)
	proxyImage := s.infraImage(ctx, "socat", catalog.SocatImage)
	if err := s.eng.Pull(ctx, proxyImage); err != nil {
		rollback(err)
		return
	}
	// The private host port is intentionally loopback-only. Route the stable
	// proxy to the managed container over Cloudless's private Docker network.
	proxyTarget := runtimeName
	if recipe.Distributed.Nodes > 1 {
		proxyTarget = "host.docker.internal"
	}
	proxySpec := sparkcluster.ProxySpecTarget(proxyTarget, recipe.Engine.ContainerPort)
	proxySpec.Image = proxyImage
	proxyID, err := s.eng.Run(ctx, proxySpec)
	if err != nil {
		rollback(err)
		return
	}
	if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "container", ID: proxyID, Node: localNode}); err != nil {
		rollback(err)
		return
	}
	if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "stable-port", ID: strconv.Itoa(recipeStablePort), Node: localNode}); err != nil {
		rollback(err)
		return
	}
	if err := waitRecipePromotion(ctx); err != nil {
		rollback(err)
		return
	}
	active := s.state.Get().InferenceRuntime()
	executionMode := "local"
	if recipe.Distributed.Nodes > 1 {
		executionMode = "cluster"
	}
	active.Engine, active.Model, active.ExecutionMode = recipe.Engine.Type, recipe.Model.ID, executionMode
	active.LocalRecipeID, active.EngineUnloaded = recipe.ID, false
	if err := s.state.CommitInferenceRuntime(active); err != nil {
		rollback(err)
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseActive, nil); err != nil {
		rollback(err)
		return
	}
	job.Progress("ready", "Recipe is running through the normal Cloudless API.", 6, 6)
	job.Succeed(localrecipes.CloudlessModelAlias)
	s.pruneRecipeOperations()
}

func (s *Server) stopManagedContainerRecipe(job *jobs.Job, recipe localrecipes.Recipe, operationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	provision.EngineMu.Lock()
	defer provision.EngineMu.Unlock()
	job.Progress("stopping", "Stopping the Cloudless-managed recipe container...", 0, 1)
	if active, ok := s.recipeOps.ActiveForRecipe(recipe.ID); ok {
		if err := s.removeRecoveryContainers(ctx, active); err != nil {
			s.finishRecipeOperation(job, operationID, err)
			return
		}
	}
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	stopped := s.state.Get().InferenceRuntime()
	stopped.LocalRecipeID, stopped.EngineUnloaded = "", true
	if err := s.state.CommitInferenceRuntime(stopped); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.closeActiveRecipeOperation(recipe.ID, nil); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	if err := s.transitionRecipeOperation(operationID, recipeops.PhaseStopped, nil); err != nil {
		s.finishRecipeOperation(job, operationID, err)
		return
	}
	job.Progress("stopped", "Recipe stopped. The model cache remains available.", 1, 1)
	job.Succeed("")
	s.pruneRecipeOperations()
}
