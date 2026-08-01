package api

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCalculateRecipeCapacityAccountsForCacheAndReserve(t *testing.T) {
	capacity, err := calculateRecipeCapacity(100, 200, 300, recipeCapacityMinimumReserve+1000, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if capacity.ModelBytes != 0 || capacity.RuntimeBytes != 200 || capacity.WorkspaceBytes != 300 {
		t.Fatalf("capacity components = %#v", capacity)
	}
	if capacity.ReserveBytes != recipeCapacityMinimumReserve || capacity.RequiredBytes != recipeCapacityMinimumReserve+500 {
		t.Fatalf("capacity totals = %#v", capacity)
	}
}

func TestCalculateRecipeCapacityRejectsNegativeValues(t *testing.T) {
	if _, err := calculateRecipeCapacity(-1, 0, 0, 0, false, false); err == nil {
		t.Fatal("negative capacity was accepted")
	}
}

func TestFilesystemAvailableBytesFindsExistingParent(t *testing.T) {
	root := t.TempDir()
	available, measuredAt, err := filesystemAvailableBytes(filepath.Join(root, "not", "created", "yet"))
	if err != nil {
		t.Fatal(err)
	}
	if available <= 0 || measuredAt != root {
		t.Fatalf("available=%d measuredAt=%q, want existing root %q", available, measuredAt, root)
	}
}

func TestPeerRecipeCapacityProbeReturnsBytes(t *testing.T) {
	output, err := exec.Command("/bin/sh", "-c", peerRecipeCapacityProbe, "cloudless-capacity", t.TempDir()).Output()
	if err != nil {
		t.Fatal(err)
	}
	available, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil || available <= 0 {
		t.Fatalf("capacity probe = %q, %v", output, err)
	}
}
