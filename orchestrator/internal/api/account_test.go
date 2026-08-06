package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/remoteaccess"
)

func TestAccountLoginUsesSameOriginProxyWithoutPersistingCredentials(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/login" || r.Method != http.MethodPost {
			t.Fatalf("unexpected upstream request %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"email":"member@example.com","password":"correct horse battery staple"}` {
			t.Fatalf("unexpected body %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"user":{"username":"member"},"session":{"access_token":"access","refresh_token":"refresh"}}`)
	}))
	defer remote.Close()

	server := &Server{accountBaseURL: remote.URL, accountHTTPClient: remote.Client()}
	request := httptest.NewRequest(http.MethodPost, "/api/account/login", strings.NewReader(`{"email":"member@example.com","password":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.accountLogin(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"username":"member"`) {
		t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
	}
}

func TestAccountOAuthPinsProviderAndLoopbackCallback(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/oauth/github" {
			t.Fatalf("unexpected OAuth path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"provider":"github","url":"https://kaftyfeclcciwwmykbms.supabase.co/auth/v1/authorize?provider=github&redirect_to=https%3A%2F%2Fbecloudless.ai%2Fauth%2Fcallback"}`)
	}))
	defer remote.Close()

	server := &Server{accountBaseURL: remote.URL, accountHTTPClient: remote.Client()}
	request := httptest.NewRequest(http.MethodGet, "/api/account/oauth/github?redirectTo=http%3A%2F%2F127.0.0.1%3A8765%2Fauth%2Fcallback", nil)
	request.SetPathValue("provider", "github")
	response := httptest.NewRecorder()
	server.accountOAuth(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload["url"], "redirect_to=http%3A%2F%2F127.0.0.1%3A8765%2Fauth%2Fcallback") {
		t.Fatalf("OAuth callback was not pinned to CloudlessOS: %s", payload["url"])
	}
}

