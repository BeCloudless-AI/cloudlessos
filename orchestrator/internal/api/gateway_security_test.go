package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestGatewayRateLimitIsPerKeyAndRefills(t *testing.T) {
	limiter := newGatewayRateLimiter()
	now := time.Unix(1000, 0)
	limiter.now = func() time.Time { return now }
	for index := 0; index < gatewayBurst; index++ {
		if allowed, _ := limiter.allow("key-a"); !allowed {
			t.Fatalf("burst request %d denied", index)
		}
	}
	if allowed, retry := limiter.allow("key-a"); allowed || retry < 1 {
		t.Fatalf("exhausted key allowed=%v retry=%d", allowed, retry)
	}
	if allowed, _ := limiter.allow("key-b"); !allowed {
		t.Fatal("one key exhausted another key's allowance")
	}
	now = now.Add(time.Second)
	if allowed, _ := limiter.allow("key-a"); !allowed {
		t.Fatal("rate-limited key did not refill")
	}
}

func TestGatewayAuditIsLocalBoundedAndSecretFree(t *testing.T) {
	dir := t.TempDir()
	log := newGatewayAuditLog(dir)
	log.Append(gatewayAuditEvent{
		Event: "gateway-auth", Outcome: "denied", Path: "/agent/v1/chat/completions",
		Detail: "invalid credential",
	})
	events, err := log.Latest(10)
	if err != nil || len(events) != 1 || events[0].Detail != "invalid credential" {
		t.Fatalf("audit history = %#v, %v", events, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "security-audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-cloudless-") {
		t.Fatal("audit log contains an API key secret")
	}
	if info, err := os.Stat(filepath.Join(dir, "security-audit.jsonl")); err != nil ||
		(runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("audit log permissions are unsafe: %v, %v", info, err)
	}
}

func TestGatewayRouteAllowListExcludesHermesAdministration(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
		agent  bool
		want   bool
	}{
		{http.MethodPost, "/v1/chat/completions", true, true},
		{http.MethodGet, "/v1/models", true, true},
		{http.MethodPost, "/v1/embeddings", false, true},
		{http.MethodPost, "/api/config", true, false},
		{http.MethodGet, "/v1/admin", true, false},
		{http.MethodDelete, "/v1/models", false, false},
	} {
		if got := gatewayRouteAllowed(test.method, test.path, test.agent); got != test.want {
			t.Fatalf("%s %s agent=%v allowed=%v, want %v", test.method, test.path, test.agent, got, test.want)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/agent/api/config", nil)
	recorder := httptest.NewRecorder()
	(&Server{}).GatewayHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("Hermes administrative route returned %d", recorder.Code)
	}
}

func TestGatewayAuthorizationRateLimitsAndAudits(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret, key, err := st.AddAPIKey("agent", state.APIKeyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: st}
	for index := 0; index < gatewayBurst; index++ {
		request := httptest.NewRequest(http.MethodPost, "/agent/v1/chat/completions", nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		if _, ok := server.authorizeGateway(httptest.NewRecorder(), request, state.APIKeyScopeAgent); !ok {
			t.Fatalf("request %d unexpectedly denied", index)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/agent/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	if _, ok := server.authorizeGateway(recorder, request, state.APIKeyScopeAgent); ok ||
		recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit response = %d, retry=%q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
	_, audit := server.gatewaySecurity()
	events, err := audit.Latest(10)
	if err != nil || len(events) == 0 || events[0].Event != "gateway-rate-limit" || events[0].KeyID != key.ID {
		t.Fatalf("rate-limit audit = %#v, %v", events, err)
	}
}

func TestInvalidCredentialsAreRateLimitedBySource(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{state: st}
	for index := 0; index < 30; index++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		request.RemoteAddr = "203.0.113.4:51000"
		request.Header.Set("Authorization", "Bearer invalid")
		recorder := httptest.NewRecorder()
		server.authorizeGateway(recorder, request, state.APIKeyScopeModel)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("invalid request %d status = %d", index, recorder.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.RemoteAddr = "203.0.113.4:51001"
	recorder := httptest.NewRecorder()
	server.authorizeGateway(recorder, request, state.APIKeyScopeModel)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("source flood status = %d", recorder.Code)
	}
}

func TestGatewayExposureRequiresExplicitConfirmation(t *testing.T) {
	for name, test := range map[string]struct {
		method  string
		path    string
		handler http.HandlerFunc
	}{
		"LAN exposure":    {http.MethodPost, "/api/gateway/lan", (&Server{}).gatewayLanSet},
		"public exposure": {http.MethodPost, "/api/gateway/tunnel", (&Server{}).gatewayTunnelSet},
		"key creation":    {http.MethodPost, "/api/keys", (&Server{}).keyCreate},
		"key revocation":  {http.MethodDelete, "/api/keys/example", (&Server{}).keyDelete},
		"API identity":    {http.MethodPost, "/api/settings/inference-contract", (&Server{}).inferenceContractSet},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.handler(recorder, httptest.NewRequest(test.method, test.path, strings.NewReader(`{"enable":true}`)))
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("%s without confirmation = %d", test.path, recorder.Code)
			}
		})
	}
}
