package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseUsageJSON(t *testing.T) {
	prompt, completion := responseUsage([]byte(`{"usage":{"prompt_tokens":123,"completion_tokens":45,"total_tokens":168}}`))
	if prompt != 123 || completion != 45 {
		t.Fatalf("got (%d, %d), want (123, 45)", prompt, completion)
	}
}

func TestResponseUsageStreamingAndResponsesAPI(t *testing.T) {
	body := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"usage\":{\"input_tokens\":88,\"output_tokens\":21}}\n\n" +
		"data: [DONE]\n\n")
	prompt, completion := responseUsage(body)
	if prompt != 88 || completion != 21 {
		t.Fatalf("got (%d, %d), want (88, 21)", prompt, completion)
	}
}

func TestStatusRecBoundsUsageCapture(t *testing.T) {
	r := &statusRec{ResponseWriter: httptest.NewRecorder(), status: 200}
	chunk := make([]byte, usageCaptureLimit+100)
	copy(chunk[len(chunk)-70:], []byte(`,"usage":{"prompt_tokens":31,"completion_tokens":12}}`))
	if _, err := r.Write(chunk); err != nil {
		t.Fatal(err)
	}
	if len(r.capture) != usageCaptureLimit {
		t.Fatalf("capture=%d, want %d", len(r.capture), usageCaptureLimit)
	}
	prompt, completion := responseUsage(r.capture)
	if prompt != 31 || completion != 12 {
		t.Fatalf("tail usage = (%d, %d), want (31, 12)", prompt, completion)
	}
}

func TestRewriteModelRequestsStreamingUsage(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"client-alias","stream":true,"stream_options":{"include_usage":false}}`))
	rewriteModel(request, servedModelName)
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	options, _ := payload["stream_options"].(map[string]any)
	if payload["model"] != servedModelName || options["include_usage"] != true {
		t.Fatalf("rewritten payload = %s", body)
	}
}
