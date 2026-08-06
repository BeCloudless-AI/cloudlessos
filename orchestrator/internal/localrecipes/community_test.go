package localrecipes

import "testing"

func TestInstallCommunityPersistsExactProvenanceAndRevocation(t *testing.T) {
	store := New(t.TempDir())
	provenance := CommunityProvenance{RecipeID: "123e4567-e89b-12d3-a456-426614174000", RevisionID: "223e4567-e89b-12d3-a456-426614174000", Slug: "managed-test", Version: "1.2.3", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SigningKeyID: "community-test"}
	recipe, err := store.InstallCommunity(managedContainerTestDraft(), provenance)
	if err != nil {
		t.Fatal(err)
	}
	if recipe.Trust != "community-signed" || recipe.Community == nil || recipe.Community.RevisionID != provenance.RevisionID {
		t.Fatalf("unexpected installation: %#v", recipe)
	}
	second := provenance
	second.RevisionID, second.Version = "323e4567-e89b-12d3-a456-426614174000", "1.2.4"
	updatedRecipe, err := store.InstallCommunity(managedContainerTestDraft(), second)
	if err != nil || len(updatedRecipe.CommunityRollback) != 1 {
		t.Fatalf("update did not retain rollback: %#v %v", updatedRecipe, err)
	}
	second.Validation = &CommunityValidation{Result: "passed", Image: &CommunityImageValidation{Admission: "moderator-override", Summary: CommunityVulnerabilitySummary{Total: 3, Critical: 1, High: 2}}}
	idempotent, err := store.InstallCommunity(managedContainerTestDraft(), second)
	if err != nil || len(idempotent.CommunityRollback) != 1 {
		t.Fatalf("idempotent reinstall changed rollback history: %#v %v", idempotent, err)
	}
	if idempotent.Community.Validation == nil || idempotent.Community.Validation.Image == nil || idempotent.Community.Validation.Image.Summary.Critical != 1 {
		t.Fatalf("idempotent reinstall did not refresh vulnerability evidence: %#v", idempotent.Community)
	}
	if err := store.MarkCommunityRevoked(provenance.RevisionID, "test revocation"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RollbackCommunity(updatedRecipe.ID); err == nil {
		t.Fatal("rollback accepted a revoked historical revision")
	}

	// A clean store still supports rollback when neither exact revision is revoked.
	store = New(t.TempDir())
	if _, err := store.InstallCommunity(managedContainerTestDraft(), provenance); err != nil {
		t.Fatal(err)
	}
	updatedRecipe, err = store.InstallCommunity(managedContainerTestDraft(), second)
	if err != nil {
		t.Fatal(err)
	}
	recipe, err = store.RollbackCommunity(updatedRecipe.ID)
	if err != nil || recipe.Community.RevisionID != provenance.RevisionID {
		t.Fatalf("rollback failed: %#v %v", recipe, err)
	}
	if err := store.MarkCommunityRevoked(provenance.RevisionID, "current revocation"); err != nil {
		t.Fatal(err)
	}
	updated, ok, err := store.Get(recipe.ID)
	if err != nil || !ok || updated.Community == nil || !updated.Community.Revoked || updated.Trust != "community-revoked" {
		t.Fatalf("revocation not retained: %#v %v", updated, err)
	}
}