func TestAccountOAuthRejectsNonLoopbackRedirect(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/api/account/oauth/google?redirectTo=https%3A%2F%2Fattacker.example%2Fcallback", nil)
	request.SetPathValue("provider", "google")
	response := httptest.NewRecorder()
	server.accountOAuth(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAccountOAuthAcceptsOnlyTheActiveTailscaleServeCallback(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"provider":"google","url":"https://kaftyfeclcciwwmykbms.supabase.co/auth/v1/authorize?provider=google"}`)
	}))
	defer remote.Close()

	server := &Server{
		accountBaseURL:    remote.URL,
		accountHTTPClient: remote.Client(),
		remoteAccess: &fakeRemoteAccess{status: remoteaccess.Status{
			Connected: true, ServeEnabled: true, DNSName: "spark-3493.tail58a396.ts.net",
		}},
	}
	redirectTo := "https://spark-3493.tail58a396.ts.net/auth/callback"
	request := httptest.NewRequest(http.MethodGet, "/api/account/oauth/google?redirectTo="+url.QueryEscape(redirectTo), nil)
	request.SetPathValue("provider", "google")
	response := httptest.NewRecorder()
	server.accountOAuth(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("active Tailscale callback rejected: %d %s", response.Code, response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload["url"], "redirect_to=https%3A%2F%2Fspark-3493.tail58a396.ts.net%2Fauth%2Fcallback") {
		t.Fatalf("Tailscale callback was not pinned to this CloudlessOS device: %s", payload["url"])
	}
}

func TestAccountOAuthRejectsInactiveOrDifferentTailscaleCallbacks(t *testing.T) {
	tests := []struct {
		name   string
		status remoteaccess.Status
		url    string
	}{
		{"serve disabled", remoteaccess.Status{Connected: true, DNSName: "spark-3493.tail58a396.ts.net"}, "https://spark-3493.tail58a396.ts.net/auth/callback"},
		{"disconnected", remoteaccess.Status{ServeEnabled: true, DNSName: "spark-3493.tail58a396.ts.net"}, "https://spark-3493.tail58a396.ts.net/auth/callback"},
		{"different device", remoteaccess.Status{Connected: true, ServeEnabled: true, DNSName: "spark-3493.tail58a396.ts.net"}, "https://attacker.tail58a396.ts.net/auth/callback"},
		{"tailscale IP", remoteaccess.Status{Connected: true, ServeEnabled: true, DNSName: "spark-3493.tail58a396.ts.net"}, "https://100.85.167.72/auth/callback"},
		{"custom port", remoteaccess.Status{Connected: true, ServeEnabled: true, DNSName: "spark-3493.tail58a396.ts.net"}, "https://spark-3493.tail58a396.ts.net:8765/auth/callback"},
		{"query injection", remoteaccess.Status{Connected: true, ServeEnabled: true, DNSName: "spark-3493.tail58a396.ts.net"}, "https://spark-3493.tail58a396.ts.net/auth/callback?next=attacker"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{remoteAccess: &fakeRemoteAccess{status: test.status}}
			request := httptest.NewRequest(http.MethodGet, "/api/account/oauth/google?redirectTo="+url.QueryEscape(test.url), nil)
			request.SetPathValue("provider", "google")
			response := httptest.NewRecorder()
			server.accountOAuth(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAccountCallbackServesTheEmbeddedInterface(t *testing.T) {
	response := httptest.NewRecorder()
	(&Server{}).Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/callback", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="auth-control"`) {
		t.Fatalf("callback did not serve the account client: %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "access_token=secret") {
		t.Fatal("URL fragment leaked into the response")
	}
}

func TestCommunityRecipeProxyPreservesAuthIdempotencyAndQuery(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/v1/recipes/me/recipes?limit=12" || r.Method != http.MethodPost {
			t.Fatalf("unexpected upstream request %s %s", r.Method, r.URL.RequestURI())
		}
		if r.Header.Get("Authorization") != "Bearer session" || r.Header.Get("Idempotency-Key") != "publish-123" {
			t.Fatalf("community credentials were not forwarded safely: %#v", r.Header)
		}
		_, _ = io.WriteString(w, `{"recipes":[]}`)
	}))
	defer remote.Close()
	server := &Server{accountBaseURL: remote.URL, accountHTTPClient: remote.Client()}
	request := httptest.NewRequest(http.MethodPost, "/api/community/recipes/me/recipes?limit=12", strings.NewReader(`{}`))
	request.SetPathValue("rest", "me/recipes")
	request.Header.Set("Authorization", "Bearer session")
	request.Header.Set("Idempotency-Key", "publish-123")
	response := httptest.NewRecorder()
	server.communityRecipes(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"recipes":[]`) {
		t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
	}
}

func TestCommunityRecipeProxyAllowsSocialPutMutations(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/recipes/123/rating" || r.Method != http.MethodPut {
			t.Fatalf("unexpected upstream request %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"rating":{"rating":5}}`)
	}))
	defer remote.Close()
	server := &Server{accountBaseURL: remote.URL, accountHTTPClient: remote.Client()}
	request := httptest.NewRequest(http.MethodPut, "/api/community/recipes/123/rating", strings.NewReader(`{"rating":5}`))
	request.SetPathValue("rest", "123/rating")
	response := httptest.NewRecorder()
	server.communityRecipes(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
	}
}

func TestCommunitySocialProxyPreservesFollowRequest(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/v1/community/profiles/alice/follow" || r.Method != http.MethodPut {
			t.Fatalf("unexpected upstream request %s %s", r.Method, r.URL.RequestURI())
		}
		if r.Header.Get("Authorization") != "Bearer session" {
			t.Fatalf("community authorization was not forwarded")
		}
		_, _ = io.WriteString(w, `{"following":true}`)
	}))
	defer remote.Close()
	server := &Server{accountBaseURL: remote.URL, accountHTTPClient: remote.Client()}
	request := httptest.NewRequest(http.MethodPut, "/api/community/social/profiles/alice/follow", nil)
	request.SetPathValue("rest", "profiles/alice/follow")
	request.Header.Set("Authorization", "Bearer session")
	response := httptest.NewRecorder()
	server.communitySocial(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"following":true`) {
		t.Fatalf("unexpected response %d %s", response.Code, response.Body.String())
	}
}
