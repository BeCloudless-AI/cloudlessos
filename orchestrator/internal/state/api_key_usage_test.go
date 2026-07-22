package state

import "testing"

func TestRecordAPIUsageTracksPerKeyMetrics(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := s.AddAPIKey("Website")
	if err != nil {
		t.Fatal(err)
	}

	s.RecordAPIUsage(key.ID, true, 120, 45)
	s.RecordAPIUsage(key.ID, false, 10, 0)
	s.PersistIfDirty()

	keys := s.APIKeys()
	if len(keys) != 1 {
		t.Fatalf("keys=%d, want 1", len(keys))
	}
	got := keys[0]
	if got.Requests != 2 || got.Successes != 1 || got.Failures != 1 {
		t.Fatalf("request metrics = %+v", got)
	}
	if got.PromptTokens != 130 || got.CompletionTokens != 45 {
		t.Fatalf("token metrics = prompt %d completion %d", got.PromptTokens, got.CompletionTokens)
	}
	if got.LastUsed == "" {
		t.Fatal("last-used timestamp was not recorded")
	}
}

func TestAPIKeyScopesAndRouteMetrics(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelSecret, modelKey, err := s.AddAPIKey("model client", APIKeyScopeModel)
	if err != nil {
		t.Fatal(err)
	}
	agentSecret, agentKey, err := s.AddAPIKey("agent client", APIKeyScopeAgent)
	if err != nil {
		t.Fatal(err)
	}
	bothSecret, bothKey, err := s.AddAPIKey("trusted client", APIKeyScopeBoth)
	if err != nil {
		t.Fatal(err)
	}

	checks := []struct {
		secret string
		scope  string
		want   bool
	}{
		{modelSecret, APIKeyScopeModel, true},
		{modelSecret, APIKeyScopeAgent, false},
		{agentSecret, APIKeyScopeModel, false},
		{agentSecret, APIKeyScopeAgent, true},
		{bothSecret, APIKeyScopeModel, true},
		{bothSecret, APIKeyScopeAgent, true},
	}
	for _, check := range checks {
		_, got := s.ValidateAPIKeyFor(check.secret, check.scope)
		if got != check.want {
			t.Fatalf("ValidateAPIKeyFor(%q, %q) = %v, want %v", check.secret[:20], check.scope, got, check.want)
		}
	}

	s.RecordAPIUsageKind(modelKey.ID, APIKeyScopeModel, true, 10, 4)
	s.RecordAPIUsageKind(agentKey.ID, APIKeyScopeAgent, true, 12, 6)
	s.RecordAPIUsageKind(bothKey.ID, APIKeyScopeAgent, false, 0, 0)
	byID := map[string]APIKey{}
	for _, key := range s.APIKeys() {
		byID[key.ID] = key
	}
	if got := byID[modelKey.ID]; got.ModelRequests != 1 || got.AgentRequests != 0 {
		t.Fatalf("model route metrics = %+v", got)
	}
	if got := byID[agentKey.ID]; got.ModelRequests != 0 || got.AgentRequests != 1 {
		t.Fatalf("agent route metrics = %+v", got)
	}
	if got := byID[bothKey.ID]; got.AgentRequests != 1 || got.Failures != 1 {
		t.Fatalf("combined-key metrics = %+v", got)
	}
}
