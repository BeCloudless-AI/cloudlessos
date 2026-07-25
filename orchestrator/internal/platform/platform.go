// Package platform detects appliances whose vendor-managed system stack must
// not be replaced by CloudlessOS generic hardware automation.
package platform

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	Generic  = "generic"
	DGXSpark = "dgx-spark"
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
