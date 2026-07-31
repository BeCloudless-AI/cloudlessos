package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestAppExposureMutationsRequireExplicitActionHeaders(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}

	public := httptest.NewRequest(http.MethodPost, "/api/apps/open-webui/tunnel", strings.NewReader(`{"enable":true}`))
	public.SetPathValue("id", "open-webui")
	response := httptest.NewRecorder()
	server.tunnelSet(response, public)
	if response.Code != http.StatusForbidden {
		t.Fatalf("public exposure without action header = %d %s", response.Code, response.Body.String())
	}

	lan := httptest.NewRequest(http.MethodPost, "/api/apps/open-webui/lan", strings.NewReader(`{"enable":true}`))
	lan.SetPathValue("id", "open-webui")
	response = httptest.NewRecorder()
	server.lanSet(response, lan)
	if response.Code != http.StatusForbidden {
		t.Fatalf("LAN exposure without action header = %d %s", response.Code, response.Body.String())
	}
}

func TestPublicExposureRequiresDemonstrablyEnabledAuthentication(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	openWebUI, _ := catalog.Get("open-webui")
	if server.appPublicAuthReady(openWebUI) {
		t.Fatal("Open WebUI default no-login mode was accepted for public exposure")
	}
	configDir := server.appConfigDir(openWebUI.ID)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "webui.env"), []byte("WEBUI_AUTH=True\nENABLE_SIGNUP=False\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !server.appPublicAuthReady(openWebUI) {
		t.Fatal("authenticated Open WebUI configuration was rejected")
	}
	if aiToolkit, ok := catalog.Get("ai-toolkit"); ok && !server.appPublicAuthReady(aiToolkit) {
		t.Fatal("AI Toolkit machine-generated password was not accepted")
	}
	if unsloth, ok := catalog.Get("unsloth"); ok && !server.appPublicAuthReady(unsloth) {
		t.Fatal("Unsloth machine-generated password was not accepted")
	}
}

func TestSettingsResolveManagedPasswordWithoutPublishingItInCatalog(t *testing.T) {
	app, ok := catalog.Get("ai-toolkit")
	if !ok {
		t.Skip("AI Toolkit is not available on this architecture")
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: store}
	request := httptest.NewRequest(http.MethodGet, "/api/apps/ai-toolkit/settings", nil)
	request.SetPathValue("id", app.ID)
	response := httptest.NewRecorder()
	server.appSettingsGet(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "cloudless-managed://") ||
		!strings.Contains(response.Body.String(), `"pass":"cloudless-ai-toolkit-`) {
		t.Fatalf("managed credential was not resolved safely: %s", response.Body.String())
	}
	if app.Admin.Pass != "" || app.Admin.ManagedPassword == "" {
		t.Fatalf("catalog exposes a fixed password: %#v", app.Admin)
	}
}
