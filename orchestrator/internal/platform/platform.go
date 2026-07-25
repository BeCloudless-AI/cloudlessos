// Package platform detects appliances whose vendor-managed system stack must
// not be replaced by CloudlessOS generic hardware automation.
package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	Generic  = "generic"
	DGXSpark = "dgx-spark"

	GPUDocker = "docker"
	GPUCDI    = "cdi"
)

var identityFiles = []string{
	"etc/dgx-release",
	"proc/device-tree/model",
	"sys/firmware/devicetree/base/model",
	"sys/devices/virtual/dmi/id/product_name",
}

// Detect returns the current platform. CLOUDLESS_PLATFORM is an explicit
// deployment/test override; otherwise detection uses vendor identity files.
func Detect() string {
	if value := strings.TrimSpace(os.Getenv("CLOUDLESS_PLATFORM")); value != "" {
		return strings.ToLower(value)
	}
	return DetectAt("/")
}

// DetectAt detects a platform beneath root, allowing deterministic tests.
func DetectAt(root string) string {
	for _, name := range identityFiles {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			continue
		}
		identity := strings.ToLower(strings.ReplaceAll(string(content), "\x00", "\n"))
		if strings.Contains(identity, "dgx spark") ||
			strings.Contains(identity, "dgx-spark") ||
			strings.Contains(identity, "dgx_spark") ||
			strings.Contains(identity, "gb10") {
			return DGXSpark
		}
	}
	return Generic
}

func IsDGXSpark() bool {
	return Detect() == DGXSpark
}

// Architecture returns the container architecture for this host. The override
// keeps cross-platform catalog and installer tests deterministic.
func Architecture() string {
	if value := strings.TrimSpace(os.Getenv("CLOUDLESS_ARCH")); value != "" {
		return strings.ToLower(value)
	}
	return runtime.GOARCH
}

// GPUContainerMode reports how Docker should receive an NVIDIA device request.
// DGX Spark's factory Docker 29 + NVIDIA Toolkit installation exposes CDI
// devices and deliberately does not register the legacy "nvidia" runtime.
func GPUContainerMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDLESS_GPU_MODE"))) {
	case GPUCDI:
		return GPUCDI
	case GPUDocker:
		return GPUDocker
	}
	if IsDGXSpark() {
		return GPUCDI
	}
	return GPUDocker
}
