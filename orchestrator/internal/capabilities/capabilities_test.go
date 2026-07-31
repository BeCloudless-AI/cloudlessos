package capabilities

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/platform"
)

func TestDGXFeatureRegistryIsPlatformLocked(t *testing.T) {
	dgx := Evaluate(Facts{
		Platform: platform.DGXSpark, Architecture: "arm64",
		CloudlessVersion: "0.3.0", DGXOSVersion: "7.6.0",
	}, definitions)
	if !dgx.Features[DGXAppliance].Available || dgx.Features[GenericDriverUpdates].Available {
		t.Fatalf("unexpected DGX capabilities: %#v", dgx.Features)
	}
	if dgx.Features[DGXAppliance].SupportLevel != "supported" || dgx.Features[SparkCluster].SupportLevel != "preview" {
		t.Fatalf("unexpected DGX support levels: %#v", dgx.Features)
	}

	generic := Evaluate(Facts{
		Platform: platform.Generic, Architecture: "amd64", CloudlessVersion: "0.3.0",
	}, definitions)
	if generic.Features[DGXAppliance].Available || !generic.Features[GenericDriverUpdates].Available {
		t.Fatalf("unexpected generic capabilities: %#v", generic.Features)
	}
}

func TestRequirementCombinesPlatformArchitectureAndVersions(t *testing.T) {
	snapshot := Snapshot{
		Facts: Facts{
			Platform: platform.DGXSpark, Architecture: "arm64",
			CloudlessVersion: "0.4.1", DGXOSVersion: "7.6.0",
		},
		Features: map[string]Status{NVIDIACDI: {Available: true}},
	}
	requirement := Requirement{
		Platforms: []string{platform.DGXSpark}, Architectures: []string{"arm64"},
		MinCloudlessVersion: "0.4.0", MinDGXOSVersion: "7.5.0",
		RequiredFeatures: []string{NVIDIACDI},
	}
	if status := Check(snapshot, requirement); !status.Available {
		t.Fatalf("compatible requirement rejected: %#v", status)
	}
	snapshot.Facts.DGXOSVersion = "7.4.9"
	if status := Check(snapshot, requirement); status.Available || status.Reason == "" {
		t.Fatalf("old DGX OS accepted: %#v", status)
	}
}

func TestPrereleaseDoesNotSatisfyFinalMinimum(t *testing.T) {
	if versionAtLeast("0.3.0-dev", "0.3.0") {
		t.Fatal("development build satisfied final release requirement")
	}
	if !versionAtLeast("0.3.1", "0.3.0") {
		t.Fatal("newer release rejected")
	}
}

func TestFeatureDependenciesFailClosed(t *testing.T) {
	registry := map[string]Requirement{
		"base":    {Platforms: []string{platform.DGXSpark}},
		"feature": {RequiredFeatures: []string{"base"}},
	}
	snapshot := Evaluate(Facts{Platform: platform.Generic, Architecture: "amd64", CloudlessVersion: "1.0.0"}, registry)
	if snapshot.Features["feature"].Available {
		t.Fatalf("dependent feature ignored unavailable base: %#v", snapshot.Features)
	}
	if snapshot.Features["base"].SupportLevel != "experimental" || snapshot.Features["feature"].SupportLevel != "experimental" {
		t.Fatalf("unknown feature support must fail to experimental: %#v", snapshot.Features)
	}
}

func TestDGXVersionPrefersOTAVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dgx-release")
	if err := os.WriteFile(path, []byte("DGX_SWBUILD_VERSION=\"7.2.3\"\nDGX_OTA_VERSION=\"7.5.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dgxOSVersion(path); got != "7.5.0" {
		t.Fatalf("dgxOSVersion() = %q", got)
	}
}

func TestSparkClusterRequiresDGXSparkARM64(t *testing.T) {
	registry := map[string]Requirement{SparkCluster: definitions[SparkCluster]}
	ready := Evaluate(Facts{Platform: platform.DGXSpark, Architecture: "arm64"}, registry)
	if !ready.Features[SparkCluster].Available {
		t.Fatalf("Spark cluster unavailable: %#v", ready.Features[SparkCluster])
	}
	wrongArch := Evaluate(Facts{Platform: platform.DGXSpark, Architecture: "amd64"}, registry)
	if wrongArch.Features[SparkCluster].Available {
		t.Fatal("Spark cluster available on amd64")
	}
}
