package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/communityrecipes"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

type communityInstallFixture struct {
	server     *Server
	upstream   *httptest.Server
	privateKey ed25519.PrivateKey
	keyID      string
	publicKey  string
	manifest   map[string]any
}

func newCommunityInstallFixture(t *testing.T) *communityInstallFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := hex.DecodeString("302a300506032b6570032100")
	publicKey := base64.StdEncoding.EncodeToString(append(prefix, public...))
	keyID := "community-test"
	keyring, err := json.Marshal(map[string]any{"keys": []map[string]string{{"id": keyID, "publicKey": publicKey}}})
	if err != nil {
		t.Fatal(err)
	}
	keyringPath := filepath.Join(t.TempDir(), "community-keys.json")
	if err := os.WriteFile(keyringPath, keyring, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDLESS_COMMUNITY_KEYRING", keyringPath)

	empty := func() map[string]any { return map[string]any{"program": "", "args": []any{}} }
	manifest := map[string]any{
		"schema": "cloudless.recipe/v1",
		"metadata": map[string]any{
			"name": "Managed test recipe", "description": "A safely constrained community recipe.",
			"version": "1.0.0", "author": "Cloudless", "category": "language", "license": "Apache-2.0",
		},
		"recipe": map[string]any{
			"name": "Managed test recipe", "description": "A safely constrained community recipe.", "platform": "generic",
			"source": map[string]any{"url": "", "revision": "", "files": map[string]any{}},
			"engine": map[string]any{
				"type": "vllm", "image": "ghcr.io/cloudless/vllm@sha256:" + strings.Repeat("a", 64),
				"servedModelName": "cloudless", "containerPort": 8890, "apiPath": "/v1",
				"proxyHost": "host.docker.internal", "restartPolicy": "no", "arguments": []any{},
			},
			"model": map[string]any{
				"id": "org/model", "revision": strings.Repeat("b", 40), "quantization": "none", "dtype": "auto",
				"kvCacheDtype": "auto", "maxContext": 4096, "maxSequences": 1, "gpuMemoryUtilization": 0.8,
				"tensorParallel": 1, "pipelineParallel": 1, "trustRemoteCode": false,
			},
			"distributed": map[string]any{
				"nodes": 1, "backend": "none", "masterPort": 25000, "interface": "auto", "hca": "auto",
				"ibGidIndex": 0, "workerAlias": "cloudless-recipe-worker", "selectedNodes": []any{},
			},
			"runtime": map[string]any{
				"adapter": "managed-container-v1", "workingDir": "", "timeoutMinutes": 120,
				"prerequisites": []any{}, "environment": map[string]any{},
				"lifecycle": map[string]any{"build": empty(), "download": empty(), "start": empty(), "stop": empty()},
			},
			"health": map[string]any{
				"scheme": "http", "host": "127.0.0.1", "port": 8890, "path": "/health",
				"timeoutSeconds": 180, "intervalSeconds": 3,
			},
		},
	}
	return &communityInstallFixture{
		server: &Server{recipes: localrecipes.New(t.TempDir())}, privateKey: private,
		keyID: keyID, publicKey: publicKey, manifest: manifest,
	}
}

func (f *communityInstallFixture) signedRelease(t *testing.T, signingKeyID string) communityrecipes.Release {
	t.Helper()
	digest, _, err := communityrecipes.Digest(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	release := communityrecipes.Release{
		Schema: communityrecipes.ReleaseSchema, RecipeID: "123e4567-e89b-12d3-a456-426614174000",
		RevisionID: "223e4567-e89b-12d3-a456-426614174000", Version: "1.0.0",
		PublisherID: "323e4567-e89b-12d3-a456-426614174000", ManifestDigest: digest,
		SigningKeyID: signingKeyID, Manifest: f.manifest,
	}
	canonical, err := communityrecipes.Canonical(release)
	if err != nil {
		t.Fatal(err)
	}
	release.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.privateKey, canonical))
	return release
}

