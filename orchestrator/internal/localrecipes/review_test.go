package localrecipes

import "testing"

func TestReviewedProfileRequiresExactSignedPackageProfile(t *testing.T) {
	recipe := recipeFromDraft(DeepSeekDSparkID, "github", "reviewed-import", "now", deepSeekDraft())
	review, ok := ReviewedProfile(recipe)
	if !ok || review.MetadataDigest == "" || review.KeyFingerprint != CloudlessArchiveFingerprint {
		t.Fatalf("review = %#v, %v", review, ok)
	}
	recipe.Engine.Arguments = append(recipe.Engine.Arguments, "--unreviewed-change")
	if review, ok := ReviewedProfile(recipe); ok {
		t.Fatalf("modified profile retained reviewed provenance: %#v", review)
	}
}

func TestReviewedProfileRejectsForgedTrustString(t *testing.T) {
	recipe := recipeFromDraft("local-forged", "github", "reviewed-import", "now", NewDraft())
	if review, ok := ReviewedProfile(recipe); ok {
		t.Fatalf("forged trust was accepted: %#v", review)
	}
}

func TestReviewInventoryContainsEveryExactReviewedRecipe(t *testing.T) {
	inventory := ReviewInventory()
	want := map[string]bool{DeepSeekDSparkID: false, DeepSeekV4Flash1MID: false}
	for _, review := range inventory {
		if review.RecipeID == "" || review.Profile == "" || review.MetadataDigest == "" ||
			review.KeyFingerprint != CloudlessArchiveFingerprint {
			t.Fatalf("incomplete reviewed profile inventory entry: %#v", review)
		}
		if _, ok := want[review.RecipeID]; !ok {
			t.Fatalf("unexpected reviewed recipe %q", review.RecipeID)
		}
		want[review.RecipeID] = true
	}
	for id, found := range want {
		if !found {
			t.Fatalf("reviewed recipe %q is absent from the offline inventory", id)
		}
	}
}

func TestLegacyMiaAICacheLocationMigratesBackToReviewedProfile(t *testing.T) {
	recipe := recipeFromDraft(DeepSeekV4Flash1MID, "github", "reviewed-import", "now", deepSeekV4Flash1MDraft())
	recipe.Runtime.Environment["HF_CACHE"] = "cloudless-hf"
	recipe = normalize(recipe)
	if got := recipe.Runtime.Environment["HF_CACHE"]; got != "/var/lib/cloudless/models-cache" {
		t.Fatalf("HF_CACHE = %q", got)
	}
	if review, ok := ReviewedProfile(recipe); !ok {
		t.Fatalf("migrated built-in recipe is not reviewed: %#v", review)
	}
}

func TestDraftFromRecipeDoesNotMutateHistoricalSnapshot(t *testing.T) {
	recipe := recipeFromDraft(DeepSeekV4Flash1MID, "github", "reviewed-import", "now", deepSeekV4Flash1MDraft())
	recipe.Runtime.Environment["HF_CACHE"] = "cloudless-hf"
	_ = DraftFromRecipe(recipe)
	if got := recipe.Runtime.Environment["HF_CACHE"]; got != "cloudless-hf" {
		t.Fatalf("identity calculation mutated historical snapshot: HF_CACHE = %q", got)
	}
}
