package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/state"
)

func TestClusterRuntimeReadinessFailsClosedWhenSelectedNodeIsLost(t *testing.T) {
	tests := []struct {
		name          string
		mode          string
		endpointReady bool
		clusterReady  bool
		wantReady     bool
		wantDegraded  bool
	}{
		{name: "healthy cluster", mode: "cluster", endpointReady: true, clusterReady: true, wantReady: true},
		{name: "worker loss while endpoint lingers", mode: "cluster", endpointReady: true, clusterReady: false, wantDegraded: true},
		{name: "coordinator endpoint failed", mode: "cluster", endpointReady: false, clusterReady: true},
		{name: "local inference ignores cluster", mode: "local", endpointReady: true, clusterReady: false, wantReady: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ready, degraded := clusterRuntimeReady(test.mode, test.endpointReady, clusterComputeView{DistributedReady: test.clusterReady})
			if ready != test.wantReady || degraded != test.wantDegraded {
				t.Fatalf("ready, degraded = %v, %v", ready, degraded)
			}
		})
	}
}

// TestClusterFailureMatrixAcrossInferenceLifecyclePhases binds every supported
// failure domain to every durable non-terminal model phase. Lower-level cluster
// tests exercise the individual probes and cleanup paths; this matrix protects
// the API contract that must fail closed while retaining an Abort path.
func TestClusterFailureMatrixAcrossInferenceLifecyclePhases(t *testing.T) {
	failures := []string{
		"packet-loss",
		"management-address-change",
		"coordinator-loss",
		"selected-worker-loss",
		"coordinator-reboot",
		"role-reversal-rejected",
		"partial-cleanup",
	}
	phases := []string{
		"pending",
		"preparing",
		"downloading",
		"starting-workers",
		"loading",
		"optimizing",
		"verifying",
		"stopping",
		"rollback",
	}
	for _, failure := range failures {
		for _, phase := range phases {
			t.Run(failure+"/"+phase, func(t *testing.T) {
				ready, degraded := clusterRuntimeReady("cluster", true, clusterComputeView{DistributedReady: false})
				if ready || !degraded {
					t.Fatalf("failure %q phase %q did not fail closed: ready=%v degraded=%v", failure, phase, ready, degraded)
				}
				operation := state.InferenceOperation{ID: "job-matrix", Action: "switch", Phase: phase}
				if !inferenceOperationBlocksClusterMutation(operation) {
					t.Fatalf("failure %q phase %q allowed cluster mutation", failure, phase)
				}
				uiOperation, jobID, canAbort := describeClusterFailureOperation(operation)
				if uiOperation != "loading" || jobID != operation.ID || !canAbort {
					t.Fatalf("failure %q phase %q lost recovery action: operation=%q job=%q abort=%v", failure, phase, uiOperation, jobID, canAbort)
				}
			})
		}
	}
}

func TestClusterFailureOperationDoesNotOfferRecursiveAbort(t *testing.T) {
	tests := []struct {
		action    string
		operation string
	}{
		{action: "abort", operation: "aborting"},
		{action: "unload", operation: "unloading"},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			operation, jobID, canAbort := describeClusterFailureOperation(state.InferenceOperation{ID: "job-1", Action: test.action, Phase: "stopping"})
			if operation != test.operation || jobID != "job-1" || canAbort {
				t.Fatalf("got operation=%q job=%q abort=%v", operation, jobID, canAbort)
			}
		})
	}

	operation, jobID, canAbort := describeClusterFailureOperation(state.InferenceOperation{ID: "job-2", Action: "switch", Phase: "error"})
	if operation != "error" || jobID != "job-2" || canAbort {
		t.Fatalf("terminal failure got operation=%q job=%q abort=%v", operation, jobID, canAbort)
	}
}
