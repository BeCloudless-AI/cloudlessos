package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

// unreachableHealth points the probes at a closed port so every attempt fails,
// leaving the liveness guard as the only thing that can end the wait early.
func unreachableHealth() localrecipes.Health {
	return localrecipes.Health{
		Scheme: "http", Host: "127.0.0.1", Port: 1, Path: "/health",
		TimeoutSeconds: 600, IntervalSeconds: 1,
	}
}

func TestHealthWaitFailsFastWhenTheContainerExits(t *testing.T) {
	recipe := localrecipes.Recipe{Health: unreachableHealth()}
	job := jobs.NewManager().Create("recipe:test")
	exited := errors.New("the recipe container stopped before it became ready (Exited (1): boom)")
	start := time.Now()
	err := waitRecipeHealthWatching(context.Background(), job, recipe, false, func(context.Context) error {
		return exited
	})
	if !errors.Is(err, exited) {
		t.Fatalf("health wait error = %v, want the container-exit reason", err)
	}
	// The recipe's own health timeout is 600s. A dead container must not be
	// reported as still starting for that long.
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Fatalf("health wait took %s; it waited out the health timeout", elapsed)
	}
}

func TestPrivateContractWaitFailsFastWhenTheContainerExits(t *testing.T) {
	recipe := localrecipes.Recipe{Health: unreachableHealth()}
	recipe.Engine.APIPath = "/v1"
	job := jobs.NewManager().Create("recipe:test")
	exited := errors.New("the recipe container stopped before it became ready (Exited (1))")
	err := waitRecipePrivateContractWatching(context.Background(), job, recipe, func(context.Context) error {
		return exited
	})
	if !errors.Is(err, exited) {
		t.Fatalf("contract wait error = %v, want the container-exit reason", err)
	}
}

func TestWaitsKeepPollingWhileTheContainerLives(t *testing.T) {
	// A transient inspect failure returns nil, so the wait must fall through to
	// its existing deadline and cancellation behavior unchanged.
	recipe := localrecipes.Recipe{Health: unreachableHealth()}
	job := jobs.NewManager().Create("recipe:test")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	calls := 0
	err := waitRecipeHealthWatching(ctx, job, recipe, false, func(context.Context) error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("health wait error = %v, want context.Canceled", err)
	}
	if calls == 0 {
		t.Fatal("liveness probe was never consulted")
	}
}

func TestNilLivenessPreservesExistingBehavior(t *testing.T) {
	recipe := localrecipes.Recipe{Health: unreachableHealth()}
	job := jobs.NewManager().Create("recipe:test")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	if err := waitRecipeHealthWithUpdates(ctx, job, recipe, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("health wait error = %v, want context.Canceled", err)
	}
}

func TestRecipeContainerFailureLineKeepsTheException(t *testing.T) {
	tail := strings.Join([]string{
		"(APIServer pid=1) Traceback (most recent call last):",
		`(APIServer pid=1)   File "/usr/local/lib/python3.12/dist-packages/vllm/x.py", line 1, in run`,
		"(APIServer pid=1)     engine_args = cls(",
		"(APIServer pid=1)                   ^^^^",
		"huggingface_hub.errors.LocalEntryNotFoundError: Cannot find an appropriate cached snapshot folder",
		"",
	}, "\n")
	line := recipeContainerFailureLine(tail)
	if !strings.Contains(line, "LocalEntryNotFoundError") {
		t.Fatalf("failure line = %q, want the final exception", line)
	}
	if strings.Contains(line, "File \"") || strings.Contains(line, "Traceback") {
		t.Fatalf("failure line kept a stack frame: %q", line)
	}
	if recipeContainerFailureLine("   \n\n  ") != "" {
		t.Error("blank output should produce no failure line")
	}
	long := recipeContainerFailureLine("Error: " + strings.Repeat("x", 500))
	if len([]rune(long)) > 300 {
		t.Errorf("failure line was not truncated: %d runes", len([]rune(long)))
	}
}

func advancedRecipe(modelRevision string, command ...string) localrecipes.Recipe {
	recipe := localrecipes.Recipe{}
	recipe.Runtime.Adapter = localrecipes.AdvancedContainerAdapter
	recipe.Model.Revision = modelRevision
	recipe.Engine.Command = command
	return recipe
}

func TestRecipeCommandRevisionsFindsEveryPin(t *testing.T) {
	// The real shape: a single -lc script with a line-continued vllm command.
	script := "set -euo pipefail\nexec vllm serve unsloth/Model \\\n  --revision abc123 \\\n  --served-model-name cloudless\n"
	got := recipeCommandRevisions(advancedRecipe("abc123", "-lc", script))
	if len(got) != 1 || got[0] != "abc123" {
		t.Fatalf("revisions = %v, want [abc123]", got)
	}
	equals := recipeCommandRevisions(advancedRecipe("abc123", "-lc", "vllm serve m --revision=def456"))
	if len(equals) != 1 || equals[0] != "def456" {
		t.Fatalf("--revision=value form not parsed: %v", equals)
	}
	if got := recipeCommandRevisions(advancedRecipe("abc123", "-lc", "vllm serve m")); len(got) != 0 {
		t.Fatalf("unpinned command reported revisions: %v", got)
	}
}

func TestRecipeRevisionAgreementRejectsDisagreement(t *testing.T) {
	// The failure this check exists to prevent: the operator updated
	// model.revision but not the command the container actually runs.
	stale := advancedRecipe("9e3d73c7", "-lc", "vllm serve m --revision 60e813d4")
	values, err := recipeRevisionAgreement(stale)
	if err == nil {
		t.Fatal("disagreeing revisions were accepted")
	}
	for _, want := range []string{"60e813d4", "9e3d73c7"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
	if values["modelRevision"] != "9e3d73c7" || !strings.Contains(values["commandRevisions"], "60e813d4") {
		t.Errorf("evidence values = %v", values)
	}

	if _, err := recipeRevisionAgreement(advancedRecipe("ABC123", "-lc", "vllm serve m --revision abc123")); err != nil {
		t.Errorf("case-different but equal revisions rejected: %v", err)
	}
	agreed, err := recipeRevisionAgreement(advancedRecipe("abc123", "-lc", "vllm serve m --revision abc123"))
	if err != nil || agreed["commandRevisions"] != "abc123" {
		t.Errorf("agreeing revisions rejected: %v %v", agreed, err)
	}
	none, err := recipeRevisionAgreement(advancedRecipe("abc123", "-lc", "vllm serve m"))
	if err != nil || none["commandRevisions"] != "none" {
		t.Errorf("unpinned command should pass: %v %v", none, err)
	}
	if _, err := recipeRevisionAgreement(advancedRecipe("", "-lc", "vllm serve m --revision abc123")); err == nil {
		t.Error("a command pin with no declared model revision was accepted")
	}
}
