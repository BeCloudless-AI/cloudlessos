// Package power records the machine's electricity consumption over time so the
// inference dashboard can show it permanently and let the user review and prune it.
//
// nvidia-smi reports each GPU's instantaneous board power (watts); the caller sums
// across GPUs and samples on a ticker. We integrate watts over the real interval
// between samples into watt-hours and fold each interval into the current hour and
// day bucket. Day buckets are the SOURCE OF TRUTH: the month and year views are
// derived by summing days, so erasing a day's log also corrects the coarser views.
// Hour buckets (sub-day) are kept separately, and shorter, for the 24-hour view.
//
// Everything persists to a JSON file, so the log survives restarts. The user can
// erase any single bucket (in any view) or clear the whole log.
package power

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	keepHours = 96              // ~4 days of hourly detail (covers the 24h view)
	keepDays  = 2000            // ~5.5 years of daily totals (the long-term record)
	maxGap    = 5 * time.Minute // cap one interval's energy, so daemon downtime/sleep can't credit a huge phantom draw
)

// Bucket is one period's accumulated electricity. PowerSum/Samples give the average
// board power over the period; PeakW is the highest single reading.
type Bucket struct {
	Key      string  `json:"key"`
	EnergyWh float64 `json:"energyWh"`
	PowerSum float64 `json:"powerSum"` // Σ watt readings (avg = powerSum/samples)
	Samples  int64   `json:"samples"`
	PeakW    float64 `json:"peakW"`
}

func (b *Bucket) add(watts, energyWh float64) {
	b.EnergyWh += energyWh
	b.PowerSum += watts
	b.Samples++
	if watts > b.PeakW {
		b.PeakW = watts
	}
}

type data struct {
	Hours        map[string]*Bucket `json:"hours"`
	Days         map[string]*Bucket `json:"days"`
	LastSampleAt string             `json:"lastSampleAt,omitempty"` // RFC3339; the clock for energy integration
}

// Store is a persistent, concurrency-safe electricity accumulator.
type Store struct {
	mu    sync.Mutex
	path  string
	d     data
	dirty bool
}

// Open loads the power store from path (empty/missing → fresh).
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
	for k, b := range s.d.Days {
		if b.Key == "" {
			b.Key = k
		}
	}
	for k, b := range s.d.Hours {
		if b.Key == "" {
			b.Key = k
		}
	}
}

// Sample integrates the current total board power (watts, summed across GPUs) over
// the real time since the previous sample, into the current hour and day buckets.
// The first sample only sets the clock (no prior interval to integrate).
func (s *Store) Sample(totalWatts float64, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	last, hasLast := parseTime(s.d.LastSampleAt)
	s.d.LastSampleAt = now.UTC().Format(time.RFC3339)
	s.dirty = true
	if !hasLast {
		return
	}
	dt := now.Sub(last)
	if dt <= 0 || totalWatts <= 0 {
		return
	}
	if dt > maxGap {
		dt = maxGap
	}
	energyWh := totalWatts * dt.Hours()

	bucketOf(s.d.Hours, now.Format("2006-01-02T15")).add(totalWatts, energyWh)
	bucketOf(s.d.Days, now.Format("2006-01-02")).add(totalWatts, energyWh)
	pruneMap(s.d.Hours, keepHours)
	pruneMap(s.d.Days, keepDays)
}

func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func bucketOf(m map[string]*Bucket, key string) *Bucket {
	if b := m[key]; b != nil {
		return b
	}
	b := &Bucket{Key: key}
	m[key] = b
	return b
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

// granularity describes one zoom level of the power chart.
type granularity struct {
	keyFmt   string
	labelFmt string
	points   int
	back     func(t time.Time, i int) time.Time
}

var grans = map[string]granularity{
	"hour":  {"2006-01-02T15", "15:04", 24, func(t time.Time, i int) time.Time { return t.Add(-time.Duration(i) * time.Hour) }},
	"day":   {"2006-01-02", "Jan 2", 30, func(t time.Time, i int) time.Time { return t.AddDate(0, 0, -i) }},
	"month": {"2006-01", "Jan", 12, func(t time.Time, i int) time.Time { return t.AddDate(0, -i, 0) }},
	"year":  {"2006", "2006", 5, func(t time.Time, i int) time.Time { return t.AddDate(-i, 0, 0) }},
}

// Point is one bucket in the chart series.
type Point struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	EnergyWh float64 `json:"energyWh"`
	AvgW     float64 `json:"avgW"`
	PeakW    float64 `json:"peakW"`
}

