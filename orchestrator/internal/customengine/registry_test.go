package customengine

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestAppInheritsVLLMContract(t *testing.T) {
	app, ok := App(state.CustomEngine{ID: "custom-sm121", Name: "SM121 vLLM", Image: "cloudless/vllm-sm121:dev", Base: "vllm", CommandMode: "vllm-entrypoint"})
	if !ok || app.ID != "custom-sm121" || app.Image != "cloudless/vllm-sm121:dev" || !app.Engine {
		t.Fatalf("unexpected custom engine: %#v", app)
	}
	if app.Preinstall || app.Prefetch || app.Verified {
		t.Fatal("a local build must not masquerade as a managed verified engine")
	}
	if app.Network != "cloudless" || app.GPUs != "all" || app.Ports[8000] != 8000 {
		t.Fatal("custom engine lost the managed runtime contract")
	}
	if len(app.Command) > 1 && app.Command[0] == "vllm" && app.Command[1] == "serve" {
		t.Fatal("vLLM entrypoint was duplicated in the custom launch command")
	}
}
