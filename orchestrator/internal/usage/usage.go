// Package usage accumulates inference usage over time into persistent rollup buckets
// at four granularities (hour / day / month / year). The engine's Prometheus counters
// are point-in-time and reset on restart, so we sample them periodically, take
// reset-safe deltas, and fold each delta into the current hour, day, month and year
// bucket at once — giving usage charts at any zoom level, totals, peak bucket,
// averages, cache hits/misses and the last request time across the whole machine.
// API success/failure is recorded separately (from the gateway, which sees HTTP
// status) for a real success rate.
package usage

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"
)

// Counters is a snapshot of the engine's cumulative metric counters.
type Counters struct {
	Requests         float64 // requests served (e.g. TTFT histogram count)
	PromptTokens     float64 // cumulative input tokens
	CompletionTokens float64 // cumulative output tokens
	CacheHits        float64 // prefix-cache hits
	CacheQueries     float64 // prefix-cache lookups (misses = queries - hits)
}

// Bucket is one time period's accumulated usage. Key is the period key in the
// granularity's format (e.g. "2026-06-28T14", "2026-06-28", "2026-06", "2026").
type Bucket struct {
	Key         string `json:"key"`
	Requests    int64  `json:"requests"`
	PromptTok   int64  `json:"promptTokens"`
	CompTok     int64  `json:"completionTokens"`
	CacheHits   int64  `json:"cacheHits"`
	CacheMisses int64  `json:"cacheMisses"`
}

type data struct {
	Hours         map[string]*Bucket `json:"hours"`
	Days          map[string]*Bucket `json:"days"`
	Months        map[string]*Bucket `json:"months"`
	Years         map[string]*Bucket `json:"years"`
	LastRequestAt string             `json:"lastRequestAt,omitempty"`
	Base          Counters           `json:"base"`
	HasBase       bool               `json:"hasBase"`
	APISuccess    int64              `json:"apiSuccess"`
	APIFail       int64              `json:"apiFail"`
}

// Store is a persistent, concurrency-safe usage accumulator.
type Store struct {
	mu    sync.Mutex
	path  string
	d     data
	dirty bool
}

// granularity describes one zoom level: how to key a bucket, how many buckets the
// chart shows, how many to retain, how to label a point, and how to step back in time.
type granularity struct {
	keyFmt   string
	labelFmt string
	points   int
	keep     int
	back     func(t time.Time, i int) time.Time
}

var grans = map[string]granularity{
	"hour":  {"2006-01-02T15", "15:04", 24, 72, func(t time.Time, i int) time.Time { return t.Add(-time.Duration(i) * time.Hour) }},
	"day":   {"2006-01-02", "Jan 2", 30, 95, func(t time.Time, i int) time.Time { return t.AddDate(0, 0, -i) }},
	"month": {"2006-01", "Jan", 12, 36, func(t time.Time, i int) time.Time { return t.AddDate(0, -i, 0) }},
	"year":  {"2006", "2006", 5, 12, func(t time.Time, i int) time.Time { return t.AddDate(-i, 0, 0) }},
}

// Open loads the usage store from path (empty/missing → fresh).
func Open(path string) *Store {
	s := &Store{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.d)
	}
	s.ensureMaps()
	return s
}

func (s *Store) ensureMaps() {
	if s.d.Hours == nil {
		s.d.Hours = map[string]*Bucket{}
	}
	if s.d.Days == nil {
		s.d.Days = map[string]*Bucket{}
	}
	if s.d.Months == nil {
		s.d.Months = map[string]*Bucket{}
	}
	if s.d.Years == nil {
		s.d.Years = map[string]*Bucket{}
	}
	// Backfill Key on buckets loaded from an older file (keyed by map key).
	for k, b := range s.d.Days {
		if b.Key == "" {
			b.Key = k
		}
	}
}

// delta returns cur-base, or 0 when the counter reset (cur < base) — so an engine
// restart (counters back to 0) doesn't produce a spike.
func delta(cur, base float64) int64 {
	if cur < base || cur < 0 {
		return 0
	}
	return int64(cur - base)
}

// Sample folds the latest engine counters into the current hour/day/month/year
// buckets via reset-safe deltas.
func (s *Store) Sample(c Counters) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.d.HasBase {
		s.d.Base, s.d.HasBase, s.dirty = c, true, true
		return
	}
	dReq := delta(c.Requests, s.d.Base.Requests)
	dPrompt := delta(c.PromptTokens, s.d.Base.PromptTokens)
	dComp := delta(c.CompletionTokens, s.d.Base.CompletionTokens)
	dHits := delta(c.CacheHits, s.d.Base.CacheHits)
	dMiss := delta(c.CacheQueries, s.d.Base.CacheQueries) - dHits
	if dMiss < 0 {
		dMiss = 0
	}
	now := time.Now()
	for _, e := range []struct {
		m   map[string]*Bucket
		key string
	}{
		{s.d.Hours, now.Format("2006-01-02T15")},
		{s.d.Days, now.Format("2006-01-02")},
		{s.d.Months, now.Format("2006-01")},
		{s.d.Years, now.Format("2006")},
	} {
		b := bucketOf(e.m, e.key)
		b.Requests += dReq
		b.PromptTok += dPrompt
		b.CompTok += dComp
		b.CacheHits += dHits
		b.CacheMisses += dMiss
	}
	if dReq > 0 {
		s.d.LastRequestAt = now.UTC().Format(time.RFC3339)
	}
	s.d.Base = c
	s.prune()
	s.dirty = true
}

