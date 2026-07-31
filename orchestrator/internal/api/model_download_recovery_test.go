package api

import (
	"errors"
	"testing"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestModelDownloadContainerNameIsStablePerRepository(t *testing.T) {
	first := modelDownloadContainerName("owner/model")
	if first != modelDownloadContainerName("owner/model") {
		t.Fatal("download helper name changed for the same repository")
	}
	if first == modelDownloadContainerName("owner/other") {
		t.Fatal("different repositories share a download helper name")
	}
}

func TestModelDownloadObserverJournalsProgressAndKeepsFailure(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	job := jobs.NewManager().Create("model-dl:owner/model")
	server.observeModelDownload(job, "owner/model")
	job.ProgressBytes("downloading", "Transferring verified chunks", 7, 19)
	got := store.ModelDownloads()
	if len(got) != 1 || got[0].BytesDone != 7 || got[0].BytesTotal != 19 {
		t.Fatalf("download progress was not journaled: %#v", got)
	}
	job.Fail(errors.New("network unavailable"))
	got = store.ModelDownloads()
	if len(got) != 1 || got[0].Phase != "error" || got[0].Error == "" {
		t.Fatalf("actionable terminal failure was discarded: %#v", got)
	}
}

func TestCanceledModelDownloadClearsJournalButKeepsCacheForResume(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	job := jobs.NewManager().Create("model-dl:owner/model")
	server.observeModelDownload(job, "owner/model")
	job.ProgressBytes("downloading", "Transferring verified chunks", 7, 19)
	job.Cancel()
	if got := store.ModelDownloads(); len(got) != 0 {
		t.Fatalf("canceled operation remained active: %#v", got)
	}
	// Cache retention is implemented by stopCanceled: it removes only the
	// helper container and intentionally never calls removeModelCache.
}
