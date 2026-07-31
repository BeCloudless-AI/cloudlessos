package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/state"
)

type customEngineInspectionEngine struct {
	engine.Engine
	architecture string
}

func (e customEngineInspectionEngine) Output(_ context.Context, args ...string) (string, error) {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, ".Config.Entrypoint") {
		return `["vllm","serve"]`, nil
	}
	return "sha256:immutable " + e.architecture, nil
}

func (e customEngineInspectionEngine) InspectImage(context.Context, string) (engine.ImageInfo, error) {
	return engine.ImageInfo{
		ID: "sha256:immutable", Architecture: e.architecture,
		EntryPoint: []string{"vllm", "serve"},
	}, nil
}

func TestCustomEngineRegistrationCreatesImmutableProfile(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, eng: customEngineInspectionEngine{architecture: runtime.GOARCH}}
	request := httptest.NewRequest(http.MethodPost, "/api/engines/custom", strings.NewReader(
		`{"name":"SM121 vLLM","image":"cloudless/vllm-sm121:dev","base":"vllm"}`,
	))
	recorder := httptest.NewRecorder()
	server.customEngineCreate(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Engine state.CustomEngine `json:"engine"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	got := response.Engine
	if got.ResolvedImage != "sha256:immutable" || got.ImageDigest != "sha256:immutable" ||
		got.Architecture != runtime.GOARCH || got.ProfileVersion != 1 ||
		got.ContractVersion != "cloudless-openai-v1" || got.ValidationStatus != "registered" ||
		got.CommandMode != "vllm-entrypoint" {
		t.Fatalf("unexpected registered profile: %#v", got)
	}
}

func TestCustomEngineRegistrationRejectsWrongArchitecture(t *testing.T) {
	other := "amd64"
	if runtime.GOARCH == other {
		other = "arm64"
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store, eng: customEngineInspectionEngine{architecture: other}}
	request := httptest.NewRequest(http.MethodPost, "/api/engines/custom", strings.NewReader(
		`{"name":"Wrong architecture","image":"cloudless/wrong:dev","base":"vllm"}`,
	))
	recorder := httptest.NewRecorder()
	server.customEngineCreate(recorder, request)
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "built for") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(store.CustomEngineList()) != 0 {
		t.Fatal("incompatible architecture was registered")
	}
}
