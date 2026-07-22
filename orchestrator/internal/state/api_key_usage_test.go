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
