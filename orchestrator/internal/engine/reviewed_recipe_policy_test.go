package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestReviewedRecipePolicyRejectsForgedAndEditedOperations(t *testing.T) {
	stateDir := t.TempDir()
	runtimeRoot := t.TempDir()
	recipes := localrecipes.New(stateDir)
	recipe, err := recipes.Import(localrecipes.DeepSeekDSparkSource)
	if err != nil {
		t.Fatal(err)
	}
	operations, err := recipeops.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := operations.BeginUnique(recipeops.KindCheck, recipe, recipeops.PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(stateDir, "recipe-checks", operation.ID)
	if err := os.MkdirAll(checkout, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := ReviewedRecipePolicy{StateDir: stateDir, RuntimeRoot: runtimeRoot}

	tests := []struct {
		name string
		spec ReviewedRecipeDockerSpec
		want string
	}{
		{
			name: "unknown operation",
			spec: ReviewedRecipeDockerSpec{
				OperationID:    "rop-00000000000000000000000000000000",
				RecipeRevision: operation.RecipeRevision,
				WorkingDir:     checkout,
				Args:           []string{"compose", "ps"},
			},
			want: "not registered",
		},
		{
			name: "forged revision",
			spec: ReviewedRecipeDockerSpec{
				OperationID:    operation.ID,
				RecipeRevision: "sha256:" + strings.Repeat("0", 64),
				WorkingDir:     checkout,
				Args:           []string{"compose", "ps"},
			},
			want: "revision does not match",
		},
		{
			name: "foreign working directory",
			spec: ReviewedRecipeDockerSpec{
				OperationID:    operation.ID,
				RecipeRevision: operation.RecipeRevision,
				WorkingDir:     t.TempDir(),
				Args:           []string{"compose", "ps"},
			},
			want: "working directory",
		},
		{
			name: "missing signed checkout repository",
			spec: ReviewedRecipeDockerSpec{
				OperationID:    operation.ID,
				RecipeRevision: operation.RecipeRevision,
				WorkingDir:     checkout,
				Args:           []string{"compose", "ps"},
			},
			want: "resolve reviewed recipe checkout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := policy.Authorize(tt.spec); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Authorize() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestReviewedRecipeDockerArgumentsFailClosed(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "models-cache")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDLESS_MODEL_CACHE", cacheRoot)
	recipeStore := localrecipes.New(t.TempDir())
	recipe, err := recipeStore.Import(localrecipes.DeepSeekDSparkSource)
	if err != nil {
		t.Fatal(err)
	}
	operation := recipeops.Operation{
		ID:                     "rop-00112233445566778899aabbccddeeff",
		RecipeSnapshot:         recipe,
		PreparedImageReference: "sha256:" + strings.Repeat("a", 64),
	}
	checkout := filepath.Join(t.TempDir(), recipe.ID)

	allowed := [][]string{
		{"compose", "-f", filepath.Join(checkout, "docker-compose.dspark.yml"), "up", "-d"},
		{"run", "--rm", "-v", "cloudless-hf:/cache/huggingface", recipe.Engine.Image, "true"},
		{"run", "--rm", "-v", cacheRoot + ":/cache/huggingface", recipe.Engine.Image, "true"},
		{"volume", "create", "cloudless-hf"},
	}
	for _, args := range allowed {
		if err := authorizeReviewedDockerArgs(operation, checkout, args); err != nil {
			t.Fatalf("expected %q to be admitted: %v", args, err)
		}
	}

	rejected := []struct {
		args []string
		want string
	}{
		{[]string{"run", "--privileged", recipe.Engine.Image}, "forbidden"},
		{[]string{"run", "-v", "/:/host", recipe.Engine.Image}, "model storage"},
		{[]string{"run", "-v", "/run/docker.sock:/run/docker.sock", recipe.Engine.Image}, "forbidden"},
		{[]string{"run", "attacker.invalid/root:latest"}, "authenticated operation"},
		{[]string{"compose", "-f", "/tmp/attacker.yml", "up"}, "escapes"},
		{[]string{"system", "prune", "-af"}, "not admitted"},
	}
	for _, tt := range rejected {
		if err := authorizeReviewedDockerArgs(operation, checkout, tt.args); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("authorizeReviewedDockerArgs(%q) = %v, want %q", tt.args, err, tt.want)
		}
	}
}

func TestReviewedComposePlanRejectsRootEquivalentAccess(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "models-cache")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDLESS_MODEL_CACHE", cacheRoot)
	recipeStore := localrecipes.New(t.TempDir())
	recipe, err := recipeStore.Import(localrecipes.DeepSeekDSparkSource)
	if err != nil {
		t.Fatal(err)
	}
	operation := recipeops.Operation{RecipeSnapshot: recipe}
	valid := `{"services":{"engine":{"image":"` + recipe.Engine.Image + `","network_mode":"host","ipc":"host","devices":[{"source":"/dev/infiniband","target":"/dev/infiniband"}],"volumes":[{"type":"volume","source":"cloudless-hf","target":"/cache"}]}}}`
	if err := validateReviewedComposePlan([]byte(valid), operation); err != nil {
		t.Fatalf("valid reviewed Compose plan rejected: %v", err)
	}
	hostCache := `{"services":{"engine":{"image":"` + recipe.Engine.Image + `","volumes":[{"type":"bind","source":"` + cacheRoot + `","target":"/cache"}]}}}`
	if err := validateReviewedComposePlan([]byte(hostCache), operation); err != nil {
		t.Fatalf("Cloudless host cache rejected: %v", err)
	}
	rejected := []string{
		`{"services":{"engine":{"image":"` + recipe.Engine.Image + `","privileged":true}}}`,
		`{"services":{"engine":{"image":"` + recipe.Engine.Image + `","volumes":[{"type":"bind","source":"/","target":"/host"}]}}}`,
		`{"services":{"engine":{"image":"` + recipe.Engine.Image + `","devices":[{"source":"/dev/sda","target":"/dev/sda"}]}}}`,
		`{"services":{"engine":{"image":"attacker.invalid/root:latest"}}}`,
		`{"services":{"engine":{"image":"` + recipe.Engine.Image + `","cap_add":["SYS_ADMIN"]}}}`,
	}
	for _, payload := range rejected {
		if err := validateReviewedComposePlan([]byte(payload), operation); err == nil {
			t.Fatalf("unsafe reviewed Compose plan was accepted: %s", payload)
		}
	}
}
