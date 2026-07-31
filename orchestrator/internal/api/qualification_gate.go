package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

const qualificationPhaseGateSchema = "cloudless.qualification-phase-gate.v1"

var qualificationPhaseGatePath = "/run/cloudless-qualification/phase-gate.json"
var qualificationPhaseGateOwnerUID uint32 = 0

type qualificationPhaseGateRecord struct {
	Schema        string `json:"schema"`
	Phase         string `json:"phase"`
	CampaignHash  string `json:"campaignSha256"`
	ExpiresAtUnix int64  `json:"expiresAtUnix"`
}

var qualificationInferencePhases = map[string]bool{
	"pending": true, "preparing": true, "downloading": true,
	"starting-workers": true, "loading": true, "optimizing": true,
	"verifying": true, "stopping": true, "rollback": true,
}

// waitQualificationPhaseGate is deliberately file-driven and read-only from
// cloudlessd. Only a root-owned, non-writable, short-lived record can pause an
// inference boundary; malformed or stale records never affect normal users.
func waitQualificationPhaseGate(ctx context.Context, phase string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for qualificationPhaseGateActive(phase, time.Now()) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func qualificationRollbackRequested() bool {
	return qualificationPhaseGateActive("rollback", time.Now())
}

func qualificationPhaseRequested(phase string) bool {
	return qualificationPhaseGateActive(phase, time.Now())
}

func qualificationPhaseGateActive(phase string, now time.Time) bool {
	if !qualificationInferencePhases[phase] {
		return false
	}
	info, err := os.Lstat(qualificationPhaseGatePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if info.Size() <= 0 || info.Size() > 4096 || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != qualificationPhaseGateOwnerUID {
		return false
	}
	handle, err := os.Open(qualificationPhaseGatePath)
	if err != nil {
		return false
	}
	defer handle.Close()
	decoder := json.NewDecoder(io.LimitReader(handle, 4097))
	decoder.DisallowUnknownFields()
	var gate qualificationPhaseGateRecord
	if decoder.Decode(&gate) != nil {
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return false
	}
	expires := time.Unix(gate.ExpiresAtUnix, 0)
	return gate.Schema == qualificationPhaseGateSchema &&
		gate.Phase == phase &&
		isLowerHex64(gate.CampaignHash) &&
		expires.After(now) &&
		!expires.After(now.Add(12*time.Minute))
}

func isLowerHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