// Report is the aggregated electricity snapshot for the UI. Totals/avg/peak are
// lifetime (the whole daily record); Series + peak reflect the requested range.
type Report struct {
	Range        string  `json:"range"`
	TotalWh      float64 `json:"totalWh"`
	RangeWh      float64 `json:"rangeWh"`
	AvgW         float64 `json:"avgW"`
	PeakW        float64 `json:"peakW"`
	PeakLabel    string  `json:"peakLabel"`
	PeakKey      string  `json:"peakKey"`
	PeakEnergyWh float64 `json:"peakEnergyWh"`
	NowW         float64 `json:"nowW"` // most recent reading (current hour's avg as a fallback)
	LastSampleAt string  `json:"lastSampleAt,omitempty"`
	Series       []Point `json:"series"`
}

// Snapshot computes the report for the given range ("hour"|"day"|"month"|"year";
// unknown → "day"). Month/year series are derived by rolling up the daily buckets.
func (s *Store) Snapshot(rng string) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := grans[rng]
	if !ok {
		rng, g = "day", grans["day"]
	}
	r := Report{Range: rng, LastSampleAt: s.d.LastSampleAt}

	// Lifetime totals + avg/peak power from the daily record (source of truth).
	var pSum float64
	var samples int64
	for _, b := range s.d.Days {
		r.TotalWh += b.EnergyWh
		pSum += b.PowerSum
		samples += b.Samples
		if b.PeakW > r.PeakW {
			r.PeakW = b.PeakW
		}
	}
	if samples > 0 {
		r.AvgW = pSum / float64(samples)
	}

	m := s.seriesMap(rng)
	now := time.Now()
	base := baseTime(rng, now)
	r.Series = make([]Point, g.points)
	for i := 0; i < g.points; i++ {
		t := g.back(base, g.points-1-i)
		key := t.Format(g.keyFmt)
		p := Point{Key: key, Label: t.Format(g.labelFmt)}
		if b := m[key]; b != nil {
			p.EnergyWh, p.PeakW = b.EnergyWh, b.PeakW
			if b.Samples > 0 {
				p.AvgW = b.PowerSum / float64(b.Samples)
			}
		}
		r.RangeWh += p.EnergyWh
		if p.EnergyWh > r.PeakEnergyWh {
			r.PeakEnergyWh, r.PeakLabel, r.PeakKey = p.EnergyWh, p.Label, key
		}
		r.Series[i] = p
	}

	// NowW: the average board power over the most recent hour we have data for.
	if hb := mostRecent(s.d.Hours); hb != nil && hb.Samples > 0 {
		r.NowW = hb.PowerSum / float64(hb.Samples)
	}
	return r
}

// seriesMap returns the buckets for a range: hour/day are stored directly, month and
// year are derived from the daily record so they always reflect day-level erasures.
func (s *Store) seriesMap(rng string) map[string]*Bucket {
	switch rng {
	case "hour":
		return s.d.Hours
	case "month":
		return rollupDays(s.d.Days, "2006-01")
	case "year":
		return rollupDays(s.d.Days, "2006")
	default:
		return s.d.Days
	}
}

func rollupDays(days map[string]*Bucket, layout string) map[string]*Bucket {
	out := map[string]*Bucket{}
	for _, b := range days {
		t, err := time.Parse("2006-01-02", b.Key)
		if err != nil {
			continue
		}
		o := bucketOf(out, t.Format(layout))
		o.EnergyWh += b.EnergyWh
		o.PowerSum += b.PowerSum
		o.Samples += b.Samples
		if b.PeakW > o.PeakW {
			o.PeakW = b.PeakW
		}
	}
	return out
}

func mostRecent(m map[string]*Bucket) *Bucket {
	var key string
	for k := range m {
		if k > key {
			key = k
		}
	}
	return m[key]
}

// baseTime is the period start "now" maps to, so stepping back never overflows.
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

// Clear erases logged electricity data. With key=="" it wipes the whole log.
// Otherwise it deletes the bucket (rng,key): hour removes that hour; day/month/year
// remove the underlying day(s) — and the matching hour buckets — so every view stays
// consistent (the daily record is the source of truth for day/month/year).
func (s *Store) Clear(rng, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	if key == "" {
		s.d.Hours = map[string]*Bucket{}
		s.d.Days = map[string]*Bucket{}
		s.d.LastSampleAt = "" // restart integration cleanly
		return
	}
	switch rng {
	case "hour":
		delete(s.d.Hours, key)
	case "day":
		delete(s.d.Days, key)
		deletePrefix(s.d.Hours, key+"T")
	case "month": // key "2006-01" → its days are "2006-01-DD", hours "2006-01-DDThh"
		deletePrefix(s.d.Days, key+"-")
		deletePrefix(s.d.Hours, key+"-")
	case "year": // key "2006" → days "2006-...", hours "2006-..."
		deletePrefix(s.d.Days, key+"-")
		deletePrefix(s.d.Hours, key+"-")
	}
}

func deletePrefix(m map[string]*Bucket, prefix string) {
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			delete(m, k)
		}
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
