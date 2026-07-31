package localrecipes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const CloudlessArchiveFingerprint = "745BF7A97F64EB716DAF7677974145C2D867C99E"

// Review describes compatibility metadata compiled into the Cloudless
// orchestrator package. The package itself is authenticated by the Cloudless
// APT archive signature; changing any executable recipe field invalidates this
// exact profile match and turns the saved recipe into a local customization.
type Review struct {
	RecipeID       string `json:"recipeId"`
	Profile        string `json:"profile"`
	MetadataDigest string `json:"metadataDigest"`
	Signer         string `json:"signer"`
	KeyFingerprint string `json:"keyFingerprint"`
}

// ReviewedProfile returns signed-package provenance only for an exact profile
// shipped by Cloudless. A persisted trust string alone is never sufficient.
func ReviewedProfile(recipe Recipe) (Review, bool) {
	if recipe.Origin != "github" || recipe.Trust != "reviewed-import" {
		return Review{}, false
	}
	var profile string
	var expected Draft
	switch recipe.ID {
	case DeepSeekDSparkID:
		profile, expected = "deepseek-v4-flash-dspark-2x", deepSeekDraft()
	case DeepSeekV4Flash1MID:
		profile, expected = "deepseek-v4-flash-dual-dspark-1m", deepSeekV4Flash1MDraft()
	default:
		return Review{}, false
	}
	expectedRecipe := recipeFromDraft(recipe.ID, "github", "reviewed-import", recipe.ImportedAt, expected)
	want, err := canonicalDraft(DraftFromRecipe(expectedRecipe))
	if err != nil {
		return Review{}, false
	}
	got, err := canonicalDraft(DraftFromRecipe(recipe))
	if err != nil || string(got) != string(want) {
		return Review{}, false
	}
	digest := sha256.Sum256(want)
	return Review{
		RecipeID: recipe.ID, Profile: profile, MetadataDigest: "sha256:" + hex.EncodeToString(digest[:]),
		Signer: "CloudlessOS signed package", KeyFingerprint: CloudlessArchiveFingerprint,
	}, true
}

// ReviewInventory returns every exact executable recipe profile authenticated
// by the signed Cloudless package. Release tooling serializes this list into a
// separately signed trust inventory, so operators can verify reviewed metadata
// without executing cloudlessd.
func ReviewInventory() []Review {
	candidates := []Recipe{
		recipeFromDraft(DeepSeekDSparkID, "github", "reviewed-import", "", deepSeekDraft()),
		recipeFromDraft(DeepSeekV4Flash1MID, "github", "reviewed-import", "", deepSeekV4Flash1MDraft()),
	}
	out := make([]Review, 0, len(candidates))
	for _, recipe := range candidates {
		if review, ok := ReviewedProfile(recipe); ok {
			out = append(out, review)
		}
	}
	return out
}

func canonicalDraft(draft Draft) ([]byte, error) {
	return json.Marshal(draft)
}
