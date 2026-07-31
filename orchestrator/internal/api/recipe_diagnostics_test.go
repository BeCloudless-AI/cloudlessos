package api

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestRecipeDiagnosticBundleRedactsSecretsAndContainsEvidence(t *testing.T) {
	recipe := localrecipes.Recipe{
		ID: "recipe", Name: "Test recipe",
		Source: localrecipes.Source{URL: "https://alice:hunter2@example.com/repository", Revision: "commit"},
		Runtime: localrecipes.Runtime{
			ArtifactDigest: "sha256:runtime",
			Environment:    map[string]string{"HF_TOKEN": "hf_privatevalue", "NORMAL": "must-also-be-hidden"},
			Lifecycle:      localrecipes.Lifecycle{Start: localrecipes.Command{Program: "server", Args: []string{"--token", "arbitrary-secret", "--port", "8890"}}},
		},
	}
	operation := recipeops.Operation{
		ID: "operation", RecipeID: recipe.ID, RecipeRevision: "sha256:recipe", RecipeSnapshot: recipe,
		Phase: recipeops.PhaseFailed, Error: "PASSWORD=hunter2 github_pat_123456789abcdef",
		Resources: []recipeops.Resource{{Kind: "container", ID: "runtime", Node: "Spark A"}},
	}
	inventory := recipeOperationInventory{Operation: operation, Observations: []recipeops.Observation{{Resource: operation.Resources[0], Presence: recipeops.PresencePresent, Detail: "token=hf_inventorysecret"}}}
	bundle, err := buildRecipeDiagnosticBundle(operation, recipe, inventory, map[string]any{"nodes": 2}, map[string]string{"Spark A": "PASSWORD=log-secret\nruntime echoed arbitrary-secret and must-also-be-hidden"})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]string)
	for _, file := range reader.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[file.Name] = string(payload)
	}
	for _, required := range []string{"README.txt", "operation.json", "failure.json", "recipe.json", "inventory.json", "topology.json", "digests.json", "logs/Spark-A.log"} {
		if _, ok := entries[required]; !ok {
			t.Fatalf("missing diagnostic entry %q; have %#v", required, entries)
		}
	}
	joined := strings.Join(mapValues(entries), "\n")
	for _, secret := range []string{"hunter2", "hf_privatevalue", "must-also-be-hidden", "arbitrary-secret", "github_pat_123456789abcdef", "hf_inventorysecret", "log-secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("secret %q remains in diagnostic bundle", secret)
		}
	}
	if !strings.Contains(entries["failure.json"], "runtime") || !strings.Contains(entries["recipe.json"], `"HF_TOKEN"`) || !strings.Contains(entries["recipe.json"], `\u003credacted\u003e`) {
		t.Fatalf("diagnostic evidence is incomplete: failure=%s recipe=%s", entries["failure.json"], entries["recipe.json"])
	}
}

func TestRecipeDiagnosticCommandRedactsOnlySecretArgumentValue(t *testing.T) {
	command := sanitizeRecipeDiagnosticCommand(localrecipes.Command{Program: "server", Args: []string{"--api-key", "secret", "--port", "8890"}})
	if strings.Join(command.Args, " ") != "--api-key <redacted> --port 8890" {
		t.Fatalf("sanitized command = %#v", command.Args)
	}
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
