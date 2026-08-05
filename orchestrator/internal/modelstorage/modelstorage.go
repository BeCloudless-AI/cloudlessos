// Package modelstorage defines the persistent model-cache storage contract.
// Containers always receive the same host cache path; this package decides
// whether that path is backed by local storage or a validated shared NFS
// export.
package modelstorage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
)

const (
	ModeLocal      = "local"
	ModeNFS        = "nfs"
	DefaultVersion = "4.2"
	// ManagedExport is shared by the coordinator Spark in the simple setup.
	// Only model weights live below this path; compiled device caches remain
	// local to each Spark.
	ManagedExport = "/var/lib/cloudless/models-cache/hub"
	// MountPoint holds the NFS export itself. Only its hub directory is bound
	// into the standard cache so node-specific CUDA/JIT caches stay local.
	MountPoint = "/var/lib/cloudless/shared-model-storage"
	CacheRoot  = "/var/lib/cloudless/models-cache"
	CacheHub   = "/var/lib/cloudless/models-cache/hub"
	MarkerName = ".cloudless-shared-storage-v1"
	ReadyPath  = "/run/cloudless/model-storage.ready"
)

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
var markerPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Config contains no credentials. Cloudless currently supports NFSv4 exports
// whose server-side identity and authorization are managed by the operator.
type Config struct {
	Mode      string `json:"mode"`
	Managed   bool   `json:"managed,omitempty"`
	Server    string `json:"server,omitempty"`
	Export    string `json:"export,omitempty"`
	Version   string `json:"version,omitempty"`
	MarkerID  string `json:"markerId,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

func (c Config) Normalized() Config {
	c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
	if c.Mode == "" {
		c.Mode = ModeLocal
	}
	c.Server = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(c.Server), "."))
	c.Export = strings.TrimSpace(c.Export)
	c.Version = strings.TrimSpace(c.Version)
	if c.Mode == ModeNFS && c.Version == "" {
		c.Version = DefaultVersion
	}
	c.MarkerID = strings.ToLower(strings.TrimSpace(c.MarkerID))
	return c
}

func (c Config) Validate() error {
	c = c.Normalized()
	switch c.Mode {
	case ModeLocal:
		if c.Managed || c.Server != "" || c.Export != "" || c.Version != "" || c.MarkerID != "" {
			return errors.New("local model storage cannot contain NFS settings")
		}
		return nil
	case ModeNFS:
	default:
		return errors.New("model storage mode must be local or nfs")
	}
	if !hostPattern.MatchString(c.Server) || net.ParseIP(c.Server) == nil && strings.Contains(c.Server, "..") {
		return errors.New("NFS server must be a valid IPv4 address or DNS hostname")
	}
	if net.ParseIP(c.Server) != nil && strings.Contains(c.Server, ":") {
		return errors.New("IPv6 NFS servers are not supported yet")
	}
	if c.Export == "" || !strings.HasPrefix(c.Export, "/") || c.Export == "/" || strings.ContainsAny(c.Export, "\\\x00\r\n\t ") {
		return errors.New("NFS export must be an absolute non-root path without whitespace")
	}
	for _, part := range strings.Split(strings.TrimPrefix(c.Export, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("NFS export contains an unsafe path component")
		}
		for _, char := range part {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-", char)) {
				return errors.New("NFS export contains unsupported characters")
			}
		}
	}
	if path.Clean(c.Export) != c.Export {
		return errors.New("NFS export must use a canonical absolute path")
	}
	if c.Version != "4.1" && c.Version != "4.2" {
		return errors.New("NFS version must be 4.1 or 4.2")
	}
	if c.Managed && c.Export != ManagedExport {
		return errors.New("managed NFS storage must use the Cloudless model hub export")
	}
	if !markerPattern.MatchString(c.MarkerID) {
		return errors.New("shared-storage marker is invalid")
	}
	return nil
}

func (c Config) Source() string {
	c = c.Normalized()
	return c.Server + ":" + c.Export
}

// Identity binds recipe checks to the exact storage mode and export without
// exposing any future secret fields.
func (c Config) Identity() string {
	c = c.Normalized()
	// Preserve the original external-NFS identity byte-for-byte so package
	// upgrades never invalidate an already mounted user's marker.
	var payload []byte
	if !c.Managed {
		payload, _ = json.Marshal(struct {
			Mode, Server, Export, Version, MarkerID string
		}{c.Mode, c.Server, c.Export, c.Version, c.MarkerID})
	} else {
		payload, _ = json.Marshal(struct {
			Mode, Server, Export, Version, MarkerID string
			Managed                                 bool
		}{c.Mode, c.Server, c.Export, c.Version, c.MarkerID, true})
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func MarkerContents(c Config) string {
	c = c.Normalized()
	return fmt.Sprintf("cloudless-nfs-v1 %s %s\n", c.MarkerID, c.Identity())
}
