package api

import "testing"

func TestParseDockerLayerProgress(t *testing.T) {
	done, total, ok := parseDockerLayerProgress("Downloading [==========>] 12.5MB/50MB")
	if !ok || done != 12_500_000 || total != 50_000_000 {
		t.Fatalf("progress = %d/%d ok=%v", done, total, ok)
	}
}

func TestDockerPullTotalsIncludeLayersAndBytes(t *testing.T) {
	layers := map[string]*dockerPullLayer{
		"one": {complete: true, done: 50, total: 50},
		"two": {done: 25, total: 100},
	}
	done, total, bytesDone, bytesTotal := dockerPullTotals(layers)
	if done != 1 || total != 2 || bytesDone != 75 || bytesTotal != 150 {
		t.Fatalf("totals = layers %d/%d bytes %d/%d", done, total, bytesDone, bytesTotal)
	}
}
