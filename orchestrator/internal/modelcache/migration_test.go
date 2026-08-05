package modelcache

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/modelstorage"
)

func TestPrepareImportsCacheWithoutDeletingLegacyData(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "legacy")
	root := filepath.Join(parent, "managed")
	blob := filepath.Join(source, "hub", "models--org--model", "blobs", "abc")
	snapshot := filepath.Join(source, "hub", "models--org--model", "snapshots", "rev")
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../blobs/abc", filepath.Join(snapshot, "model.bin")); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, source, -1, -1); err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode()&os.ModeSetgid == 0 || rootInfo.Mode().Perm() != 0o750 {
		t.Fatalf("cache root mode = %v, want setgid 750", rootInfo.Mode())
	}
	runtimeInfo, err := os.Stat(filepath.Join(root, runtimeCacheName))
	if err != nil || runtimeInfo.Mode()&os.ModeSticky == 0 || runtimeInfo.Mode().Perm() != 0o777 {
		t.Fatalf("runtime cache mode = %v, %v; want sticky 777", runtimeInfo, err)
	}
	for _, name := range []string{"pip", "uv", "torch-extensions"} {
		info, statErr := os.Stat(filepath.Join(root, runtimeCacheName, name))
		if statErr != nil || info.Mode()&os.ModeSticky == 0 || info.Mode().Perm() != 0o777 {
			t.Fatalf("%s cache mode = %v, %v; want sticky 777", name, info, statErr)
		}
	}
	imported := filepath.Join(root, "hub", "models--org--model", "snapshots", "rev", "model.bin")
	data, err := os.ReadFile(imported)
	if err != nil || string(data) != "weights" {
		t.Fatalf("imported model = %q, %v", data, err)
	}
	if data, err := os.ReadFile(blob); err != nil || string(data) != "weights" {
		t.Fatalf("legacy model was altered = %q, %v", data, err)
	}
	importedBlob := filepath.Join(root, "hub", "models--org--model", "blobs", "abc")
	sourceInfo, sourceErr := os.Stat(blob)
	importedInfo, importedErr := os.Stat(importedBlob)
	if sourceErr != nil || importedErr != nil || !os.SameFile(sourceInfo, importedInfo) {
		t.Fatalf("same-filesystem migration duplicated the blob: source=%v imported=%v", sourceErr, importedErr)
	}
	if marker, err := readMarker(filepath.Join(root, markerName)); err != nil || marker.Source != source {
		t.Fatalf("marker = %+v, %v", marker, err)
	}
	info, err := os.Stat(filepath.Join(root, "hub"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("imported cache directory must retain setgid inheritance: mode=%v", info.Mode())
	}
}

func TestPrepareResumesMatchingPartialImportAndRejectsConflict(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "legacy")
	root := filepath.Join(parent, "managed")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "same"), []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "same"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, source, -1, -1); err != nil {
		t.Fatalf("resume matching import: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "same")); err != nil || info.Mode().Perm() != 0o640 {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("resumed cache file mode = %o, want 640", info.Mode().Perm())
	}

	secondSource := filepath.Join(parent, "legacy-two")
	if err := os.MkdirAll(secondSource, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondSource, "same"), []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Prepare(root, secondSource, -1, -1)
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("conflicting import error = %v", err)
	}
}

func TestPrepareRejectsEscapingSymlinkAndUnsafeRoots(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "legacy")
	root := filepath.Join(parent, "managed")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, source, -1, -1); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escaping symlink error = %v", err)
	}
	if err := Prepare("relative", "", -1, -1); err == nil {
		t.Fatal("relative cache root was accepted")
	}
	if err := Prepare(source, source, -1, -1); err == nil {
		t.Fatal("overlapping cache paths were accepted")
	}
}

func TestPrepareEmptyCacheIsIdempotent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	if err := Prepare(root, "", -1, -1); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, "", -1, -1); err != nil {
		t.Fatal(err)
	}
}

func TestPreparePreservesRootSquashedSharedExportOwnership(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "shared")
	source := filepath.Join(parent, "legacy")
	if err := os.MkdirAll(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, modelstorage.MarkerName), []byte("cloudless-nfs-v1 test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "must-stay-local"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, source, 1234, 1234); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0o777 {
		t.Fatalf("shared export root was modified: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(root, "must-stay-local")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy local cache was imported into shared storage: %v", err)
	}
}

func TestPrepareSplitSharedHubPreservesLocalMigrationAndRuntimeCaches(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "managed")
	shared := filepath.Join(parent, "shared")
	source := filepath.Join(parent, "legacy")
	for _, path := range []string{root, shared, source} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	markerPath := filepath.Join(root, markerName)
	markerPayload := []byte(`{"schema":1,"source":"legacy-before-nfs"}` + "\n")
	if err := os.WriteFile(markerPath, markerPayload, 0o640); err != nil {
		t.Fatal(err)
	}
	sharedMarker := filepath.Join(shared, modelstorage.MarkerName)
	if err := os.WriteFile(sharedMarker, []byte("cloudless-nfs-v1 test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedMarker, filepath.Join(root, modelstorage.MarkerName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "must-not-import"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, source, -1, -1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, runtimeCacheName, "pip")); err != nil {
		t.Fatalf("local runtime cache was not prepared: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "must-not-import")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy data was imported while split NFS was active: %v", err)
	}
	if data, err := os.ReadFile(markerPath); err != nil || string(data) != string(markerPayload) {
		t.Fatalf("local migration evidence changed: %q, %v", data, err)
	}
}

func TestPrepareIgnoresLegacyCompilerCaches(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "legacy")
	root := filepath.Join(parent, "managed")
	for _, base := range []string{source, root} {
		if err := os.MkdirAll(filepath.Join(base, "flashinfer"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "flashinfer", ".ninja_deps"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "flashinfer", ".ninja_deps"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "hub", "weights"), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, source, -1, -1); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "flashinfer", ".ninja_deps")); err != nil || string(data) != "new" {
		t.Fatalf("local compiler cache changed: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "hub", "weights")); err != nil || string(data) != "model" {
		t.Fatalf("model weights were not imported: %q, %v", data, err)
	}
}
