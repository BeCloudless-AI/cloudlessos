package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/remoteaccess"
	"github.com/cloudless/orchestrator/internal/state"
)

type fakeRemoteAccess struct {
	status    remoteaccess.Status
	authURL   string
	installed bool
	loggedOut bool
	ssh       bool
	serve     bool
}

func (f *fakeRemoteAccess) Status(context.Context) remoteaccess.Status   { return f.status }
func (f *fakeRemoteAccess) Connect(context.Context) (string, error)      { return f.authURL, nil }
func (f *fakeRemoteAccess) Logout(context.Context) error                 { f.loggedOut = true; return nil }
func (f *fakeRemoteAccess) SetSSH(_ context.Context, enabled bool) error { f.ssh = enabled; return nil }
func (f *fakeRemoteAccess) SetServe(_ context.Context, enabled bool) error {
	f.serve = enabled
	return nil
}
func (f *fakeRemoteAccess) Install(context.Context) error { f.installed = true; return nil }

func TestTailscaleRoutesKeepCredentialsOutsideCloudless(t *testing.T) {
	fake := &fakeRemoteAccess{status: remoteaccess.Status{Installed: true}, authURL: "https://login.tailscale.com/a/example"}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{remoteAccess: fake, state: store}
	routes := s.Routes()

	request := httptest.NewRequest(http.MethodPost, "/api/system/tailscale/connect", nil)
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("connect without confirmation = %d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/system/tailscale/connect", nil)
	request.Header.Set("X-Cloudless-Action", "tailscale-connect")
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), fake.authURL) {
		t.Fatalf("connect response = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/system/tailscale/serve", strings.NewReader(`{"enabled":true}`))
	request.Header.Set("X-Cloudless-Action", "tailscale-serve")
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !fake.serve {
		t.Fatalf("serve response = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/system/tailscale/logout", nil)
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || fake.loggedOut {
		t.Fatal("logout accepted without confirmation header")
	}

	response = httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/security/audit", nil))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"category":"remote-access"`) ||
		!strings.Contains(response.Body.String(), `"event":"tailscale-connect"`) {
		t.Fatalf("security audit response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), fake.authURL) {
		t.Fatalf("Tailscale authorization URL leaked into the audit: %s", response.Body.String())
	}
}

func TestBrowserRequestIsValidatedAndQueued(t *testing.T) {
	old := browserRequestDir
	browserRequestDir = t.TempDir()
	t.Cleanup(func() { browserRequestDir = old })
	s := &Server{}

	request := httptest.NewRequest(http.MethodPost, "/api/system/browser", strings.NewReader(`{"url":"example.com/docs"}`))
	response := httptest.NewRecorder()
	s.systemBrowserOpen(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("open response = %d %s", response.Code, response.Body.String())
	}
	entries, err := osReadDir(browserRequestDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("queued requests = %d, err %v", len(entries), err)
	}
	payload, err := osReadFile(browserRequestDir + "/" + entries[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("browser request mode = %o, want 660", info.Mode().Perm())
	}
	var queued map[string]string
	if json.Unmarshal(payload, &queued) != nil || queued["url"] != "https://example.com/docs" {
		t.Fatalf("queued payload = %s", payload)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/system/browser", strings.NewReader(`{"url":"file:///etc/passwd"}`))
	response = httptest.NewRecorder()
	s.systemBrowserOpen(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe URL response = %d", response.Code)
	}
}

func TestBrowserStatusAndRestoreRequest(t *testing.T) {
	oldRequests, oldStatus := browserRequestDir, browserStatusPath
	browserRequestDir = t.TempDir()
	browserStatusPath = browserRequestDir + "/status.json"
	t.Cleanup(func() { browserRequestDir, browserStatusPath = oldRequests, oldStatus })
	if err := os.WriteFile(browserStatusPath, []byte(`{"running":true,"minimized":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	response := httptest.NewRecorder()
	s.systemBrowserStatus(response, httptest.NewRequest(http.MethodGet, "/api/system/browser", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"running":true`) || !strings.Contains(response.Body.String(), `"minimized":true`) ||
		!strings.Contains(response.Body.String(), `"profilePersistence":"persistent"`) || !strings.Contains(response.Body.String(), `"downloadsPath":"/home/cloudless/Downloads"`) {
		t.Fatalf("status response = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status cache policy = %q", response.Header().Get("Cache-Control"))
	}

	response = httptest.NewRecorder()
	s.systemBrowserOpen(response, httptest.NewRequest(http.MethodPost, "/api/system/browser", strings.NewReader(`{"action":"show"}`)))
	if response.Code != http.StatusAccepted {
		t.Fatalf("restore response = %d %s", response.Code, response.Body.String())
	}
	entries, _ := os.ReadDir(browserRequestDir)
	for _, entry := range entries {
		if entry.Name() == "status.json" {
			continue
		}
		payload, _ := os.ReadFile(browserRequestDir + "/" + entry.Name())
		if strings.Contains(string(payload), `"action":"show"`) {
			return
		}
	}
	t.Fatal("show request was not queued")
}

// Indirections keep the test's filesystem use explicit without widening the
// production browser handler API.
var osReadDir = os.ReadDir
var osReadFile = os.ReadFile
