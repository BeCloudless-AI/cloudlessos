package assistant

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestStreamForwardsHermesTextAndToolProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer internal-secret" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: hermes.tool.progress\n")
		fmt.Fprint(w, "data: {\"tool\":\"terminal\",\"label\":\"Checking files\",\"toolCallId\":\"call-1\",\"status\":\"running\"}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"}}]}\n\n")
		fmt.Fprint(w, "event: hermes.tool.progress\n")
		fmt.Fprint(w, "data: {\"tool\":\"terminal\",\"label\":\"Checking files\",\"toolCallId\":\"call-1\",\"status\":\"completed\"}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	var text string
	var tools []ToolProgress
	err := Stream(context.Background(), server.URL, "internal-secret", "hermes-agent", []Msg{{Role: "user", Content: "hi"}}, func(delta string) {
		text += delta
	}, func(progress ToolProgress) {
		tools = append(tools, progress)
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello world" {
		t.Fatalf("text = %q", text)
	}
	if got, want := []string{tools[0].Status, tools[1].Status}, []string{"running", "completed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool statuses = %#v, want %#v", got, want)
	}
}
