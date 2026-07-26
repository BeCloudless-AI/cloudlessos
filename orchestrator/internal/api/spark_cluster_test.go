package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestSparkClusterDeniedOnGenericSystem(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	t.Setenv("CLOUDLESS_ARCH", "amd64")
	rec := httptest.NewRecorder()
	(&Server{}).sparkClusterStatus(rec, httptest.NewRequest(http.MethodGet, "/api/system/spark-cluster", nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "spark-cluster") {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSparkClusterCreateRequiresExplicitConfirmation(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	rec := httptest.NewRecorder()
	(&Server{}).sparkClusterCreate(rec, httptest.NewRequest(http.MethodPost, "/api/system/spark-cluster/create", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSparkClusterDisconnectRequiresExplicitConfirmation(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	rec := httptest.NewRecorder()
	(&Server{}).sparkClusterDisconnect(rec, httptest.NewRequest(http.MethodPost, "/api/system/spark-cluster/disconnect", strings.NewReader(`{}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestClusterDisconnectKeepsSingleSparkCompatibleModel(t *testing.T) {
	model, changed := localModelAfterClusterDisconnect(state.State{Model: "Qwen/Qwen3.6-35B-A3B"}, 120)
	if changed || model != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("fallback = %q, changed = %v", model, changed)
	}
}

func TestClusterDisconnectFallsBackFromClusterOnlyModel(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	model, changed := localModelAfterClusterDisconnect(state.State{Model: "deepseek-ai/DeepSeek-V4-Flash"}, 120)
	if !changed || model != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("fallback = %q, changed = %v", model, changed)
	}
}
