package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecipeArtifactManifestDetectsSameSizeCorruption(t *testing.T) {
	repository := t.TempDir()
	blob := filepath.Join(repository, "blobs", "weights")
	snapshot := filepath.Join(repository, "snapshots", "revision")
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("good"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "blobs", "weights"), filepath.Join(snapshot, "model.bin")); err != nil {
		t.Fatal(err)
	}
	first, err := buildRecipeArtifactManifest(repository, snapshot, "owner/model", "revision")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := buildRecipeArtifactManifest(repository, snapshot, "owner/model", "revision")
	if err != nil {
		t.Fatal(err)
	}
	if first.Bytes != second.Bytes || first.Digest == second.Digest {
		t.Fatalf("same-size corruption was not detected: before=%+v after=%+v", first, second)
	}
}

func TestRecipeArtifactManifestIsDeterministic(t *testing.T) {
	repository := t.TempDir()
	snapshot := filepath.Join(repository, "snapshots", "revision")
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"z.bin": "last", "a.json": "first"} {
		if err := os.WriteFile(filepath.Join(snapshot, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := buildRecipeArtifactManifest(repository, snapshot, "owner/model", "revision")
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildRecipeArtifactManifest(repository, snapshot, "owner/model", "revision")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Files[0].Path != "a.json" {
		t.Fatalf("manifest is not deterministic: %#v %#v", first, second)
	}
}

func TestRecipeArtifactManifestRejectsEscapingSymlink(t *testing.T) {
	repository := t.TempDir()
	snapshot := filepath.Join(repository, "snapshots", "revision")
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(snapshot, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := buildRecipeArtifactManifest(repository, snapshot, "owner/model", "revision"); err == nil {
		t.Fatal("escaping symlink was accepted")
	}
}

func TestRecipeArtifactManifestSaveIsReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "complete", "manifest.json")
	manifest := recipeArtifactManifest{Version: recipeArtifactManifestVersion, ModelID: "owner/model", Revision: "revision", Files: []recipeArtifactFile{{Path: "model.bin", Size: 4, SHA256: "770e607624d689265ca6c44884d0807d9b054d23c473c106c72be9de08b7376c"}}, Bytes: 4}
	var err error
	manifest.Digest, err = digestRecipeArtifactManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveRecipeArtifactManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadRecipeArtifactManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ModelID != manifest.ModelID || loaded.Revision != manifest.Revision {
		t.Fatalf("loaded manifest = %#v", loaded)
	}
}
