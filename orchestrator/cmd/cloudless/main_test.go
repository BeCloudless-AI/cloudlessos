package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/communityrecipes"
)

func TestParseOptionsAcceptsModeratorForce(t *testing.T) {
	positional, api, key, force, reason, err := parseOptions([]string{
		"manifest.yaml", "--api", "https://example.invalid/recipes/", "--api-key", "cld_alpha_test",
		"--force", "--force-reason", "Controlled compatibility testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(positional) != 1 || positional[0] != "manifest.yaml" || api != "https://example.invalid/recipes" || key != "cld_alpha_test" || !force || reason != "Controlled compatibility testing" {
		t.Fatalf("options = %#v %q %q %v %q", positional, api, key, force, reason)
	}
}

func TestClientErrorIncludesStructuredDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Invalid request body","details":[{"field":"manifest.recipe.engine.type","message":"Unsupported engine"}]}`))
	}))
	defer server.Close()
	c := &client{base: server.URL, http: server.Client()}
	_, err := c.request(context.Background(), http.MethodPost, "", map[string]any{}, nil, false)
	if err == nil || !strings.Contains(err.Error(), "manifest.recipe.engine.type: Unsupported engine") {
		t.Fatalf("detailed error = %v", err)
	}
}

func TestPublishForceResubmitsMatchingRejectedRevision(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "..", "docs", "examples", "community-managed-container.yaml")
	manifest, err := readManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	digest, _, err := communityrecipes.Digest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	metadata := manifest["metadata"].(map[string]any)
	version := metadata["version"].(string)
	const recipeID = "11111111-1111-4111-8111-111111111111"
	const revisionID = "22222222-2222-4222-8222-222222222222"
	var submitBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"slug exists"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/me/recipes":
			_ = json.NewEncoder(w).Encode(map[string]any{"recipes": []any{map[string]any{
				"id": recipeID, "slug": "example-managed-model", "submissions": []any{map[string]any{
					"id": revisionID, "version": version, "status": "rejected",
				}},
			}}})
		case r.Method == http.MethodPost && r.URL.Path == "/"+recipeID+"/revisions":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"version exists"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/me/recipes/"+recipeID+"/revisions/"+revisionID:
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": map[string]any{"manifest_digest": digest}})
		case r.Method == http.MethodPost && r.URL.Path == "/"+recipeID+"/revisions/"+revisionID+"/submit":
			if err := json.NewDecoder(r.Body).Decode(&submitBody); err != nil {
				t.Errorf("decode submit body: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": map[string]any{"id": revisionID, "status": "submitted"}})
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := &client{base: server.URL, key: "cld_alpha_moderator", http: server.Client()}
	if err := publish(context.Background(), c, manifestPath, true, "Controlled compatibility testing"); err != nil {
		t.Fatal(err)
	}
	if submitBody["force"] != true || submitBody["reason"] != "Controlled compatibility testing" {
		t.Fatalf("submit body = %#v", submitBody)
	}
}
