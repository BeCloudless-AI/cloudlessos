package power

import (
	"path/filepath"
	"testing"
	"time"
)

// feed integrates `watts` held constant across `n` intervals of `step`, starting at
// t0. It models an independent sampling session: the clock is reset first, so a prior
// feed's last sample doesn't bridge a (capped) interval into this one.
func feed(s *Store, watts float64, t0 time.Time, step time.Duration, n int) time.Time {
	s.d.LastSampleAt = ""
	t := t0
	s.Sample(watts, t) // sets the clock; no interval yet
	for i := 0; i < n; i++ {
		t = t.Add(step)
		s.Sample(watts, t)
	}
	return t
}

func TestIntegratesWattsToWattHours(t *testing.T) {
	s := Open("")
	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	// 100 W held over 12 × 5-minute intervals = 1 hour → 100 Wh (5 min == maxGap, uncapped).
	feed(s, 100, t0, 5*time.Minute, 12)

	r := s.Snapshot("day")
	if got := r.TotalWh; got < 99.9 || got > 100.1 {
		t.Fatalf("TotalWh = %.3f, want ~100", got)
	}
	if r.AvgW < 99.9 || r.AvgW > 100.1 {
		t.Fatalf("AvgW = %.3f, want ~100", r.AvgW)
	}
	if r.PeakW != 100 {
		t.Fatalf("PeakW = %.3f, want 100", r.PeakW)
	}
}

func TestFirstSampleSetsClockOnly(t *testing.T) {
	s := Open("")
	s.Sample(250, time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC))
	if r := s.Snapshot("day"); r.TotalWh != 0 {
		t.Fatalf("first sample credited energy: %.3f", r.TotalWh)
	}
}

func TestGapIsCapped(t *testing.T) {
	s := Open("")
	t0 := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	s.Sample(100, t0)
	// A 10-hour gap at 100 W would be 1000 Wh uncapped; capped at maxGap (5 min) → ~8.33 Wh.
	s.Sample(100, t0.Add(10*time.Hour))
	r := s.Snapshot("day")
	if r.TotalWh > 9 {
		t.Fatalf("gap not capped: TotalWh = %.3f, want <9", r.TotalWh)
	}
}

func TestMonthYearDerivedFromDays(t *testing.T) {
	s := Open("")
	// Two different days in the same month, 60 W for 1h each → 120 Wh that month/year.
	feed(s, 60, time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC), 5*time.Minute, 12)
	feed(s, 60, time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC), 5*time.Minute, 12)

	for _, rng := range []string{"month", "year"} {
		r := s.Snapshot(rng)
		if r.RangeWh < 119 || r.RangeWh > 121 {
			t.Fatalf("%s RangeWh = %.3f, want ~120", rng, r.RangeWh)
		}
	}
}

func TestClearDayCorrectsAllViews(t *testing.T) {
	s := Open("")
	feed(s, 60, time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC), 5*time.Minute, 12)
	feed(s, 60, time.Date(2026, 6, 20, 8, 0, 0, 0, time.UTC), 5*time.Minute, 12)

	s.Clear("day", "2026-06-10") // erase one day

	if r := s.Snapshot("day"); r.TotalWh < 59 || r.TotalWh > 61 {
		t.Fatalf("after day erase, TotalWh = %.3f, want ~60", r.TotalWh)
	}
	// Month/year are derived, so they must drop too.
	if r := s.Snapshot("month"); r.RangeWh < 59 || r.RangeWh > 61 {
		t.Fatalf("after day erase, month RangeWh = %.3f, want ~60", r.RangeWh)
	}
}

func TestClearAllWipes(t *testing.T) {
	s := Open("")
	feed(s, 200, time.Date(2026, 6, 10, 8, 0, 0, 0, time.UTC), 5*time.Minute, 12)
	s.Clear("", "")
	if r := s.Snapshot("year"); r.TotalWh != 0 || r.RangeWh != 0 {
		t.Fatalf("clear-all left data: total=%.3f range=%.3f", r.TotalWh, r.RangeWh)
	}
}

func TestUnknownRangeFallsBack(t *testing.T) {
	s := Open("")
	r := s.Snapshot("decade")
	if r.Range != "day" || len(r.Series) != 30 {
		t.Fatalf("unknown range: got range=%q len=%d, want day/30", r.Range, len(r.Series))
	}
}

func TestPersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "power.json")
	s := Open(path)
	feed(s, 150, time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC), 5*time.Minute, 12)
	s.Flush()

	s2 := Open(path)
	if r := s2.Snapshot("day"); r.TotalWh < 149 || r.TotalWh > 151 {
		t.Fatalf("reloaded TotalWh = %.3f, want ~150", r.TotalWh)
	}
}

func TestRangeSeriesLengths(t *testing.T) {
	s := Open("")
	for rng, want := range map[string]int{"hour": 24, "day": 30, "month": 12, "year": 5} {
		if got := len(s.Snapshot(rng).Series); got != want {
			t.Fatalf("range %s series len = %d, want %d", rng, got, want)
		}
	}
}
