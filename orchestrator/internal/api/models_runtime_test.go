package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/platform"
)

func TestReviewedSingleNodeModelRejectsClusterLaunch(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/settings/model",
		strings.NewReader(`{"model":"nvidia/LocateAnything-3B","mode":"cluster"}`))
	rec := httptest.NewRecorder()
	(&Server{}).settingsModel(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "single-Spark") {
		t.Fatalf("cluster launch was not rejected: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDistributedLaunchRequiresExactReviewedTopology(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	if _, err := reviewedDistributedProfile("Qwen/Qwen3.6-35B-A3B", "vllm", 2); err != nil {
		t.Fatalf("qualified Spark pair rejected: %v", err)
	}
	for name, test := range map[string]struct {
		model    string
		engineID string
		nodes    int
	}{
		"unreviewed topology": {"Qwen/Qwen3.6-35B-A3B", "vllm", 3},
		"unreviewed model":    {"Qwen/Qwen2.5-3B-Instruct", "vllm", 2},
		"unreviewed engine":   {"Qwen/Qwen3.6-35B-A3B", "sglang", 2},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := reviewedDistributedProfile(test.model, test.engineID, test.nodes); err == nil {
				t.Fatal("unreviewed distributed launch was accepted")
			}
		})
	}
}

func TestDistributedImageNeverFallsBackToMutableTag(t *testing.T) {
	target, ok := catalog.Get("vllm")
	if !ok {
		t.Fatal("vLLM catalog entry missing")
	}
	server := &Server{}
	if _, err := server.reviewedDistributedImage(context.Background(), target, target.Image); err == nil {
		t.Fatal("mutable engine tag was accepted")
	}
	const exact = "example.invalid/vllm@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got, err := server.reviewedDistributedImage(context.Background(), target, exact); err != nil || got != exact {
		t.Fatalf("verified exact image rejected: got=%q err=%v", got, err)
	}
}
