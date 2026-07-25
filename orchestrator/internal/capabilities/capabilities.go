// Package capabilities is the single authority for platform- and
// version-gated CloudlessOS behavior. The API and catalog both consume this
// evaluator so hiding a control in the UI can never be the only enforcement.
package capabilities

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/cloudless/orchestrator/internal/platform"
)

// BuildVersion is injected into production binaries by build-packages.sh.
var BuildVersion = "development"

const (
	DGXAppliance            = "dgx-appliance"
	UnifiedAcceleratorRAM   = "unified-accelerator-memory"
	NVIDIACDI               = "nvidia-cdi"
	DGXVendorUpdateBoundary = "dgx-vendor-update-boundary"
	GenericDriverUpdates    = "generic-nvidia-driver-management"
)

// Requirement describes where a feature or application is supported.
type Requirement struct {
	Platforms           []string `json:"platforms,omitempty"`
	Architectures       []string `json:"architectures,omitempty"`
	MinCloudlessVersion string   `json:"minCloudlessVersion,omitempty"`
	MinDGXOSVersion     string   `json:"minDgxOsVersion,omitempty"`
	RequiredFeatures    []string `json:"requiredFeatures,omitempty"`
}

// Facts are immutable inputs used to evaluate requirements.
type Facts struct {
	Platform         string `json:"platform"`
	Architecture     string `json:"architecture"`
	CloudlessVersion string `json:"cloudlessVersion"`
	DGXOSVersion     string `json:"dgxOsVersion,omitempty"`
}

// Status is a user- and client-readable feature verdict.
type Status struct {
	Available   bool        `json:"available"`
	Reason      string      `json:"reason,omitempty"`
	Requirement Requirement `json:"requirement,omitempty"`
}

// Snapshot is the complete capability contract exposed by the local API.
type Snapshot struct {
	Facts    Facts             `json:"facts"`
	Features map[string]Status `json:"features"`
}

var definitions = map[string]Requirement{
	DGXAppliance:            {Platforms: []string{platform.DGXSpark}},
	UnifiedAcceleratorRAM:   {Platforms: []string{platform.DGXSpark}},
	NVIDIACDI:               {Platforms: []string{platform.DGXSpark}},
	DGXVendorUpdateBoundary: {Platforms: []string{platform.DGXSpark}},
	GenericDriverUpdates:    {Platforms: []string{platform.Generic}},
}

// Current evaluates the built-in feature registry for this machine.
func Current() Snapshot {
	return Evaluate(CurrentFacts(), definitions)
}

// CurrentFacts gathers only stable platform and release identity. Hardware
// telemetry remains in the hardware API and does not alter feature ownership.
func CurrentFacts() Facts {
	version := strings.TrimSpace(os.Getenv("CLOUDLESS_VERSION"))
	if version == "" {
		version = BuildVersion
	}
	dgxVersion := strings.TrimSpace(os.Getenv("CLOUDLESS_DGX_OS_VERSION"))
	if dgxVersion == "" {
		dgxVersion = dgxOSVersion("/etc/dgx-release")
	}
	return Facts{
		Platform:         platform.Detect(),
		Architecture:     platform.Architecture(),
		CloudlessVersion: version,
		DGXOSVersion:     dgxVersion,
	}
}

// Evaluate resolves a registry deterministically against the supplied facts.
func Evaluate(facts Facts, registry map[string]Requirement) Snapshot {
	snapshot := Snapshot{Facts: facts, Features: make(map[string]Status, len(registry))}
	resolving := make(map[string]bool)
	var resolve func(string) Status
	resolve = func(id string) Status {
		if status, ok := snapshot.Features[id]; ok {
			return status
		}
		requirement, ok := registry[id]
		if !ok {
			return Status{Available: false, Reason: "Unknown required feature"}
		}
		if resolving[id] {
			return Status{Available: false, Reason: "Capability dependency cycle", Requirement: requirement}
		}
		resolving[id] = true
		status := evaluateBase(facts, requirement)
		if status.Available {
			for _, dependency := range requirement.RequiredFeatures {
				if required := resolve(dependency); !required.Available {
					status.Available = false
					status.Reason = "Requires " + dependency + ": " + required.Reason
					break
				}
			}
		}
		delete(resolving, id)
		snapshot.Features[id] = status
		return status
	}
	for id := range registry {
		resolve(id)
	}
	return snapshot
}

// Check evaluates an arbitrary feature/application requirement against a
// snapshot. RequiredFeatures are resolved from the snapshot's signed-in binary
// registry, so callers cannot enable a feature from the browser.
func Check(snapshot Snapshot, requirement Requirement) Status {
	status := evaluateBase(snapshot.Facts, requirement)
	if !status.Available {
		return status
	}
	for _, id := range requirement.RequiredFeatures {
		required, ok := snapshot.Features[id]
		if !ok || !required.Available {
			status.Available = false
			status.Reason = "Requires unavailable feature " + id
			return status
		}
	}
	return status
}

func evaluateBase(facts Facts, requirement Requirement) Status {
	status := Status{Available: true, Requirement: requirement}
	if len(requirement.Platforms) > 0 && !contains(requirement.Platforms, facts.Platform) {
		status.Available = false
		status.Reason = "Requires platform " + strings.Join(requirement.Platforms, " or ")
		return status
	}
	if len(requirement.Architectures) > 0 && !contains(requirement.Architectures, facts.Architecture) {
		status.Available = false
		status.Reason = "Requires architecture " + strings.Join(requirement.Architectures, " or ")
		return status
	}
	if requirement.MinCloudlessVersion != "" &&
		!versionAtLeast(facts.CloudlessVersion, requirement.MinCloudlessVersion) {
		status.Available = false
		status.Reason = "Requires CloudlessOS " + requirement.MinCloudlessVersion + " or newer"
		return status
	}
	if requirement.MinDGXOSVersion != "" &&
		!versionAtLeast(facts.DGXOSVersion, requirement.MinDGXOSVersion) {
		status.Available = false
		status.Reason = "Requires DGX OS " + requirement.MinDGXOSVersion + " or newer"
		return status
	}
	return status
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func versionAtLeast(current, minimum string) bool {
	currentParts, currentPre, currentOK := parseVersion(current)
	minimumParts, minimumPre, minimumOK := parseVersion(minimum)
	if !currentOK || !minimumOK {
		return false
	}
	length := len(currentParts)
	if len(minimumParts) > length {
		length = len(minimumParts)
	}
	for i := 0; i < length; i++ {
		var a, b int
		if i < len(currentParts) {
			a = currentParts[i]
		}
		if i < len(minimumParts) {
			b = minimumParts[i]
		}
		if a != b {
			return a > b
		}
	}
	if currentPre != minimumPre {
		return !currentPre
	}
	return true
}

func parseVersion(value string) ([]int, bool, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "v"))
	if value == "" || value == "development" {
		return nil, false, false
	}
	core := value
	pre := false
	if index := strings.IndexAny(core, "-~+"); index >= 0 {
		pre = core[index] != '+'
		core = core[:index]
	}
	fields := strings.Split(core, ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return nil, false, false
		}
		parts = append(parts, number)
	}
	return parts, pre, len(parts) > 0
}

func dgxOSVersion(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	for _, key := range []string{"DGX_OTA_VERSION", "DGX_SWBUILD_VERSION"} {
		if value := values[key]; value != "" {
			return value
		}
	}
	return ""
}
