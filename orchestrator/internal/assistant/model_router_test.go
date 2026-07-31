package assistant

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSemanticModelControlRoutesUnknownModelFamily(t *testing.T) {
	server := classifierServer(t, "MODEL_CONTROL")
	defer server.Close()
	handled, err := SemanticModelControl(context.Background(), server.URL, "cloudless", []Msg{{Role: "user", Content: "Would Command R+ be usable on this computer?"}})
	if err != nil || !handled {
		t.Fatalf("semantic model request not routed: handled=%v err=%v", handled, err)
	}
}

func TestSemanticModelControlLeavesGeneralQuestionAlone(t *testing.T) {
	server := classifierServer(t, "OTHER")
	defer server.Close()
	handled, err := SemanticModelControl(context.Background(), server.URL, "cloudless", []Msg{{Role: "user", Content: "Why do mixture-of-experts models exist?"}})
	if err != nil || handled {
		t.Fatalf("general question was routed: handled=%v err=%v", handled, err)
	}
}

func TestMayNeedModelRoutingRejectsOrdinaryFormattingRequest(t *testing.T) {
	if MayNeedModelRouting("Reply with **Ready** and a two-item bullet list.") {
		t.Fatal("ordinary chat was admitted to the model-control classifier")
	}
}

func TestMayNeedModelRoutingAcceptsUnknownCompatibilityRequest(t *testing.T) {
	if !MayNeedModelRouting("Would Command R+ be usable on this computer?") {
		t.Fatal("unknown model compatibility request was not admitted to semantic routing")
	}
}

func TestMayNeedModelRoutingAcceptsExplicitModelConceptQuestion(t *testing.T) {
	if !MayNeedModelRouting("Why do mixture-of-experts models exist?") {
		t.Fatal("explicit model question should reach semantic classification")
	}
}

func TestRecentUserTextExcludesAssistantClaims(t *testing.T) {
	text := RecentUserText([]Msg{
		{Role: "user", Content: "Will acme/Model-40B-AWQ fit?"},
		{Role: "assistant", Content: "It only needs 2 GB."},
		{Role: "user", Content: "Are you sure?"},
	}, 3)
	if strings.Contains(text, "2 GB") || !strings.Contains(text, "40B-AWQ") || !strings.Contains(text, "Are you sure") {
		t.Fatalf("unsafe follow-up context: %q", text)
	}
	advice := ForcedModelGuidance(text, modelTestContext())
	for _, want := range []string{"can’t determine", "parameter count", "Not reviewed"} {
		if !strings.Contains(advice.Reply, want) {
			t.Fatalf("forced advice did not preserve evidence boundaries: %q", advice.Reply)
		}
	}
	if strings.Contains(advice.Reply, "2 GB") || strings.Contains(advice.Reply, "26 GB") {
		t.Fatalf("forced advice invented or repeated a memory claim: %q", advice.Reply)
	}
}

func TestRecentUserTextDoesNotLeakAnOlderModelIntoNewRequest(t *testing.T) {
	text := RecentUserText([]Msg{
		{Role: "user", Content: "Will Qwen2.5 14B fit?"},
		{Role: "assistant", Content: "Yes."},
		{Role: "user", Content: "Would Command R+ be usable on this computer?"},
	}, 3)
	if text != "Would Command R+ be usable on this computer?" {
		t.Fatalf("new request inherited stale model context: %q", text)
	}
}

func classifierServer(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("classifier path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, answer)
	}))
}
