package api

import (
	"context"
	"time"

	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

// clusterComputeView is the shared representation of all enrolled Sparks.
type clusterComputeView struct {
	Configured          bool                       `json:"configured"`
	Healthy             bool                       `json:"healthy"`
	Nodes               int                        `json:"nodes"`
	EnrolledNodes       int                        `json:"enrolledNodes"`
	SelectedNodes       int                        `json:"selectedNodes"`
	PeerName            string                     `json:"peerName,omitempty"`
	PeerHost            string                     `json:"peerHost,omitempty"`
	NodeNames           []string                   `json:"nodeNames,omitempty"`
	Topology            string                     `json:"topology,omitempty"`
	LocalMemoryGB       int                        `json:"localMemoryGB,omitempty"`
	CombinedMemoryGB    int                        `json:"combinedMemoryGB,omitempty"`
	LocalCapacityGB     float64                    `json:"localCapacityGB,omitempty"`
	CombinedCapacityGB  float64                    `json:"combinedCapacityGB,omitempty"`
	LocalAvailableGB    float64                    `json:"localAvailableGB,omitempty"`
	CombinedAvailableGB float64                    `json:"combinedAvailableGB,omitempty"`
	LocalReservedGB     float64                    `json:"localReservedGB,omitempty"`
	CombinedReservedGB  float64                    `json:"combinedReservedGB,omitempty"`
	CombinedCacheGB     float64                    `json:"combinedCacheGB,omitempty"`
	PerNodeCapacityGB   float64                    `json:"perNodeCapacityGB,omitempty"`
	PerNodeAvailableGB  float64                    `json:"perNodeAvailableGB,omitempty"`
	DistributedReady    bool                       `json:"distributedReady"`
	Workers             []clusterComputeWorkerView `json:"workers,omitempty"`
}

type clusterComputeWorkerView struct {
	Name               string  `json:"name"`
	Host               string  `json:"host"`
	Fingerprint        string  `json:"fingerprint,omitempty"`
	Healthy            bool    `json:"healthy"`
	WorkerReady        bool    `json:"workerReady"`
	Selected           bool    `json:"selected"`
	UtilizationPct     int     `json:"utilizationPct,omitempty"`
	TemperatureC       int     `json:"temperatureC,omitempty"`
	PowerW             float64 `json:"powerW,omitempty"`
	StorageTotalGB     float64 `json:"storageTotalGB,omitempty"`
	StorageAvailableGB float64 `json:"storageAvailableGB,omitempty"`
	PhysicalLink       string  `json:"physicalLink,omitempty"`
	ManagementIP       string  `json:"managementIp,omitempty"`
	SSH                string  `json:"ssh,omitempty"`
	Fabric             string  `json:"fabric,omitempty"`
	WorkerRuntime      string  `json:"workerRuntime,omitempty"`
	MemoryTotalGB      float64 `json:"memoryTotalGB,omitempty"`
	MemoryCapacityGB   float64 `json:"memoryCapacityGB,omitempty"`
	MemoryAvailableGB  float64 `json:"memoryAvailableGB,omitempty"`
	MemoryReservedGB   float64 `json:"memoryReservedGB,omitempty"`
	MemoryCacheGB      float64 `json:"memoryCacheGB,omitempty"`
}

func mbToGB(value int) float64 {
	return float64(value) / 1024
}

func clusterCompute(ctx context.Context, localMemoryGB int) clusterComputeView {
	localBudget := hardware.AcceleratorMemoryBudget(ctx)
	if localBudget.TotalMB <= 0 && localMemoryGB > 0 {
		localBudget = hardware.NewMemoryBudget(localMemoryGB*1024, localMemoryGB*1024, 0)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	status, err := sparkcluster.Status(probeCtx)
	if err != nil || !status.Configured {
		return clusterComputeView{
			LocalMemoryGB:    localMemoryGB,
			LocalCapacityGB:  mbToGB(localBudget.WorkloadCapacityMB),
			LocalAvailableGB: mbToGB(localBudget.WorkloadHeadroomMB),
			LocalReservedGB:  mbToGB(localBudget.ReservedMB),
		}
	}
	view := clusterComputeView{
		Configured: status.Configured, Healthy: status.ComputeHealthy, Nodes: status.ComputeNodeCount,
		EnrolledNodes: status.NodeCount, SelectedNodes: status.ComputeNodeCount,
		PeerName: status.PeerName, PeerHost: status.PeerHost,
		LocalMemoryGB: localMemoryGB, Topology: status.Topology,
		LocalCapacityGB:  mbToGB(localBudget.WorkloadCapacityMB),
		LocalAvailableGB: mbToGB(localBudget.WorkloadHeadroomMB),
		LocalReservedGB:  mbToGB(localBudget.ReservedMB),
	}
	telemetry, _ := sparkcluster.ClusterGPUs(probeCtx)
	telemetryByHost := make(map[string]sparkcluster.PeerTelemetry, len(telemetry))
	for _, peer := range telemetry {
		if peer.Reachable && len(peer.GPUs) > 0 {
			telemetryByHost[peer.Host] = peer
		}
	}
	totalMB := localBudget.TotalMB
	capacityMB := localBudget.WorkloadCapacityMB
	availableMB := localBudget.WorkloadHeadroomMB
	reservedMB := localBudget.ReservedMB
	cacheMB := localBudget.ReclaimableMB
	minCapacityMB := localBudget.WorkloadCapacityMB
	minAvailableMB := localBudget.WorkloadHeadroomMB
	completeMemory := localBudget.TotalMB > 0
	for _, node := range status.Nodes {
		view.NodeNames = append(view.NodeNames, node.Name)
		worker := clusterComputeWorkerView{
			Name: node.Name, Host: node.Host, Fingerprint: node.Fingerprint,
			Healthy: node.Healthy, WorkerReady: node.WorkerReady, Selected: node.Selected,
			PhysicalLink: node.Health.PhysicalLink.Status,
			ManagementIP: node.Health.ManagementIP.Status,
			SSH:          node.Health.SSH.Status, Fabric: node.Health.Fabric.Status,
			WorkerRuntime: node.Health.WorkerRuntime.Status,
		}
		if peer, ok := telemetryByHost[node.Host]; ok {
			gpu := peer.GPUs[0]
			nodeCapacityMB := max(0, gpu.MemTotalMB-gpu.MemReservedMB)
			worker.UtilizationPct = gpu.UtilPct
			worker.TemperatureC = gpu.TempC
			worker.PowerW = gpu.PowerW
			worker.StorageTotalGB = float64(peer.StorageTotalBytes) / (1024 * 1024 * 1024)
			worker.StorageAvailableGB = float64(peer.StorageAvailableBytes) / (1024 * 1024 * 1024)
			worker.MemoryTotalGB = mbToGB(gpu.MemTotalMB)
			worker.MemoryCapacityGB = mbToGB(nodeCapacityMB)
			worker.MemoryAvailableGB = mbToGB(gpu.MemHeadroomMB)
			worker.MemoryReservedGB = mbToGB(gpu.MemReservedMB)
			worker.MemoryCacheGB = mbToGB(gpu.MemReclaimableMB)
			if node.Selected {
				totalMB += gpu.MemTotalMB
				capacityMB += nodeCapacityMB
				availableMB += gpu.MemHeadroomMB
				reservedMB += gpu.MemReservedMB
				cacheMB += gpu.MemReclaimableMB
				if nodeCapacityMB < minCapacityMB {
					minCapacityMB = nodeCapacityMB
				}
				if gpu.MemHeadroomMB < minAvailableMB {
					minAvailableMB = gpu.MemHeadroomMB
				}
			}
		} else if node.Selected {
			completeMemory = false
		}
		view.Workers = append(view.Workers, worker)
	}
	if status.ComputeHealthy && status.ComputeWorkerReady && localMemoryGB > 0 {
		if completeMemory {
			view.CombinedMemoryGB = totalMB / 1024
			view.CombinedCapacityGB = mbToGB(capacityMB)
			view.CombinedAvailableGB = mbToGB(availableMB)
			view.CombinedReservedGB = mbToGB(reservedMB)
			view.CombinedCacheGB = mbToGB(cacheMB)
			view.PerNodeCapacityGB = mbToGB(minCapacityMB)
			view.PerNodeAvailableGB = mbToGB(minAvailableMB)
		}
		view.DistributedReady = true
	}
	return view
}

func clusterRuntimeReady(executionMode string, endpointReady bool, cluster clusterComputeView) (bool, bool) {
	degraded := executionMode == "cluster" && !cluster.DistributedReady
	return endpointReady && !degraded, degraded
}
