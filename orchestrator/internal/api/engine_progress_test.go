package api

import (
	"strings"
	"testing"
)

func TestPeerDownloadStatusIncludesMeasuredProgressAndETA(t *testing.T) {
	got := peerDownloadStatus("spark-peer", "Qwen/Test", 50<<30, 70<<30, 1<<30)
	for _, want := range []string{"spark-peer is downloading Qwen/Test", "50.0 / 70.0 GB", "about 20 seconds remaining"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status missing %q: %q", want, got)
		}
	}
}

func TestEngineBannerUsesRealPeerByteProgress(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{"peer-downloading", "engineStartup.bytesDone", "Worker Sparks are downloading the model", "progress = Math.round(bytesDone / bytesTotal * 100)", "Finalizing"} {
		if !strings.Contains(page, want) {
			t.Fatalf("engine progress UI missing %q", want)
		}
	}
	if strings.Contains(page, "done >= total ? 92") {
		t.Fatal("engine progress UI still contains the synthetic 92% holding value")
	}
}
