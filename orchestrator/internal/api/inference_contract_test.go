package api

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestInferenceContractValidationRejectsReservedAndUnsafeValues(t *testing.T) {
	for _, contract := range []state.InferenceContract{
		{Port: 8000, ModelAlias: "cloudless"},
		{Port: 8765, ModelAlias: "cloudless"},
		{Port: 18766, ModelAlias: "has spaces"},
		{Port: 80, ModelAlias: "cloudless"},
	} {
		if err := validateInferenceContract(contract); err == nil {
			t.Fatalf("accepted unsafe contract %#v", contract)
		}
	}
	if err := validateInferenceContract(state.InferenceContract{Port: 18766, ModelAlias: "office/cloudless-v2"}); err != nil {
		t.Fatalf("rejected valid contract: %v", err)
	}
}

func TestEngineEndpointContractRequiresOpenAIModelAlias(t *testing.T) {
	if err := engineModelsResponseError(strings.NewReader(`{"object":"list","data":[{"id":"rogue-model"}]}`)); err == nil || !strings.Contains(err.Error(), `required model name "cloudless"`) {
		t.Fatalf("engineModelsResponseError = %v, want stable model-name failure", err)
	}
	if err := engineModelsResponseError(strings.NewReader(`{"object":"list","data":[{"id":"cloudless"}]}`)); err != nil {
		t.Fatalf("engineModelsResponseError rejected stable alias: %v", err)
	}
}

func TestRecipeCannotOverridePrivateGatewayContract(t *testing.T) {
	server := &Server{}
	path, alias := server.activeRecipeGatewaySettings()
	if path != "/v1" || alias != localrecipes.CloudlessModelAlias {
		t.Fatalf("private gateway contract = %q %q", path, alias)
	}
}

func TestGatewayAliasBodyRewritesJSONAndStreamingLines(t *testing.T) {
	source := io.NopCloser(strings.NewReader("{\"id\":\"cloudless\",\"object\":\"model\"}\n" +
		"data: {\"model\":\"cloudless\",\"choices\":[]}\n"))
	body := &gatewayAliasBody{src: source, reader: bufio.NewReader(source), alias: "office-ai"}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, `"cloudless"`) || !strings.Contains(text, `"office-ai"`) {
		t.Fatalf("alias rewrite failed: %s", text)
	}
}