// RecordAPI records a gateway request outcome (for the success rate).
func (s *Store) RecordAPI(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		s.d.APISuccess++
	} else {
		s.d.APIFail++
	}
	s.d.LastRequestAt = time.Now().UTC().Format(time.RFC3339)
	s.dirty = true
}

func bucketOf(m map[string]*Bucket, key string) *Bucket {
	if b := m[key]; b != nil {
		return b
	}
	b := &Bucket{Key: key}
	m[key] = b
	return b
}

func (s *Store) prune() {
	pruneMap(s.d.Hours, grans["hour"].keep)
	pruneMap(s.d.Days, grans["day"].keep)
	pruneMap(s.d.Months, grans["month"].keep)
	pruneMap(s.d.Years, grans["year"].keep)
}

// pruneMap keeps only the most recent `keep` buckets (keys sort chronologically).
func pruneMap(m map[string]*Bucket, keep int) {
	if len(m) <= keep {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys[:len(m)-keep] {
		delete(m, k)
	}
}

// Point is one bucket in a usage series, with a display label.
type Point struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Requests  int64  `json:"requests"`
	PromptTok int64  `json:"promptTokens"`
	CompTok   int64  `json:"completionTokens"`
}

// Report is the aggregated usage snapshot for the UI. Totals/averages/cache are
// lifetime; Series + peak reflect the requested range.
type Report struct {
	Range            string  `json:"range"`
	TotalRequests    int64   `json:"totalRequests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	TotalTokens      int64   `json:"totalTokens"`
	CacheHits        int64   `json:"cacheHits"`
	CacheMisses      int64   `json:"cacheMisses"`
	PeakLabel        string  `json:"peakLabel"`
	PeakKey          string  `json:"peakKey"`
	PeakRequests     int64   `json:"peakRequests"`
	AvgPromptTokens  float64 `json:"avgPromptTokens"`
	AvgCompTokens    float64 `json:"avgCompletionTokens"`
	SuccessRate      float64 `json:"successRate"` // 0..100, -1 if no API requests yet
	APIRequests      int64   `json:"apiRequests"`
	LastRequestAt    string  `json:"lastRequestAt,omitempty"`
	Series           []Point `json:"series"` // selected range, oldest → newest, zero-filled
}

// Snapshot computes the aggregated report for the given range ("hour"|"day"|
// "month"|"year"; unknown → "day"). Lifetime totals come from the yearly rollup.
func (s *Store) Snapshot(rng string) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := grans[rng]
	if !ok {
		rng, g = "day", grans["day"]
	}
	r := Report{Range: rng, SuccessRate: -1, LastRequestAt: s.d.LastRequestAt}

	// Lifetime totals from the yearly rollup (retained for years → effectively all-time).
	for _, b := range s.d.Years {
		r.TotalRequests += b.Requests
		r.PromptTokens += b.PromptTok
		r.CompletionTokens += b.CompTok
		r.CacheHits += b.CacheHits
		r.CacheMisses += b.CacheMisses
	}
	r.TotalTokens = r.PromptTokens + r.CompletionTokens
	if r.TotalRequests > 0 {
		r.AvgPromptTokens = float64(r.PromptTokens) / float64(r.TotalRequests)
		r.AvgCompTokens = float64(r.CompletionTokens) / float64(r.TotalRequests)
	}
	r.APIRequests = s.d.APISuccess + s.d.APIFail
	if r.APIRequests > 0 {
		r.SuccessRate = float64(s.d.APISuccess) / float64(r.APIRequests) * 100
	}

	// Series for the selected range, zero-filled and chronological.
	m := s.mapFor(rng)
	base := baseTime(rng, time.Now())
	r.Series = make([]Point, g.points)
	for i := 0; i < g.points; i++ {
		t := g.back(base, g.points-1-i)
		key := t.Format(g.keyFmt)
		p := Point{Key: key, Label: t.Format(g.labelFmt)}
		if b := m[key]; b != nil {
			p.Requests, p.PromptTok, p.CompTok = b.Requests, b.PromptTok, b.CompTok
		}
		if p.Requests > r.PeakRequests {
			r.PeakRequests, r.PeakLabel, r.PeakKey = p.Requests, p.Label, key
		}
		r.Series[i] = p
	}
	return r
}

func (s *Store) mapFor(rng string) map[string]*Bucket {
	switch rng {
	case "hour":
		return s.d.Hours
	case "month":
		return s.d.Months
	case "year":
		return s.d.Years
	default:
		return s.d.Days
	}
}

// baseTime is the period start "now" maps to, so stepping back never overflows
// (month/year normalize to the first of the period).
func baseTime(rng string, now time.Time) time.Time {
	switch rng {
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	case "year":
		return time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
	default:
		return now
	}
}

// Flush writes the store to disk if it changed since the last flush.
func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty || s.path == "" {
		return
	}
	if b, err := json.MarshalIndent(s.d, "", "  "); err == nil {
		tmp := s.path + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			_ = os.Rename(tmp, s.path)
			s.dirty = false
		}
	}
}
