package api

import (
	"context"
	"time"

	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

// clusterComputeView is the shared system-wide representation of a connected
// Spark pair. UI surfaces, model fit calculations, and assistant grounding all
// consume this contract instead of independently interpreting cluster state.
type clusterComputeView struct {
	Configured       bool   `json:"configured"`
	Healthy          bool   `json:"healthy"`
	Nodes            int    `json:"nodes"`
	PeerName         string `json:"peerName,omitempty"`
	PeerHost         string `json:"peerHost,omitempty"`
	LocalMemoryGB    int    `json:"localMemoryGB,omitempty"`
	CombinedMemoryGB int    `json:"combinedMemoryGB,omitempty"`
	DistributedReady bool   `json:"distributedReady"`
}

func clusterCompute(ctx context.Context, localMemoryGB int) clusterComputeView {
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	status, err := sparkcluster.Status(probeCtx)
	if err != nil || !status.Configured {
		return clusterComputeView{LocalMemoryGB: localMemoryGB}
	}
	view := clusterComputeView{
		Configured: status.Configured, Healthy: status.Healthy, Nodes: 2,
		PeerName: status.PeerName, PeerHost: status.PeerHost,
		LocalMemoryGB: localMemoryGB,
	}
	if status.Healthy && status.WorkerReady && localMemoryGB > 0 {
		view.CombinedMemoryGB = localMemoryGB * 2
		view.DistributedReady = true
	}
	return view
}
