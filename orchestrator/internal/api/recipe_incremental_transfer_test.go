package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/jobs"
)

func TestMissingRecipeTransferEntriesSkipsVerifiedFiles(t *testing.T) {
	expected := recipeTransferManifest{Bytes: 9, Entries: []recipeTransferEntry{
		{Path: "blobs/a", Kind: "file", Size: 4, SHA256: "aaaa"},
		{Path: "blobs/b", Kind: "file", Size: 5, SHA256: "bbbb"},
		{Path: "snapshots/rev/a", Kind: "symlink", Target: "../../blobs/a"},
	}}
	present := recipeTransferManifest{Entries: []recipeTransferEntry{
		expected.Entries[0],
		{Path: "blobs/b", Kind: "file", Size: 5, SHA256: "corrupt"},
	}}
	missing, bytes := missingRecipeTransferEntries(expected, present)
	if len(missing) != 2 || bytes != 5 || missing[0].Path != "blobs/b" || missing[1].Kind != "symlink" {
		t.Fatalf("missing entries = %#v, bytes = %d", missing, bytes)
	}
}

func TestRetryRecipePeerTransferIsBoundedAndResumes(t *testing.T) {
	attempts := 0
	err := retryRecipePeerTransfer(context.Background(), jobs.NewManager().Create("recipe:test"), "spark-b", func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary link failure")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("retry result = %v after %d attempts", err, attempts)
	}
}

func TestBuildRecipeTransferManifestIncludesBlobAndSnapshotLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "snapshots", "rev"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "blobs", "a"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "blobs", "a"), filepath.Join(root, "snapshots", "rev", "model")); err != nil {
		t.Fatal(err)
	}
	manifest, err := buildRecipeTransferManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Bytes != 4 || len(manifest.Entries) != 2 || manifest.Entries[0].Kind != "file" || manifest.Entries[1].Kind != "symlink" {
		t.Fatalf("transfer manifest = %#v", manifest)
	}
}

func TestBuildRecipeTransferManifestRejectsEscapingLink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := buildRecipeTransferManifest(root); err == nil {
		t.Fatal("escaping transfer symlink was accepted")
	}
}
