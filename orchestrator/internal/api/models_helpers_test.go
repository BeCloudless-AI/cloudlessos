package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/jobs"
)

func TestScanModelHub(t *testing.T) {
	root := t.TempDir()
	hub := filepath.Join(root, "hub")
	repo := filepath.Join(hub, "models--Qwen--Qwen-Test")
	if err := os.MkdirAll(filepath.Join(repo, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "snapshots", "abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "refs", "main"), []byte("abc123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hub, "datasets--ignore--me"), 0o755); err != nil {
		t.Fatal(err)
	}
	have := scanModelHub(root)
	if !have["Qwen/Qwen-Test"] || len(have) != 1 {
		t.Fatalf("scanModelHub = %#v", have)
	}
}

func TestDirectoryBytesAndProgressMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "weights"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := directoryBytes(root); got != 1024 {
		t.Fatalf("directoryBytes = %d, want 1024", got)
	}
	if got := formatDownloadProgress(512, 1024); got == "" {
		t.Fatal("formatDownloadProgress returned an empty message")
	}
}

func TestModelRepoIncompleteCountsOnlyResumableChunks(t *testing.T) {
	root := t.TempDir()
	blobs := filepath.Join(root, "hub", "models--Qwen--Test", "blobs")
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"one.incomplete": "partial", "two.incomplete": "partial", "complete": "ready",
	} {
		if err := os.WriteFile(filepath.Join(blobs, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := (&Server{}).modelRepoIncomplete(context.Background(), "Qwen/Test", root); got != 2 {
		t.Fatalf("incomplete chunks = %d, want 2", got)
	}
}

func TestModelCacheNameRejectsUnsafeIDs(t *testing.T) {
	if got, ok := modelCacheName("Qwen/Qwen-Test"); !ok || got != "models--Qwen--Qwen-Test" {
		t.Fatalf("modelCacheName valid = %q, %v", got, ok)
	}
	for _, id := range []string{"", "../model", "org/../model", "org\\model", "org//model"} {
		if got, ok := modelCacheName(id); ok {
			t.Errorf("modelCacheName(%q) = %q, true; want rejected", id, got)
		}
	}
}

func TestRemoveExposedModelPreservesUserFolder(t *testing.T) {
	cloudlessHome := filepath.Join(t.TempDir(), "Cloudless")
	t.Setenv("CLOUDLESS_HOME", cloudlessHome)
	modelsDir := filepath.Join(cloudlessHome, "Models")
	managed := filepath.Join(modelsDir, "Qwen--Managed")
	userOwned := filepath.Join(modelsDir, "Qwen--Personal")
	for _, dir := range []string{managed, userOwned} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(managed, ".cloudless-revision"), []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := &Server{}
	if err := server.removeExposedModel("Qwen/Managed"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed model folder still exists: %v", err)
	}
	if err := server.removeExposedModel("Qwen/Personal"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(userOwned); err != nil {
		t.Fatalf("user-owned model folder was removed: %v", err)
	}
}

func TestModelDownloadCancelStopsRegisteredJob(t *testing.T) {
	server := &Server{jobs: jobs.NewManager()}
	job := server.jobs.Create("model-dl:Qwen/Test")
	ctx, cancel := context.WithCancel(context.Background())
	server.registerModelJob(job.ID, cancel)

	request := httptest.NewRequest(http.MethodPost, "/api/models/download/cancel",
		strings.NewReader(`{"jobId":"`+job.ID+`"}`))
	recorder := httptest.NewRecorder()
	server.modelDownloadCancel(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("registered download context was not canceled")
	}
}

func TestExposeModelCacheUsesHardlinks(t *testing.T) {
	cloudlessHome := filepath.Join(t.TempDir(), "Cloudless")
	t.Setenv("CLOUDLESS_HOME", cloudlessHome)
	cache := t.TempDir()
	repoRoot := filepath.Join(cache, "hub", "models--Qwen--Visible")
	revision := "abc123"
	blob := filepath.Join(repoRoot, "blobs", "weights")
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob, []byte("weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(repoRoot, "snapshots", revision)
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "blobs", "weights"), filepath.Join(snapshot, "model.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "refs", "main"), []byte(revision), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exposeModelCache(cache, map[string]bool{"Qwen/Visible": true}); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(cloudlessHome, "Models", "Qwen--Visible", "model.bin")
	sourceInfo, err := os.Stat(blob)
	if err != nil {
		t.Fatal(err)
	}
	viewInfo, err := os.Stat(view)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, viewInfo) {
		t.Fatal("visible model file is not hard-linked to the cache")
	}
	viewDirInfo, err := os.Stat(filepath.Dir(view))
	if err != nil {
		t.Fatal(err)
	}
	if viewDirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("model view directory mode = %o, want 755", viewDirInfo.Mode().Perm())
	}
	markerInfo, err := os.Stat(filepath.Join(filepath.Dir(view), ".cloudless-revision"))
	if err != nil {
		t.Fatal(err)
	}
	if markerInfo.Mode().Perm() != 0o644 {
		t.Fatalf("model view marker mode = %o, want 644", markerInfo.Mode().Perm())
	}
}
