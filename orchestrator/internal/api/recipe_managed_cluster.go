package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

type managedRecipeTopology struct {
	Cluster sparkcluster.State
	Env     map[string]string
	Workdir string
	Peers   []recipePeer
}

func prepareManagedRecipeTopology(ctx context.Context, recipe localrecipes.Recipe, operation *recipeops.Operation, requireHealthy bool) (managedRecipeTopology, error) {
	if recipe.Distributed.Nodes <= 1 {
		return managedRecipeTopology{Cluster: sparkcluster.State{NodeCount: 1, ComputeNodeCount: 1}}, nil
	}
	cluster, err := sparkcluster.Status(ctx)
	if err != nil {
		return managedRecipeTopology{}, err
	}
	cluster, err = selectRecipeClusterWithHealth(recipe, cluster, requireHealthy)
	if err != nil {
		return managedRecipeTopology{}, err
	}
	checkout := recipeCheckout(recipe)
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		return managedRecipeTopology{}, err
	}
	env, workdir, err := writeRecipeRuntime(recipe, checkout, cluster, requireHealthy, operation)
	if err != nil {
		return managedRecipeTopology{}, err
	}
	peers, err := recipeDistributionPeers(recipe, cluster, env)
	if err != nil {
		return managedRecipeTopology{}, err
	}
	return managedRecipeTopology{Cluster: cluster, Env: env, Workdir: workdir, Peers: peers}, nil
}

func managedContainerRecipeNodeSpec(recipe localrecipes.Recipe, image, name, operationID, tokenPath string, rank int, hostIP, iface, hca, masterAddress string) (engine.RunSpec, error) {
	spec, err := managedContainerRecipeSpec(recipe, image, name, operationID, tokenPath)
	if err != nil {
		return engine.RunSpec{}, err
	}
	if recipe.Distributed.Nodes <= 1 {
		return spec, nil
	}
	if rank < 0 || rank >= recipe.Distributed.Nodes || strings.TrimSpace(hostIP) == "" || strings.TrimSpace(iface) == "" || strings.TrimSpace(masterAddress) == "" {
		return engine.RunSpec{}, errors.New("distributed container rank and fabric identity are incomplete")
	}
	values := map[string]string{
		"NODE_RANK": rankString(rank), "MASTER_ADDR": masterAddress,
		"MASTER_PORT": strconv.Itoa(recipe.Distributed.MasterPort), "VLLM_HOST_IP": hostIP,
		"NCCL_SOCKET_IFNAME": iface, "TP_SOCKET_IFNAME": iface, "GLOO_SOCKET_IFNAME": iface,
		"NCCL_IB_HCA": hca, "NCCL_IB_GID_INDEX": strconv.Itoa(recipe.Distributed.IBGIDIndex),
		"HEADLESS": "",
	}
	if rank > 0 {
		values["HEADLESS"] = "1"
	}
	for key, value := range values {
		spec.Env[key] = value
	}
	return spec, nil
}

func rankString(rank int) string { return strconv.Itoa(rank) }

func managedRecipeNodeFabric(topology managedRecipeTopology, rank int) (hostIP, iface string, err error) {
	if rank == 0 {
		if len(topology.Cluster.LocalIPs) != 0 {
			hostIP = topology.Cluster.LocalIPs[0]
		}
		if len(topology.Cluster.LocalLinks) != 0 {
			iface = topology.Cluster.LocalLinks[0]
		}
	} else if rank-1 < len(topology.Cluster.Nodes) {
		node := topology.Cluster.Nodes[rank-1]
		if len(node.IPs) != 0 {
			hostIP = node.IPs[0]
		}
		if len(node.Links) != 0 {
			iface = node.Links[0]
		}
	}
	if hostIP == "" || iface == "" {
		return "", "", fmt.Errorf("Spark rank %d is missing its fabric address or interface", rank)
	}
	return hostIP, iface, nil
}

func launchManagedRecipePeer(ctx context.Context, recipe localrecipes.Recipe, topology managedRecipeTopology, peer recipePeer, spec engine.RunSpec) (string, error) {
	args, err := engine.RunArguments(spec)
	if err != nil {
		return "", err
	}
	_, _ = recipeCommandOutput(recipeSSHCommand(ctx, topology.Workdir, topology.Env, peer, "docker", "rm", "-f", spec.Name))
	return recipeCommandOutput(recipeSSHCommand(ctx, topology.Workdir, topology.Env, peer, append([]string{"docker"}, args...)...))
}

func (s *Server) prepareManagedRecipePeers(ctx context.Context, runtime engine.Engine, job *jobs.Job, recipe localrecipes.Recipe, topology managedRecipeTopology, token, executionImage string) error {
	if len(topology.Peers) == 0 {
		return nil
	}
	executionRecipe := recipe
	executionRecipe.Engine.Image = executionImage
	models := recipeModelSet(executionRecipe)
	manifests := make([]recipeArtifactManifest, len(models))
	for index, modelRecipe := range models {
		job.Progress("verifying-existing-model", "Verifying the installed "+modelRecipe.Model.ID+" snapshot before peer transfer...", -1, -1)
		lastUpdate := time.Time{}
		manifest, err := verifyOrCertifyInstalledRecipeModelCache(ctx, runtime, modelRecipe, token, func(done, total int64) {
			if time.Since(lastUpdate) < time.Second && done < total {
				return
			}
			job.ProgressBytes("verifying-existing-model", "Verifying "+modelRecipe.Model.ID+" â€” "+formatDownloadProgress(done, total), done, total)
			lastUpdate = time.Now()
		})
		if err != nil {
			return fmt.Errorf("verify installed model %s before peer distribution: %w", modelRecipe.Model.ID, err)
		}
		manifests[index] = manifest
	}
	if err := distributeRecipeImage(ctx, runtime, job, executionRecipe, topology.Workdir, topology.Env, topology.Peers); err != nil {
		return err
	}
	sharedStorage, err := s.sharedModelStorageForRecipe(ctx, recipe)
	if err != nil {
		return err
	}
	for index, modelRecipe := range models {
		if _, err := distributeRecipeModel(ctx, runtime, job, modelRecipe, topology.Workdir, topology.Env, topology.Peers, manifests[index], sharedStorage); err != nil {
			return err
		}
	}
	return nil
}
