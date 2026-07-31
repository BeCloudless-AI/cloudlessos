package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

type recipeClusterNodeFingerprint struct {
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	Username    string   `json:"username"`
	Fingerprint string   `json:"fingerprint"`
	Links       []string `json:"links"`
	IPs         []string `json:"ips"`
}

type recipeClusterFingerprintDocument struct {
	Configured bool                           `json:"configured"`
	Role       string                         `json:"role"`
	Topology   string                         `json:"topology"`
	CreatedAt  string                         `json:"createdAt"`
	LocalLinks []string                       `json:"localLinks"`
	LocalIPs   []string                       `json:"localIps"`
	Nodes      []recipeClusterNodeFingerprint `json:"nodes"`
}

func recipeFingerprint(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func recipePlatformFingerprint(values map[string]string) (string, error) {
	return recipeFingerprint(values)
}

func recipeClusterFingerprint(cluster sparkcluster.State) (string, error) {
	document := recipeClusterFingerprintDocument{
		Configured: cluster.Configured, Role: strings.TrimSpace(cluster.Role),
		Topology: strings.TrimSpace(cluster.Topology), CreatedAt: cluster.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		LocalLinks: append([]string(nil), cluster.LocalLinks...), LocalIPs: append([]string(nil), cluster.LocalIPs...),
	}
	sort.Strings(document.LocalLinks)
	sort.Strings(document.LocalIPs)
	for _, node := range cluster.Nodes {
		entry := recipeClusterNodeFingerprint{
			Name: strings.TrimSpace(node.Name), Host: strings.TrimSpace(node.Host), Username: strings.TrimSpace(node.Username),
			Fingerprint: strings.TrimSpace(node.Fingerprint), Links: append([]string(nil), node.Links...), IPs: append([]string(nil), node.IPs...),
		}
		sort.Strings(entry.Links)
		sort.Strings(entry.IPs)
		document.Nodes = append(document.Nodes, entry)
	}
	sort.Slice(document.Nodes, func(i, j int) bool {
		if document.Nodes[i].Fingerprint != document.Nodes[j].Fingerprint {
			return document.Nodes[i].Fingerprint < document.Nodes[j].Fingerprint
		}
		if document.Nodes[i].Host != document.Nodes[j].Host {
			return document.Nodes[i].Host < document.Nodes[j].Host
		}
		return document.Nodes[i].Name < document.Nodes[j].Name
	})
	return recipeFingerprint(document)
}
