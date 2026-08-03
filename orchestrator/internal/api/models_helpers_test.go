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

func TestImmutableHubRevisionRequiresFullCommitHash(t *testing.T) {
	for _, valid := range []string{
		"0123456789abcdef0123456789abcdef01234567",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if !immutableHubRevision(valid) {
			t.Errorf("immutableHubRevision(%q) rejected a commit hash", valid)
		}
	}
	for _, invalid := range []string{"", "main", "0123456789abcdef0123456789abcdef0123456g", "ABCDEF0123456789ABCDEF0123456789ABCDEF01"} {
		if immutableHubRevision(invalid) {
			t.Errorf("immutableHubRevision(%q) accepted a mutable or malformed revision", invalid)
		}
	}
}

func TestModelRevisionBytesIgnoresOtherCachedRevisions(t *testing.T) {
	root := t.TempDir()
	repoRoot := filepath.Join(root, "hub", "models--owner--model")
	wanted := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	other := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := os.MkdirAll(filepath.Join(repoRoot, "snapshots", wanted), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "snapshots", other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "snapshots", wanted, "config.json"), make([]byte, 7), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "snapshots", other, "model.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory := map[string]huggingFaceRevisionFile{
		"config.json": {Size: 7},
		"model.bin":   {Size: 200, BlobID: "wanted-blob"},
	}
	if got := modelRevisionBytes("owner/model", wanted, root, inventory); got != 7 {
		t.Fatalf("revision progress = %d, want 7; another revision leaked into progress", got)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "blobs", "wanted-blob.incomplete"), make([]byte, 11), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := modelRevisionBytes("owner/model", wanted, root, inventory); got != 18 {
		t.Fatalf("revision progress with resumable chunk = %d, want 18", got)
	}
}

func TestCompletedRevisionSurvivesUnrelatedResumableChunks(t *testing.T) {
	root := t.TempDir()
	revision := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repoRoot := filepath.Join(root, "hub", "models--owner--model")
	if err := os.MkdirAll(filepath.Join(repoRoot, "snapshots", revision), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "snapshots", revision, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "blobs", "other.incomplete"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := markModelRevisionComplete(root, "owner/model", revision); err != nil {
		t.Fatal(err)
	}
	if !modelCacheComplete(repoRoot) {
		t.Fatal("an unrelated resumable chunk invalidated the completed revision")
	}
	main, err := os.ReadFile(filepath.Join(repoRoot, "refs", "main"))
	if err != nil || strings.TrimSpace(string(main)) != revision {
		t.Fatalf("current cache revision = %q, %v", main, err)
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

func TestModelDownloadRejectsMutableAndConflictingRevisions(t *testing.T) {
	server := &Server{jobs: jobs.NewManager()}
	request := httptest.NewRequest(http.MethodPost, "/api/models/download",
		strings.NewReader(`{"id":"owner/model","revision":"main"}`))
	recorder := httptest.NewRecorder()
	server.modelDownload(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("mutable revision status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	job := server.jobs.Create("model-dl:owner/model")
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.registerModelDownloadJob(job.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", cancel)
	request = httptest.NewRequest(http.MethodPost, "/api/models/download",
		strings.NewReader(`{"id":"owner/model","revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
	recorder = httptest.NewRecorder()
	server.modelDownload(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflicting revision status = %d, body = %s", recorder.Code, recorder.Body.String())
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
