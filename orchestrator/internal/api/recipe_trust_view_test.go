package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestRecipeTrustViewNeverPromotesLocalRecipeFromPassingCheck(t *testing.T) {
	draft := localrecipes.NewDraft()
	draft.Source.Revision = "0123456789abcdef0123456789abcdef01234567"
	recipe := localrecipes.Recipe{ID: "local", Name: draft.Name, Origin: "local", Trust: "local-custom", Source: draft.Source, Engine: draft.Engine, Model: draft.Model, Distributed: draft.Distributed, Runtime: draft.Runtime, Health: draft.Health}
	revision, err := recipeops.RecipeRevision(recipe)
	if err != nil {
		t.Fatal(err)
	}
	view := buildRecipeTrustView(recipe, []recipeops.Operation{{
		ID: "check", RecipeID: recipe.ID, RecipeRevision: revision, UpdatedAt: "2026-07-31T00:00:00Z",
		Preflight: &recipeops.PreflightArtifact{Runnable: true, ImageDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", EvidenceHash: "sha256:evidence", CreatedAt: "now"},
	}})
	if view.Reviewed || view.SignedMetadata || view.Level != "local-unreviewed" {
		t.Fatalf("local recipe was promoted by compatibility evidence: %#v", view)
	}
	if !view.SourcePinned || !view.ImagePinned || view.EvidenceHash == "" {
		t.Fatalf("verified technical evidence was hidden: %#v", view)
	}
	if view.ExecutionAllowed || view.ExecutionMode != "blocked-unreviewed" || view.BlockedReason == "" {
		t.Fatalf("unreviewed recipe execution policy = %#v", view)
	}
	if view.SupportLevel != "experimental" {
		t.Fatalf("unreviewed recipe support = %q, want experimental", view.SupportLevel)
	}
}

func TestRecipeExecutionPolicyAllowsOnlyExactSignedProfile(t *testing.T) {
	store := localrecipes.New(t.TempDir())
	reviewed, err := store.PreviewImport(localrecipes.DeepSeekDSparkSource)
	if err != nil {
		t.Fatal(err)
	}
	decision := recipeExecutionPolicy(reviewed)
	if !decision.Allowed || decision.Mode != "signed-profile" || decision.Reason != "" {
		t.Fatalf("reviewed decision = %#v", decision)
	}
	if view := buildRecipeTrustView(reviewed, nil); view.SupportLevel != "supported" {
		t.Fatalf("reviewed recipe support = %q, want supported", view.SupportLevel)
	}
	reviewed.Engine.Arguments = append(reviewed.Engine.Arguments, "--local-change")
	decision = recipeExecutionPolicy(reviewed)
	if decision.Allowed || decision.Mode != "blocked-unreviewed" || decision.Reason == "" {
		t.Fatalf("modified decision = %#v", decision)
	}
}

func TestRecipeExecutionPolicyAllowsOnlyValidConstrainedContainer(t *testing.T) {
	recipe := managedContainerRecipeForTest()
	decision := recipeExecutionPolicy(recipe)
	if !decision.Allowed || decision.Mode != "constrained-container" || decision.Reason != "" {
		t.Fatalf("managed decision = %#v", decision)
	}
	if view := buildRecipeTrustView(recipe, nil); view.SupportLevel != "preview" {
		t.Fatalf("constrained recipe support = %q, want preview", view.SupportLevel)
	}
	recipe.Runtime.Lifecycle.Start = localrecipes.Command{Program: "bash", Args: []string{"run.sh"}}
	decision = recipeExecutionPolicy(recipe)
	if decision.Allowed || decision.Mode != "blocked-invalid-container" || decision.Reason == "" {
		t.Fatalf("executable managed decision = %#v", decision)
	}
}

func TestRecipeTrustViewListsRemoteCodeAndClusterPermissions(t *testing.T) {
	recipe := localrecipes.Recipe{ID: "local", Origin: "local", Trust: "local-custom"}
	recipe.Distributed.Nodes = 2
	recipe.Model.TrustRemoteCode = true
	view := buildRecipeTrustView(recipe, nil)
	want := map[string]bool{"Execute model repository remote code": false, "Connect to selected Sparks over the enrolled SSH identity": false}
	for _, permission := range view.HostPermissions {
		if _, ok := want[permission]; ok {
			want[permission] = true
		}
	}
	for permission, found := range want {
		if !found {
			t.Fatalf("missing permission %q in %#v", permission, view.HostPermissions)
		}
	}
}

func TestRecipeTrustViewListsAdvancedMemoryCeiling(t *testing.T) {
	recipe := managedContainerRecipeForTest()
	recipe.Runtime.Adapter = localrecipes.AdvancedContainerAdapter
	recipe.Engine.EntryPoint = "/bin/bash"
	recipe.Engine.Command = []string{"-lc", "exec python3 -m sglang.launch_server"}
	recipe.Engine.Arguments = nil
	recipe.Runtime.Container = localrecipes.ContainerRuntime{Memory: "100g", MemorySwap: "100g"}
	view := buildRecipeTrustView(recipe, nil)
	want := "Limit container memory to 100g with memory+swap capped at 100g"
	for _, permission := range view.HostPermissions {
		if permission == want {
			return
		}
	}
	t.Fatalf("missing memory ceiling %q in %#v", want, view.HostPermissions)
}

func TestRecipeTrustViewRedactsSecretCommandArguments(t *testing.T) {
	recipe := localrecipes.Recipe{ID: "local", Origin: "local", Trust: "local-custom"}
	recipe.Runtime.Lifecycle.Start = localrecipes.Command{Program: "server", Args: []string{"--token", "secret-value", "--port", "8890"}}
	view := buildRecipeTrustView(recipe, nil)
	if len(view.Commands) != 1 || view.Commands[0] != "Start: server --token <redacted> --port 8890" {
		t.Fatalf("commands = %#v", view.Commands)
	}
}
