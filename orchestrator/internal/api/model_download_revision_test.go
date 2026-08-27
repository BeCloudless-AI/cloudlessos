package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestHuggingFaceContentMissingSeparatesMissingFromTransient(t *testing.T) {
	// Only a definitive 404 may shortcut a download. A gated repository, a rate
	// limit or an outage must still reach the helper container, which runs with
	// the same credentials and may succeed where the metadata call did not.
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"missing revision", &huggingFaceStatusError{StatusCode: http.StatusNotFound}, true},
		{"wrapped missing revision", fmt.Errorf("inventory: %w", &huggingFaceStatusError{StatusCode: http.StatusNotFound}), true},
		{"gated repository", &huggingFaceStatusError{StatusCode: http.StatusUnauthorized}, false},
		{"forbidden repository", &huggingFaceStatusError{StatusCode: http.StatusForbidden}, false},
		{"rate limited", &huggingFaceStatusError{StatusCode: http.StatusTooManyRequests}, false},
		{"hub outage", &huggingFaceStatusError{StatusCode: http.StatusBadGateway}, false},
		{"transport failure", errors.New("dial tcp: connection refused"), false},
		{"no error", nil, false},
	}
	for _, testCase := range cases {
		if got := huggingFaceContentMissing(testCase.err); got != testCase.want {
			t.Errorf("%s: huggingFaceContentMissing = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestHuggingFaceStatusErrorKeepsItsExistingMessage(t *testing.T) {
	err := &huggingFaceStatusError{StatusCode: http.StatusNotFound, Repo: "owner/model"}
	if got := err.Error(); got != "Hugging Face model metadata returned HTTP 404" {
		t.Fatalf("status error message changed: %q", got)
	}
}

func TestMissingHuggingFaceContentErrorNamesTheFieldToCorrect(t *testing.T) {
	revision := "60e813d4dbbdc5d64cf3f5a8caf2897bedf03679"
	pinned := missingHuggingFaceContentError("unsloth/Qwen3.8-27B-NVFP4", revision).Error()
	for _, want := range []string{"unsloth/Qwen3.8-27B-NVFP4", revision, "pinned revision", "repository head"} {
		if !strings.Contains(pinned, want) {
			t.Errorf("pinned-revision failure omits %q: %s", want, pinned)
		}
	}
	// A traceback names a Python frame; an operator needs the field to edit.
	if strings.Contains(pinned, "Traceback") {
		t.Error("pinned-revision failure leaked a traceback")
	}
	whole := missingHuggingFaceContentError("owner/model", "  ").Error()
	if !strings.Contains(whole, "repository owner/model") {
		t.Errorf("missing-repository failure does not name the repository: %s", whole)
	}
	if strings.Contains(whole, "revision") {
		t.Errorf("missing-repository failure blames a revision that was never pinned: %s", whole)
	}
}

func TestModelDownloadFailureToastSurfacesTheReason(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function modelDownloadFailureReason(raw)`,
		`function modelDownloadFailureMessage(name, raw)`,
		`toast(modelDownloadFailureMessage(m.name, reason || completed.error), true)`,
		`if (u.phase === 'error') return finish('failed', u.error);`,
		`finish(update.phase === 'error' ? 'failed' : update.phase === 'canceled' ? 'canceled' : 'done', update.error)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("interface no longer surfaces the download failure reason: %s", want)
		}
	}
	// A bare status toast leaves the operator without the reason. Both the
	// language-model and the diffusion tracker must pass the job error through.
	if strings.Contains(page, `' download failed'), status === 'failed')`) {
		t.Error("a download tracker still discards the job error")
	}
	if got := strings.Count(page, `toast(modelDownloadFailureMessage(`); got != 2 {
		t.Errorf("expected both download trackers to report the reason, found %d", got)
	}
}
