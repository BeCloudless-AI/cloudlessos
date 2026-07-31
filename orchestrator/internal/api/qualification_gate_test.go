package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func writeQualificationGate(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "phase-gate.json")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func gateJSON(phase string, expires time.Time) string {
	return fmt.Sprintf(
		`{"schema":"%s","phase":"%s","campaignSha256":"%064d","expiresAtUnix":%d}`,
		qualificationPhaseGateSchema, phase, 0, expires.Unix(),
	)
}

func withQualificationGateTestPath(t *testing.T, path string) {
	t.Helper()
	oldPath, oldUID := qualificationPhaseGatePath, qualificationPhaseGateOwnerUID
	qualificationPhaseGatePath = path
	qualificationPhaseGateOwnerUID = uint32(os.Getuid())
	t.Cleanup(func() {
		qualificationPhaseGatePath = oldPath
		qualificationPhaseGateOwnerUID = oldUID
	})
}

func TestQualificationPhaseGateAcceptsExactProtectedRecord(t *testing.T) {
	now := time.Now()
	path := writeQualificationGate(t, gateJSON("loading", now.Add(time.Minute)), 0o600)
	withQualificationGateTestPath(t, path)
	if !qualificationPhaseGateActive("loading", now) {
		t.Fatal("exact protected qualification gate was not admitted")
	}
	if qualificationPhaseGateActive("verifying", now) {
		t.Fatal("gate admitted the wrong phase")
	}
}

func TestQualificationPhaseGateRejectsUnsafeOrStaleRecords(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"group-writable", gateJSON("loading", now.Add(time.Minute)), 0o620},
		{"expired", gateJSON("loading", now.Add(-time.Second)), 0o600},
		{"unbounded", gateJSON("loading", now.Add(15*time.Minute)), 0o600},
		{"unknown-field", gateJSON("loading", now.Add(time.Minute))[:len(gateJSON("loading", now.Add(time.Minute)))-1] + `,"extra":true}`, 0o600},
		{"invalid-campaign-hash", `{"schema":"cloudless.qualification-phase-gate.v1","phase":"loading","campaignSha256":"not-a-digest","expiresAtUnix":` + fmt.Sprint(now.Add(time.Minute).Unix()) + `}`, 0o600},
		{"malformed", `{`, 0o600},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeQualificationGate(t, test.body, test.mode)
			withQualificationGateTestPath(t, path)
			if qualificationPhaseGateActive("loading", now) {
				t.Fatal("unsafe qualification gate was admitted")
			}
		})
	}
}

func TestQualificationPhaseGateRejectsSymlinkAndWrongOwner(t *testing.T) {
	now := time.Now()
	target := writeQualificationGate(t, gateJSON("loading", now.Add(time.Minute)), 0o600)
	link := filepath.Join(t.TempDir(), "gate-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	withQualificationGateTestPath(t, link)
	if qualificationPhaseGateActive("loading", now) {
		t.Fatal("symlink gate was admitted")
	}
	qualificationPhaseGatePath = target
	if stat, ok := mustStat(t, target).Sys().(*syscall.Stat_t); ok {
		qualificationPhaseGateOwnerUID = stat.Uid + 1
	}
	if qualificationPhaseGateActive("loading", now) {
		t.Fatal("wrong-owner gate was admitted")
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func TestQualificationPhaseGateWaitsUntilRootControllerReleasesIt(t *testing.T) {
	path := writeQualificationGate(t, gateJSON("loading", time.Now().Add(time.Minute)), 0o600)
	withQualificationGateTestPath(t, path)
	done := make(chan struct{})
	go func() {
		waitQualificationPhaseGate(context.Background(), "loading")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("phase gate did not hold the operation")
	case <-time.After(30 * time.Millisecond):
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("phase gate did not release the operation")
	}
}

func TestQualificationRollbackRequestRequiresExactLiveRootGate(t *testing.T) {
	path := writeQualificationGate(t, gateJSON("rollback", time.Now().Add(time.Minute)), 0o600)
	withQualificationGateTestPath(t, path)
	if !qualificationRollbackRequested() {
		t.Fatal("exact rollback qualification gate did not request rollback")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if qualificationRollbackRequested() {
		t.Fatal("removed qualification gate still requested rollback")
	}
}
