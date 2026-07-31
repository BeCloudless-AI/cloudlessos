package api

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyRecipeDependencyErrorProvidesActionableRemedies(t *testing.T) {
	tests := []struct {
		message   string
		kind      string
		retryable bool
		remedy    string
	}{
		{"HTTP 401 Unauthorized", "authentication failed", false, "Hugging Face account"},
		{"HTTP 429 Too Many Requests", "was rate-limited", true, "preserved"},
		{"Revision Not Found: abc", "artifact was not found", false, "immutable revision"},
		{"write: no space left on device", "ran out of storage", false, "active model cache"},
		{"incomplete pinned snapshot", "failed integrity verification", true, "rejected"},
		{"connection reset by peer", "was interrupted by the network", true, "resume"},
	}
	for _, test := range tests {
		classified := classifyRecipeDependencyError(errors.New(test.message))
		var dependency *recipeDependencyError
		if !errors.As(classified, &dependency) || dependency.Kind != test.kind || dependency.Retryable != test.retryable || !strings.Contains(dependency.Remedy, test.remedy) {
			t.Errorf("classification for %q = %#v", test.message, dependency)
		}
	}
}
