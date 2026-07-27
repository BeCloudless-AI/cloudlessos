package api

import (
	"context"
	"time"

	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

// clusterComputeView is the shared representation of all enrolled Sparks.
type clusterComputeView struct {
	Configured       bool     `json:"configured"`
	Healthy          bool     `json:"healthy"`
	Nodes            int      `json:"nodes"`
	PeerName         string   `json:"peerName,omitempty"`
	PeerHost         string   `json:"peerHost,omitempty"`
	NodeNames        []string `json:"nodeNames,omitempty"`
	Topology         string   `json:"topology,omitempty"`
	LocalMemoryGB    int      `json:"localMemoryGB,omitempty"`
	CombinedMemoryGB int      `json:"combinedMemoryGB,omitempty"`
	DistributedReady bool     `json:"distributedReady"`
}

func clusterCompute(ctx context.Context, localMemoryGB int) clusterComputeView {
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	status, err := sparkcluster.Status(probeCtx)
	if err != nil || !status.Configured {
		return clusterComputeView{LocalMemoryGB: localMemoryGB}
	}
	view := clusterComputeView{
		Configured: status.Configured, Healthy: status.Healthy, Nodes: status.NodeCount,
		PeerName: status.PeerName, PeerHost: status.PeerHost,
		LocalMemoryGB: localMemoryGB, Topology: status.Topology,
	}
	for _, node := range status.Nodes {
		view.NodeNames = append(view.NodeNames, node.Name)
	}
	if status.Healthy && status.WorkerReady && localMemoryGB > 0 {
		view.CombinedMemoryGB = localMemoryGB * status.NodeCount
		view.DistributedReady = true
	}
	return view
}
