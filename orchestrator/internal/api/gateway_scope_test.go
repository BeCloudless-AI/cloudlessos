package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestGatewayAuthorizationEnforcesKeyScope(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelSecret, _, err := st.AddAPIKey("model", state.APIKeyScopeModel)
	if err != nil {
		t.Fatal(err)
	}
	agentSecret, _, err := st.AddAPIKey("agent", state.APIKeyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{state: st}

	checks := []struct {
		name   string
		secret string
		scope  string
		ok     bool
		status int
	}{
		{"model on model", modelSecret, state.APIKeyScopeModel, true, http.StatusOK},
		{"model on agent", modelSecret, state.APIKeyScopeAgent, false, http.StatusForbidden},
		{"agent on agent", agentSecret, state.APIKeyScopeAgent, true, http.StatusOK},
		{"agent on model", agentSecret, state.APIKeyScopeModel, false, http.StatusForbidden},
		{"invalid", "sk-cloudless-invalid", state.APIKeyScopeAgent, false, http.StatusUnauthorized},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("Authorization", "Bearer "+check.secret)
			res := httptest.NewRecorder()
			_, ok := s.authorizeGateway(res, req, check.scope)
			if ok != check.ok {
				t.Fatalf("ok = %v, want %v", ok, check.ok)
			}
			if !ok && res.Code != check.status {
				t.Fatalf("status = %d, want %d", res.Code, check.status)
			}
		})
	}
}