func (f *communityInstallFixture) serve(t *testing.T, release communityrecipes.Release, revocations []communityrecipes.Revocation) {
	t.Helper()
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/recipes/managed-test/revisions/1.0.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"recipe": map[string]string{"slug": "managed-test"}, "release": release,
				"validation": map[string]any{
					"result": "passed", "checkedAt": "2026-08-06T00:00:00Z",
					"image": map[string]any{
						"admission": "moderator-override", "policy": "actionable-critical-v1", "scanner": "trivy",
						"summary":          map[string]int{"total": 12, "high": 10, "critical": 2, "actionableCritical": 2, "fixableCritical": 2, "warnings": 10},
						"blockingFindings": []map[string]string{{"id": "CVE-TEST", "package": "runtime", "severity": "CRITICAL", "fixed": "2.0.0"}},
					},
				},
			})
		case "/v1/recipes/trust/revocations":
			_ = json.NewEncoder(w).Encode(map[string]any{"revocations": revocations})
		default:
			http.NotFound(w, r)
		}
	}))
	f.server.accountBaseURL, f.server.accountHTTPClient = f.upstream.URL, f.upstream.Client()
	t.Cleanup(f.upstream.Close)
}

func (f *communityInstallFixture) install() *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/community/install", strings.NewReader(`{"slug":"managed-test","version":"1.0.0"}`))
	response := httptest.NewRecorder()
	f.server.communityRecipeInstall(response, request)
	return response
}

func TestCommunityRecipeInstallVerifiesAndPersistsExactRelease(t *testing.T) {
	fixture := newCommunityInstallFixture(t)
	fixture.serve(t, fixture.signedRelease(t, fixture.keyID), nil)
	response := fixture.install()
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"verified":true`) {
		t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
	}
	recipes, err := fixture.server.recipes.List()
	if err != nil || len(recipes) != 1 || recipes[0].Community == nil || recipes[0].Community.RevisionID != "223e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("exact verified provenance was not persisted: %#v %v", recipes, err)
	}
	if validation := recipes[0].Community.Validation; validation == nil || validation.Image == nil || validation.Image.Summary.Critical != 2 || validation.Image.Admission != "moderator-override" {
		t.Fatalf("sanitized vulnerability evidence was not persisted: %#v", recipes[0].Community)
	}
}

func TestCommunityRecipeInstallRejectsUnknownSigningKey(t *testing.T) {
	fixture := newCommunityInstallFixture(t)
	fixture.serve(t, fixture.signedRelease(t, "unknown-key"), nil)
	if response := fixture.install(); response.Code != http.StatusForbidden {
		t.Fatalf("expected unknown key rejection, got %d %s", response.Code, response.Body.String())
	}
}

func TestCommunityRecipeInstallRejectsTamperedRelease(t *testing.T) {
	fixture := newCommunityInstallFixture(t)
	release := fixture.signedRelease(t, fixture.keyID)
	release.Manifest["metadata"].(map[string]any)["description"] = "Tampered after signing."
	fixture.serve(t, release, nil)
	if response := fixture.install(); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected tamper rejection, got %d %s", response.Code, response.Body.String())
	}
}

func TestCommunityRecipeInstallRejectsRevokedExactRevision(t *testing.T) {
	fixture := newCommunityInstallFixture(t)
	release := fixture.signedRelease(t, fixture.keyID)
	revocation := communityrecipes.Revocation{
		Schema: "cloudless.recipe.revocation/v1", RevisionID: release.RevisionID,
		Reason: "unsafe behavior confirmed", SigningKeyID: fixture.keyID, CreatedAt: "2026-08-01T00:00:00Z",
	}
	canonical, err := communityrecipes.Canonical(revocation)
	if err != nil {
		t.Fatal(err)
	}
	revocation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, canonical))
	fixture.serve(t, release, []communityrecipes.Revocation{revocation})
	if response := fixture.install(); response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "revoked") {
		t.Fatalf("expected revocation rejection, got %d %s", response.Code, response.Body.String())
	}
}
