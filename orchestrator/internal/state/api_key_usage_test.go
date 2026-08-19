package state

import (
	"reflect"
	"testing"
)

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

func TestAPIKeyLimitsUpdateAndPersistIndependently(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := store.AddAPIKey("limited", APIKeyScopeBoth)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := store.AddAPIKey("unlimited", APIKeyScopeModel)
	if err != nil {
		t.Fatal(err)
	}
	want := APIKeyLimits{
		TokensPerMinute: 12000, RequestsPerMinute: 30, MaxParallelRequests: 2,
		ModelTokensPerMinute: 8000, ModelRequestsPerMinute: 20,
	}
	updated, err := store.UpdateAPIKeyLimits(first.ID, want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.Limits, want) {
		t.Fatalf("updated limits = %+v, want %+v", updated.Limits, want)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	limits, ok := reopened.APIKeyLimitsFor(first.ID)
	if !ok || !reflect.DeepEqual(limits, want) {
		t.Fatalf("persisted limits = %+v, %v", limits, ok)
	}
	secondLimits, ok := reopened.APIKeyLimitsFor(second.ID)
	if !ok || secondLimits != (APIKeyLimits{}) {
		t.Fatalf("second key limits changed = %+v, %v", secondLimits, ok)
	}
}

func TestAPIKeyLimitsValidation(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := store.AddAPIKey("client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAPIKeyLimits(key.ID, APIKeyLimits{RequestsPerMinute: -1}); err == nil {
		t.Fatal("negative limit was accepted")
	}
	if _, err := store.UpdateAPIKeyLimits("missing", APIKeyLimits{}); err == nil {
		t.Fatal("missing key update was accepted")
	}
}
