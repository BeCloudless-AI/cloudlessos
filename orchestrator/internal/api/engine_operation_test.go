package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/jobs"
)

func TestDescribeEngineOperationUsesRuntimeTruth(t *testing.T) {
	tests := []struct {
		name      string
		startup   *jobs.Snapshot
		ready     bool
		unloaded  bool
		operation string
		jobID     string
		canAbort  bool
	}{
		{name: "ready overrides stale launch", startup: engineStartup("job-1", "model:vllm"), ready: true, operation: "idle"},
		{name: "loading model can abort", startup: engineStartup("job-2", "model:vllm"), operation: "loading", jobID: "job-2", canAbort: true},
		{name: "switching engine can abort", startup: engineStartup("job-3", "engine:vllm"), operation: "loading", jobID: "job-3", canAbort: true},
		{name: "unloading is not loading", startup: engineStartup("job-4", "engine:unload"), ready: true, operation: "unloading", jobID: "job-4"},
		{name: "aborting is not loading", startup: engineStartup("job-5", "engine:abort"), operation: "aborting", jobID: "job-5"},
		{name: "unloaded overrides stale launch", startup: engineStartup("job-6", "model:vllm"), unloaded: true, operation: "idle"},
		{name: "completed job is idle", startup: &jobs.Snapshot{ID: "job-7", AppID: "model:vllm", Update: jobs.Update{Done: true}}, operation: "idle"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation, jobID, canAbort := describeEngineOperation(test.startup, test.ready, test.unloaded)
			if operation != test.operation || jobID != test.jobID || canAbort != test.canAbort {
				t.Fatalf("got (%q, %q, %t), want (%q, %q, %t)", operation, jobID, canAbort, test.operation, test.jobID, test.canAbort)
			}
		})
	}
}

func engineStartup(id, appID string) *jobs.Snapshot {
	return &jobs.Snapshot{ID: id, AppID: appID, Update: jobs.Update{Phase: "loading"}}
}
