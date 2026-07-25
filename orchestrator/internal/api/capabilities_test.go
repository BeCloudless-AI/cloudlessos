package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/platform"
)

func TestCapabilitiesAPIExposesAuthoritativePlatformVerdicts(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	t.Setenv("CLOUDLESS_ARCH", "arm64")
	t.Setenv("CLOUDLESS_VERSION", "0.3.0")
	rec := httptest.NewRecorder()
	(&Server{}).capabilities(rec, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`"platform":"dgx-spark"`,
		`"architecture":"arm64"`,
		`"cloudlessVersion":"0.3.0"`,
		`"dgx-appliance":{"available":true`,
		`"generic-nvidia-driver-management":{"available":false`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("capabilities response missing %q: %s", want, body)
		}
	}
}
