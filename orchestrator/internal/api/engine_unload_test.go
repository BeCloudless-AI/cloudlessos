package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEngineUnloadRequiresExplicitActionHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Server{}).engineUnload(recorder, httptest.NewRequest(http.MethodPost, "/api/engine/unload", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestEngineAbortRequiresExplicitActionHeader(t *testing.T) {
	recorder := httptest.NewRecorder()
	(&Server{}).engineAbort(recorder, httptest.NewRequest(http.MethodPost, "/api/engine/abort", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestCancelEngineJobsCancelsEveryQueuedLaunch(t *testing.T) {
	server := &Server{}
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	server.registerEngineJob("job-a", cancelA)
	server.registerEngineJob("job-b", cancelB)
	if got := server.cancelEngineJobs(); got != 2 {
		t.Fatalf("canceled %d jobs, want 2", got)
	}
	for name, ctx := range map[string]context.Context{"job-a": ctxA, "job-b": ctxB} {
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("%s was not canceled", name)
		}
	}
}
