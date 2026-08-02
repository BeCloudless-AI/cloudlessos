package communityrecipes

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestCanonicalGoldenFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Input     any    `json:"input"`
		Canonical string `json:"canonical"`
		SHA256    string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	canonical, err := Canonical(fixture.Input)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != fixture.Canonical {
		t.Fatalf("canonical mismatch\nwant %s\n got %s", fixture.Canonical, canonical)
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != fixture.SHA256 {
		t.Fatal("digest mismatch")
	}
}

func TestCanonicalSortsReleaseStructKeysLikeCommunityService(t *testing.T) {
	release := Release{
		Schema:         ReleaseSchema,
		RecipeID:       "recipe",
		RevisionID:     "revision",
		Version:        "1.0.0",
		PublisherID:    "publisher",
		ManifestDigest: "sha256:digest",
		SigningKeyID:   "community-key",
		Manifest:       map[string]any{"schema": ManifestSchema},
	}
	canonical, err := Canonical(release)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"manifest":{"schema":"cloudless.recipe/v1"},"manifestDigest":"sha256:digest","publisherId":"publisher","recipeId":"recipe","revisionId":"revision","schema":"cloudless.recipe.release/v1","signingKeyId":"community-key","version":"1.0.0"}`
	if string(canonical) != want {
		t.Fatalf("release canonicalization differs from the community service\nwant %s\n got %s", want, canonical)
	}
}

func TestParseAndVerify(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"schema": ManifestSchema, "value": "test"}
	digest, _, err := Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	release := Release{Schema: ReleaseSchema, RecipeID: "r", RevisionID: "v", Version: "1.0.0", PublisherID: "p", ManifestDigest: digest, SigningKeyID: "test-key", Manifest: manifest}
	canonical, err := Canonical(release)
	if err != nil {
		t.Fatal(err)
	}
	release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, canonical))
	raw, err := Canonical(release)
	if err != nil {
		t.Fatal(err)
	}
	spkiPrefix, _ := hex.DecodeString("302a300506032b6570032100")
	spki := base64.StdEncoding.EncodeToString(append(spkiPrefix, public...))
	if _, err := ParseAndVerify(raw, spki); err != nil {
		t.Fatal(err)
	}

	release.Manifest["value"] = "tampered"
	tampered, _ := Canonical(release)
	if _, err := ParseAndVerify(tampered, spki); err == nil {
		t.Fatal("tampered release verified")
	}
}

func TestVerifyRevocation(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	revocation := Revocation{Schema: "cloudless.recipe.revocation/v1", RevisionID: "revision", Reason: "unsafe behavior confirmed", SigningKeyID: "test-key", CreatedAt: "2026-08-01T00:00:00Z"}
	canonical, err := Canonical(revocation)
	if err != nil {
		t.Fatal(err)
	}
	revocation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, canonical))
	prefix, _ := hex.DecodeString("302a300506032b6570032100")
	spki := base64.StdEncoding.EncodeToString(append(prefix, public...))
	if err := VerifyRevocation(revocation, spki); err != nil {
		t.Fatal(err)
	}
	revocation.Reason = "tampered"
	if err := VerifyRevocation(revocation, spki); err == nil {
		t.Fatal("tampered revocation verified")
	}
}
