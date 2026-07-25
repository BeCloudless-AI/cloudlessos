package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/models"
)

func TestCustomModelsPersistAcrossStoreReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	model := models.Model{ID: "org/community-model", Name: "Community model", Tags: []string{"chat"}, Source: "huggingface"}
	if err := store.UpsertCustomModel(model); err != nil {
		t.Fatal(err)
	}
	model.Tags[0] = "mutated"

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.CustomModels()["org/community-model"]
	if got.Name != "Community model" || len(got.Tags) != 1 || got.Tags[0] != "chat" {
		t.Fatalf("persisted custom model = %#v", got)
	}
}

func TestHuggingFaceTokenUsesPrivateSeparateFile(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	const token = "hf_test_secret"
	if err := store.SetHuggingFaceToken(token); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("Hugging Face token leaked into state.json")
	}
	secretPath := filepath.Join(dir, "huggingface-token")
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token permissions = %o, want 600", info.Mode().Perm())
	}
	got, err := store.HuggingFaceToken()
	if err != nil || got != token {
		t.Fatalf("HuggingFaceToken() = %q, %v", got, err)
	}
	if err := store.ClearHuggingFaceToken(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.HuggingFaceToken(); err != nil || got != "" {
		t.Fatalf("token after clear = %q, %v", got, err)
	}
}
