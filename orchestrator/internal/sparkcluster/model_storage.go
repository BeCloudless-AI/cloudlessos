package sparkcluster

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/modelstorage"
)

type ModelStorageNodeStatus struct {
	Name  string `json:"name"`
	Host  string `json:"host"`
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

// ManagedNFSServer returns the coordinator address on the private Spark
// fabric. It deliberately never falls back to Wi-Fi, Ethernet, or Tailscale:
// the built-in export must only be reachable by enrolled cluster peers.
func ManagedNFSServer() (string, error) {
	state, err := load()
	if err != nil {
		return "", err
	}
	if !state.Configured || state.Role != "coordinator" || len(state.Nodes) == 0 {
		return "", errors.New("a coordinator Spark cluster is required for managed shared storage")
	}
	for _, address := range state.LocalIPs {
		address = strings.TrimSpace(address)
		if strings.HasPrefix(address, "10.100.0.") {
			return address, nil
		}
	}
	return "", errors.New("the coordinator has no private 10.100.0.x fabric address")
}

func storageArgument(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func upgradeStorageWorker(ctx context.Context, node Node) error {
	payload := base64.StdEncoding.EncodeToString([]byte(workerScript))
	return upgradeWorkerHelper(ctx, node, payload)
}

func ApplyNFSModelStorage(ctx context.Context, config modelstorage.Config) ([]ModelStorageNodeStatus, error) {
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	state, err := load()
	if err != nil || !state.Configured {
		return nil, err
	}
	statuses := make([]ModelStorageNodeStatus, 0, len(state.Nodes))
	applied := make([]Node, 0, len(state.Nodes))
	for _, node := range state.Nodes {
		status := ModelStorageNodeStatus{Name: node.Name, Host: node.Host}
		peerErr := upgradeStorageWorker(ctx, node)
		if peerErr == nil {
			layout := "external"
			if config.Managed {
				layout = "managed"
			}
			command := fmt.Sprintf("sudo -n %s nfs-apply %s %s %s %s", workerPath,
				storageArgument(config.Server), storageArgument(config.Export), storageArgument(config.Version), layout)
			_, peerErr = remote(ctx, node.Host, node.Username, "", command, nil)
		}
		if peerErr != nil {
			status.Error = cleanStoragePeerError(peerErr)
			statuses = append(statuses, status)
			rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer rollbackCancel()
			for _, previous := range applied {
				_, _ = remote(rollbackCtx, previous.Host, previous.Username, "", "sudo -n "+workerPath+" nfs-local", nil)
			}
			return statuses, fmt.Errorf("configure shared model storage on %s: %w", node.Name, peerErr)
		}
		status.Ready = true
		statuses = append(statuses, status)
		applied = append(applied, node)
	}
	return statuses, nil
}

func RemoveNFSModelStorage(ctx context.Context) ([]ModelStorageNodeStatus, error) {
	state, err := load()
	if err != nil || !state.Configured {
		return nil, err
	}
	statuses := make([]ModelStorageNodeStatus, 0, len(state.Nodes))
	var result error
	for _, node := range state.Nodes {
		status := ModelStorageNodeStatus{Name: node.Name, Host: node.Host}
		peerErr := upgradeStorageWorker(ctx, node)
		if peerErr == nil {
			_, peerErr = remote(ctx, node.Host, node.Username, "", "sudo -n "+workerPath+" nfs-local", nil)
		}
		if peerErr != nil {
			status.Error = cleanStoragePeerError(peerErr)
			result = errors.Join(result, fmt.Errorf("restore local model storage on %s: %w", node.Name, peerErr))
		} else {
			status.Ready = true
		}
		statuses = append(statuses, status)
	}
	return statuses, result
}

func VerifyNFSModelStorage(ctx context.Context, config modelstorage.Config) ([]ModelStorageNodeStatus, error) {
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		return nil, err
	}
	state, err := load()
	if err != nil || !state.Configured {
		return nil, err
	}
	marker := strings.TrimSuffix(modelstorage.MarkerContents(config), "\n")
	statuses := make([]ModelStorageNodeStatus, 0, len(state.Nodes))
	var result error
	for _, node := range state.Nodes {
		status := ModelStorageNodeStatus{Name: node.Name, Host: node.Host}
		command := fmt.Sprintf("sudo -n %s nfs-status %s %s %s", workerPath,
			storageArgument(config.Server), storageArgument(config.Export), storageArgument(marker))
		output, peerErr := remote(ctx, node.Host, node.Username, "", command, nil)
		if peerErr != nil || strings.TrimSpace(output) != "ok" {
			if peerErr == nil {
				peerErr = errors.New("peer did not confirm the shared marker")
			}
			status.Error = cleanStoragePeerError(peerErr)
			result = errors.Join(result, fmt.Errorf("verify shared model storage on %s: %w", node.Name, peerErr))
		} else {
			status.Ready = true
		}
		statuses = append(statuses, status)
	}
	return statuses, result
}

func cleanStoragePeerError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}
