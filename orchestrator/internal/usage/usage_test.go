package usage

import "testing"

func TestSampleDeltasAndSnapshot(t *testing.T) {
	s := Open("")
	// first sample = baseline only (no usage yet)
	s.Sample(Counters{Requests: 10, PromptTokens: 1000, CompletionTokens: 500, CacheHits: 4, CacheQueries: 10})
	if r := s.Snapshot("day"); r.TotalRequests != 0 || r.TotalTokens != 0 {
		t.Fatalf("baseline sample should add nothing, got %+v", r)
	}
	// second sample → deltas: +5 req, +500 prompt, +300 comp, +6 hits, +8 queries (→2 misses)
	s.Sample(Counters{Requests: 15, PromptTokens: 1500, CompletionTokens: 800, CacheHits: 10, CacheQueries: 18})
	r := s.Snapshot("day")
	if r.TotalRequests != 5 || r.PromptTokens != 500 || r.CompletionTokens != 300 || r.TotalTokens != 800 {
		t.Errorf("req/tokens: %+v", r)
	}
	if r.CacheHits != 6 || r.CacheMisses != 2 {
		t.Errorf("cache hits/misses = %d/%d, want 6/2", r.CacheHits, r.CacheMisses)
	}
	if r.AvgPromptTokens != 100 || r.AvgCompTokens != 60 {
		t.Errorf("averages = %v/%v, want 100/60", r.AvgPromptTokens, r.AvgCompTokens)
	}
	if r.LastRequestAt == "" || r.PeakRequests != 5 {
		t.Errorf("lastReq=%q peak=%d", r.LastRequestAt, r.PeakRequests)
	}
	if r.Range != "day" || len(r.Series) != 30 || r.Series[29].Requests != 5 {
		t.Errorf("day series: range=%s len=%d last=%+v", r.Range, len(r.Series), r.Series[len(r.Series)-1])
	}
}

// The same usage must be visible at every zoom level, since each sample folds into
// all four rollups. The newest bucket of each range should carry the +5 requests.
func TestRangesAllAccrue(t *testing.T) {
	s := Open("")
	s.Sample(Counters{Requests: 100}) // baseline
	s.Sample(Counters{Requests: 105}) // +5
	for _, tc := range []struct {
		rng  string
		want int
	}{{"hour", 24}, {"day", 30}, {"month", 12}, {"year", 5}} {
		r := s.Snapshot(tc.rng)
		if r.Range != tc.rng || len(r.Series) != tc.want {
			t.Errorf("%s: range=%s len=%d want %d", tc.rng, r.Range, len(r.Series), tc.want)
		}
		if last := r.Series[len(r.Series)-1]; last.Requests != 5 {
			t.Errorf("%s: newest bucket requests=%d, want 5", tc.rng, last.Requests)
		}
		if r.TotalRequests != 5 || r.PeakRequests != 5 {
			t.Errorf("%s: total=%d peak=%d, want 5/5", tc.rng, r.TotalRequests, r.PeakRequests)
		}
	}
}

// An unknown range falls back to "day".
func TestUnknownRangeFallsBack(t *testing.T) {
	s := Open("")
	if r := s.Snapshot("decade"); r.Range != "day" || len(r.Series) != 30 {
		t.Errorf("unknown range: got range=%s len=%d, want day/30", r.Range, len(r.Series))
	}
}

func TestSampleResetNoSpike(t *testing.T) {
	s := Open("")
	s.Sample(Counters{Requests: 100, PromptTokens: 9000}) // baseline
	s.Sample(Counters{Requests: 105, PromptTokens: 9500}) // +5 / +500
	s.Sample(Counters{Requests: 3, PromptTokens: 200})    // engine restarted: counters dropped → 0 delta
	s.Sample(Counters{Requests: 8, PromptTokens: 700})    // +5 / +500 from the new session
	r := s.Snapshot("day")
	if r.TotalRequests != 10 || r.PromptTokens != 1000 {
		t.Errorf("reset handling: req=%d prompt=%d, want 10/1000", r.TotalRequests, r.PromptTokens)
	}
}

func TestSuccessRate(t *testing.T) {
	s := Open("")
	if s.Snapshot("day").SuccessRate != -1 {
		t.Error("no API requests → success rate should be -1")
	}
	for i := 0; i < 9; i++ {
		s.RecordAPI(true)
	}
	s.RecordAPI(false)
	if r := s.Snapshot("day"); r.SuccessRate != 90 || r.APIRequests != 10 {
		t.Errorf("success rate = %v (apiReq %d), want 90 / 10", r.SuccessRate, r.APIRequests)
	}
}
