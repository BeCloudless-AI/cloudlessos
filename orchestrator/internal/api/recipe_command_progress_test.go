package api

import (
	"strings"
	"testing"
)

func TestRecipeCommandProgressParsesDockerBuildLayer(t *testing.T) {
	progress := newRecipeCommandProgress("building", "Build")
	message, done, total, ok := progress.parse("#6 sha256:abc 505.41MB / 4.14GB 259.3s")
	if !ok || done != 505_410_000 || total != 4_140_000_000 {
		t.Fatalf("progress = %q %d/%d ok=%v", message, done, total, ok)
	}
	for _, want := range []string{"pinned custom inference runtime", "0.5 / 3.9 GB", "12%", "remaining"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message is missing %q: %s", want, message)
		}
	}
}

func TestRecipeCommandProgressUsesBuildKitElapsedTimeWithTotalBytes(t *testing.T) {
	progress := newRecipeCommandProgress("building", "Build")
	if _, _, _, ok := progress.parse("#6 100MB / 1GB 10s"); !ok {
		t.Fatal("first BuildKit progress line was not parsed")
	}
	message, _, _, ok := progress.parse("#6 250MB / 1GB 20s")
	if !ok {
		t.Fatal("second BuildKit progress line was not parsed")
	}
	// 250 MB in 20 seconds is 12.5 MB/s, leaving about 60 seconds.
	if !strings.Contains(message, "about 1 minute remaining") {
		t.Fatalf("ETA did not use total bytes with BuildKit elapsed time: %s", message)
	}
}

func TestRecipeCommandProgressIgnoresOrdinaryLogs(t *testing.T) {
	progress := newRecipeCommandProgress("building", "Build")
	if _, _, _, ok := progress.parse("Step completed successfully"); ok {
		t.Fatal("ordinary log line was interpreted as byte progress")
	}
}

func TestRecipeProgressETAUsesHumanDurations(t *testing.T) {
	for _, test := range []struct {
		remaining int64
		rate      float64
		want      string
	}{{30, 1, "30 seconds"}, {600, 1, "10 minutes"}, {7500, 1, "2 hours 5 minutes"}} {
		if got := recipeProgressETA(test.remaining, test.rate); !strings.Contains(got, test.want) {
			t.Fatalf("ETA = %q, want %q", got, test.want)
		}
	}
}
