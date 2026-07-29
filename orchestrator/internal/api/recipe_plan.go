package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

const deepSeekRuntimeEstimateBytes int64 = 23_000_000_000

type recipeLaunchPlan struct {
	RequiresCustomRuntime bool   `json:"requiresCustomRuntime"`
	RuntimeReady          bool   `json:"runtimeReady"`
	RuntimeImage          string `json:"runtimeImage"`
	RuntimeBytes          int64  `json:"runtimeBytes,omitempty"`
	RuntimeEstimateBytes  int64  `json:"runtimeEstimateBytes,omitempty"`
	RuntimeReason         string `json:"runtimeReason"`
	ModelReady            bool   `json:"modelReady"`
	ModelID               string `json:"modelId"`
	BuildOnce             bool   `json:"buildOnce"`
	DownloadOnce          bool   `json:"downloadOnce"`
	Nodes                 int    `json:"nodes"`
}

func recipeRuntimeExplanation(recipe localrecipes.Recipe) string {
	if recipe.ID == localrecipes.DeepSeekDSparkID ||
		(strings.EqualFold(recipe.Source.URL, localrecipes.DeepSeekDSparkSource) && recipe.Source.Revision == localrecipes.DeepSeekDSparkRevision) {
		return "The standard Cloudless vLLM runtime does not contain this recipe's DeepSeek V4 model support, sparse MLA attention, DSpark speculative decoding, and DGX Spark SM120/SM121 kernel patches. The pinned custom runtime is required for this configuration."
	}
	if recipe.Runtime.Lifecycle.Build.Program != "" {
		return "This recipe declares its own pinned inference runtime instead of the standard Cloudless engine. Cloudless must prepare that exact runtime before it can safely launch the model."
	}
	return "This recipe uses its configured inference-engine image."
}

func recipeRuntimeEstimate(recipe localrecipes.Recipe) int64 {
	if recipe.ID == localrecipes.DeepSeekDSparkID ||
		(strings.EqualFold(recipe.Source.URL, localrecipes.DeepSeekDSparkSource) && recipe.Source.Revision == localrecipes.DeepSeekDSparkRevision) {
		return deepSeekRuntimeEstimateBytes
	}
	return 0
}

func recipeCachedModelReady(ctx context.Context, recipe localrecipes.Recipe) bool {
	volume, err := recipeCacheVolume(recipe)
	if err != nil {
		return false
	}
	cacheName, ok := modelCacheName(recipe.Model.ID)
	if !ok || strings.TrimSpace(recipe.Model.Revision) == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, "docker", "volume", "inspect", volume, "--format", "{{.Mountpoint}}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	mountpoint := strings.TrimSpace(string(out))
	if mountpoint == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(mountpoint, "hub", cacheName, "snapshots", recipe.Model.Revision))
	return err == nil && info.IsDir()
}

func buildRecipeLaunchPlan(ctx context.Context, recipe localrecipes.Recipe) recipeLaunchPlan {
	plan := recipeLaunchPlan{
		RequiresCustomRuntime: recipe.Runtime.Lifecycle.Build.Program != "",
		RuntimeImage:          recipe.Engine.Image,
		RuntimeEstimateBytes:  recipeRuntimeEstimate(recipe),
		RuntimeReason:         recipeRuntimeExplanation(recipe),
		ModelID:               recipe.Model.ID,
		BuildOnce:             recipe.Runtime.BuildOnce,
		DownloadOnce:          recipe.Runtime.DownloadOnce,
		Nodes:                 max(1, recipe.Distributed.Nodes),
	}
	if _, size, err := recipeImageMetadata(ctx, "/", nil, recipe.Engine.Image); err == nil {
		plan.RuntimeReady = true
		plan.RuntimeBytes = size
	}
	plan.ModelReady = recipeCachedModelReady(ctx, recipe)
	return plan
}
