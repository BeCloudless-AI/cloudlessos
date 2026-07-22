package main

import (
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/osupdate"
)

func TestWriteProgressClampsPercentage(t *testing.T) {
	t.Setenv("CLOUDLESS_UPDATE_STATUS", filepath.Join(t.TempDir(), "status.json"))
	status := osupdate.DefaultStatus()

	if err := writeProgress(&status, -4, "Starting"); err != nil {
		t.Fatal(err)
	}
	if status.Progress != 0 || status.Message != "Starting" {
		t.Fatalf("writeProgress() = %#v", status)
	}

	if err := writeProgress(&status, 140, "Finishing"); err != nil {
		t.Fatal(err)
	}
	if status.Progress != 100 || status.Message != "Finishing" {
		t.Fatalf("writeProgress() = %#v", status)
	}
}
