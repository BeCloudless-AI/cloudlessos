package api

import (
	"regexp"
	"sort"
	"strings"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

var immutableRecipeSourcePattern = regexp.MustCompile(`^[0-9a-fA-F]{40}(?:[0-9a-fA-F]{24})?$`)

type recipeTrustView struct {
	Level                 string   `json:"level"`
	SupportLevel          string   `json:"supportLevel"`
	Label                 string   `json:"label"`
	Summary               string   `json:"summary"`
	Reviewed              bool     `json:"reviewed"`
	SignedMetadata        bool     `json:"signedMetadata"`
	Signer                string   `json:"signer,omitempty"`
	SigningKeyFingerprint string   `json:"signingKeyFingerprint,omitempty"`
	MetadataDigest        string   `json:"metadataDigest,omitempty"`
	SourcePinned          bool     `json:"sourcePinned"`
	SourceRevision        string   `json:"sourceRevision,omitempty"`
	ImagePinned           bool     `json:"imagePinned"`
	ImageDigest           string   `json:"imageDigest,omitempty"`
	EvidenceHash          string   `json:"evidenceHash,omitempty"`
	VerifiedAt            string   `json:"verifiedAt,omitempty"`
	HostPermissions       []string `json:"hostPermissions"`
	Commands              []string `json:"commands"`
	ImageReference        string   `json:"imageReference,omitempty"`
	ExecutionAllowed      bool     `json:"executionAllowed"`
	ExecutionMode         string   `json:"executionMode"`
	BlockedReason         string   `json:"blockedReason,omitempty"`
}

type recipeExecutionDecision struct {
	Allowed bool
	Mode    string
	Reason  string
}

// recipeExecutionPolicy is the boundary between editable recipe metadata and
// privileged host/container execution. source-scripts-v1 may invoke arbitrary
// programs and Docker is root-equivalent, so only an exact profile from the
// signed Cloudless package may cross that boundary. managed-container-v1 is
// separately admitted because Cloudless owns its complete constrained spec.
func recipeExecutionPolicy(recipe localrecipes.Recipe) recipeExecutionDecision {
	if _, reviewed := localrecipes.ReviewedProfile(recipe); reviewed {
		return recipeExecutionDecision{Allowed: true, Mode: "signed-profile"}
	}
	if recipe.Runtime.Adapter == localrecipes.ManagedContainerAdapter {
		if err := localrecipes.ValidateManagedContainerRecipe(recipe); err != nil {
			return recipeExecutionDecision{Mode: "blocked-invalid-container", Reason: err.Error()}
		}
		return recipeExecutionDecision{Allowed: true, Mode: "constrained-container"}
	}
	return recipeExecutionDecision{
		Mode:   "blocked-unreviewed",
		Reason: "This editable recipe is not Cloudless-signed. Its host and Docker commands are shown for review, but execution is blocked until a constrained recipe runner is available.",
	}
}

// recipeExecutionPolicyEvaluator is replaceable only by package-level failure
// injection tests that exercise lifecycle boundaries with synthetic recipes.
// Production code always retains the fail-closed evaluator above.
var recipeExecutionPolicyEvaluator = recipeExecutionPolicy

func recipeTrustViews(recipes []localrecipes.Recipe, operations []recipeops.Operation) map[string]recipeTrustView {
	result := make(map[string]recipeTrustView, len(recipes))
	for _, recipe := range recipes {
		result[recipe.ID] = buildRecipeTrustView(recipe, operations)
	}
	return result
}

func buildRecipeTrustView(recipe localrecipes.Recipe, operations []recipeops.Operation) recipeTrustView {
	review, reviewed := localrecipes.ReviewedProfile(recipe)
	decision := recipeExecutionPolicyEvaluator(recipe)
	view := recipeTrustView{
		Level: "local-unreviewed", SupportLevel: "experimental", Label: "Local · unreviewed",
		Summary:  "This editable recipe requests host or container commands. Cloudless shows those permissions but does not execute unreviewed code.",
		Reviewed: reviewed, SignedMetadata: reviewed,
		SourcePinned:   immutableRecipeSourcePattern.MatchString(strings.TrimSpace(recipe.Source.Revision)),
		SourceRevision: strings.TrimSpace(recipe.Source.Revision), HostPermissions: recipeHostPermissions(recipe),
		Commands: recipeTrustCommands(recipe), ImageReference: recipe.Engine.Image,
		ExecutionAllowed: decision.Allowed, ExecutionMode: decision.Mode, BlockedReason: decision.Reason,
	}
	if reviewed {
		view.Level, view.Label = "cloudless-reviewed", "Cloudless reviewed"
		view.SupportLevel = "supported"
		view.Summary = "This exact compatibility profile ships inside a package authenticated by the CloudlessOS archive key. Any edit removes reviewed status."
		view.Signer, view.SigningKeyFingerprint, view.MetadataDigest = review.Signer, review.KeyFingerprint, review.MetadataDigest
	} else if decision.Allowed && decision.Mode == "constrained-container" {
		view.Level, view.Label = "cloudless-constrained", "Cloudless constrained"
		view.SupportLevel = "preview"
		view.Summary = "Cloudless generates the complete container command and enforces an immutable image, immutable model, read-only root, no host commands, no host mounts, and no Linux capabilities."
	}
	if recipe.Origin == "catalog" && !reviewed {
		view.Level, view.Label = "catalog-unverified", "Catalog · unverified"
	}

	revision, err := recipeops.RecipeRevision(recipe)
	if err != nil {
		return view
	}
	var latest recipeops.Operation
	found := false
	var prepared recipeops.Operation
	preparedFound := false
	for _, operation := range operations {
		if operation.RecipeID != recipe.ID || operation.RecipeRevision != revision {
			continue
		}
		if operation.PreparedImageDigest != "" && (!preparedFound || operation.UpdatedAt > prepared.UpdatedAt || operation.UpdatedAt == prepared.UpdatedAt && operation.ID > prepared.ID) {
			prepared, preparedFound = operation, true
		}
		if operation.Preflight != nil && operation.Preflight.Runnable && (!found || operation.UpdatedAt > latest.UpdatedAt || operation.UpdatedAt == latest.UpdatedAt && operation.ID > latest.ID) {
			latest, found = operation, true
		}
	}
	if found {
		view.ImageDigest = latest.Preflight.ImageDigest
		view.ImagePinned = recipeImageDigestPattern.MatchString(view.ImageDigest)
		view.EvidenceHash = latest.Preflight.EvidenceHash
		view.VerifiedAt = latest.Preflight.CreatedAt
	}
	if preparedFound {
		view.ImageDigest, view.ImagePinned = prepared.PreparedImageDigest, true
	}
	return view
}

func recipeTrustCommands(recipe localrecipes.Recipe) []string {
	commands := make([]string, 0, 4)
	for _, item := range []struct {
		label   string
		command localrecipes.Command
	}{{"Build", recipe.Runtime.Lifecycle.Build}, {"Download", recipe.Runtime.Lifecycle.Download}, {"Start", recipe.Runtime.Lifecycle.Start}, {"Stop", recipe.Runtime.Lifecycle.Stop}} {
		if strings.TrimSpace(item.command.Program) == "" {
			continue
		}
		command := sanitizeRecipeDiagnosticCommand(item.command)
		commands = append(commands, item.label+": "+strings.Join(append([]string{command.Program}, command.Args...), " "))
	}
	return commands
}

func recipeHostPermissions(recipe localrecipes.Recipe) []string {
	if recipe.Runtime.Adapter == localrecipes.ManagedContainerAdapter {
		permissions := []string{
			"Use NVIDIA accelerators",
			"Use outbound container networking",
			"Write only to the Cloudless model-cache volume",
			"Bind the loopback-only private inference port",
			"No host commands, host paths, Docker socket or Linux capabilities",
		}
		if recipe.Model.TrustRemoteCode {
			permissions = append(permissions, "Execute model repository remote code inside the constrained container")
		}
		sort.Strings(permissions)
		return permissions
	}
	permissions := []string{"Start and stop containers or host processes", "Bind the private inference port"}
	if recipe.Distributed.Nodes > 1 {
		permissions = append(permissions, "Connect to selected Sparks over the enrolled SSH identity", "Use the private cluster fabric and rendezvous port")
	}
	if recipe.Model.TrustRemoteCode {
		permissions = append(permissions, "Execute model repository remote code")
	}
	for _, command := range []localrecipes.Command{recipe.Runtime.Lifecycle.Build, recipe.Runtime.Lifecycle.Download, recipe.Runtime.Lifecycle.Start, recipe.Runtime.Lifecycle.Stop} {
		if strings.EqualFold(command.Program, "docker") || strings.Contains(strings.ToLower(strings.Join(command.Args, " ")), "docker") {
			permissions = append(permissions, "Use the local container engine")
			break
		}
	}
	sort.Strings(permissions)
	return permissions
}
