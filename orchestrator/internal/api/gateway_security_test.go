package api

import (
	"encoding/json"
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

func TestAPIKeyLimitsCanBeUpdatedIndependently(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := st.AddAPIKey("client", state.APIKeyScopeBoth)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/api/keys/"+key.ID, strings.NewReader(`{"limits":{"tokensPerMinute":12000,"requestsPerMinute":30,"maxParallelRequests":2,"modelTokensPerMinute":8000,"modelRequestsPerMinute":20}}`))
	request.SetPathValue("id", key.ID)
	request.Header.Set("X-Cloudless-Action", "gateway-key-update")
	recorder := httptest.NewRecorder()
	(&Server{state: st}).keyUpdate(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var view keyView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Limits.RequestsPerMinute != 30 || view.Limits.ModelTokensPerMinute != 8000 || view.Scope != state.APIKeyScopeBoth {
		t.Fatalf("updated view = %+v", view)
	}
	limits, ok := st.APIKeyLimitsFor(key.ID)
	if !ok || limits.MaxParallelRequests != 2 || limits.ModelRequestsPerMinute != 20 {
		t.Fatalf("stored limits = %+v, %v", limits, ok)
	}
}

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
	if _, err := st.UpdateAPIKeyLimits(key.ID, state.APIKeyLimits{RequestsPerMinute: 2}); err != nil {
		t.Fatal(err)
	}
	server := &Server{state: st}
	proxy := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for index := 0; index < 2; index++ {
		request := httptest.NewRequest(http.MethodPost, "/agent/v1/chat/completions", nil)
		request.Header.Set("Authorization", "Bearer "+secret)
		recorder := httptest.NewRecorder()
		id, ok := server.authorizeGateway(recorder, request, state.APIKeyScopeAgent)
		if !ok {
			t.Fatalf("request %d unexpectedly denied", index)
		}
		server.serveGatewayProxy(recorder, request, id, state.APIKeyScopeAgent, proxy)
	}
	request := httptest.NewRequest(http.MethodPost, "/agent/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	recorder := httptest.NewRecorder()
	id, ok := server.authorizeGateway(recorder, request, state.APIKeyScopeAgent)
	if !ok {
		t.Fatal("configured rate limit incorrectly failed authentication")
	}
	server.serveGatewayProxy(recorder, request, id, state.APIKeyScopeAgent, proxy)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit response = %d, retry=%q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
	_, audit := server.gatewaySecurity()
	events, err := audit.Latest(10)
	if err != nil || len(events) == 0 || events[0].Event != "gateway-rate-limit" || events[0].KeyID != key.ID {
		t.Fatalf("rate-limit audit = %#v, %v", events, err)
	}
}

func TestGatewayKeyQuotaEnforcesIndependentLimits(t *testing.T) {
	now := time.Unix(1200, 0)
	newQuota := func() *gatewayKeyQuota {
		quota := newGatewayKeyQuota()
		quota.now = func() time.Time { return now }
		return quota
	}
	t.Run("total requests", func(t *testing.T) {
		quota := newQuota()
		limits := state.APIKeyLimits{RequestsPerMinute: 1}
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeAgent, limits); !allowed {
			t.Fatal("first request denied")
		}
		quota.complete("key", state.APIKeyScopeAgent, 0, 0)
		if allowed, retry, detail := quota.begin("key", state.APIKeyScopeModel, limits); allowed || retry < 1 || detail != "requests per minute limit exceeded" {
			t.Fatalf("second request = allowed %v retry %d detail %q", allowed, retry, detail)
		}
	})
	t.Run("model requests", func(t *testing.T) {
		quota := newQuota()
		limits := state.APIKeyLimits{ModelRequestsPerMinute: 1}
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeModel, limits); !allowed {
			t.Fatal("first model request denied")
		}
		quota.complete("key", state.APIKeyScopeModel, 0, 0)
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeAgent, limits); !allowed {
			t.Fatal("agent request consumed model RPM")
		}
		quota.complete("key", state.APIKeyScopeAgent, 0, 0)
		if allowed, _, detail := quota.begin("key", state.APIKeyScopeModel, limits); allowed || detail != "model requests per minute limit exceeded" {
			t.Fatalf("second model request allowed=%v detail=%q", allowed, detail)
		}
	})
	t.Run("parallel", func(t *testing.T) {
		quota := newQuota()
		limits := state.APIKeyLimits{MaxParallelRequests: 1}
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeModel, limits); !allowed {
			t.Fatal("first parallel request denied")
		}
		if allowed, _, detail := quota.begin("key", state.APIKeyScopeAgent, limits); allowed || detail != "parallel request limit exceeded" {
			t.Fatalf("parallel cap allowed=%v detail=%q", allowed, detail)
		}
		quota.complete("key", state.APIKeyScopeModel, 0, 0)
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeAgent, limits); !allowed {
			t.Fatal("parallel slot was not released")
		}
	})
	t.Run("total and model tokens", func(t *testing.T) {
		quota := newQuota()
		limits := state.APIKeyLimits{TokensPerMinute: 20, ModelTokensPerMinute: 10}
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeModel, limits); !allowed {
			t.Fatal("first token request denied")
		}
		quota.complete("key", state.APIKeyScopeModel, 6, 4)
		if allowed, _, detail := quota.begin("key", state.APIKeyScopeModel, limits); allowed || detail != "model tokens per minute limit exceeded" {
			t.Fatalf("model TPM allowed=%v detail=%q", allowed, detail)
		}
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeAgent, limits); !allowed {
			t.Fatal("model TPM incorrectly blocked agent traffic")
		}
		quota.complete("key", state.APIKeyScopeAgent, 5, 5)
		if allowed, _, detail := quota.begin("key", state.APIKeyScopeAgent, limits); allowed || detail != "tokens per minute limit exceeded" {
			t.Fatalf("total TPM allowed=%v detail=%q", allowed, detail)
		}
		now = now.Add(time.Minute)
		if allowed, _, _ := quota.begin("key", state.APIKeyScopeModel, limits); !allowed {
			t.Fatal("minute rollover did not reset token windows")
		}
	})
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
		"LAN exposure":     {http.MethodPost, "/api/gateway/lan", (&Server{}).gatewayLanSet},
		"Tailnet exposure": {http.MethodPost, "/api/gateway/tailnet", (&Server{}).gatewayTailnetSet},
		"public exposure":  {http.MethodPost, "/api/gateway/tunnel", (&Server{}).gatewayTunnelSet},
		"key creation":     {http.MethodPost, "/api/keys", (&Server{}).keyCreate},
		"key update":       {http.MethodPatch, "/api/keys/example", (&Server{}).keyUpdate},
		"key revocation":   {http.MethodDelete, "/api/keys/example", (&Server{}).keyDelete},
		"API identity":     {http.MethodPost, "/api/settings/inference-contract", (&Server{}).inferenceContractSet},
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
