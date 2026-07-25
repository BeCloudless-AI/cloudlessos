package provision

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/platform"
)

func TestDefaultModelPinKeyFollowsPlatform(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", platform.Generic)
	if got := defaultModelPinKey(); got != "default" {
		t.Fatalf("generic model pin = %q", got)
	}
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	if got := defaultModelPinKey(); got != "dgx-spark" {
		t.Fatalf("Spark model pin = %q", got)
	}
}

func TestCredentialComparisonRequiresExactEnvironmentEntry(t *testing.T) {
	const key = "cloudless-hermes-secret"
	env := "PATH=/usr/bin\nAPI_SERVER_KEY=" + key + "\nAPI_SERVER_ENABLED=true\n"
	if !hasEnvValue(env, "API_SERVER_KEY", key) {
		t.Fatal("exact Hermes credential was not detected")
	}
	if hasEnvValue(env, "API_SERVER_KEY", key+"-different") {
		t.Fatal("mismatched Hermes credential was accepted")
	}
}

func TestHasLineIgnoresCLIChatterButRequiresExactValue(t *testing.T) {
	output := "warning: using profile default\n65536\n"
	if !hasLine(output, "65536") {
		t.Fatal("configuration value was not detected")
	}
	if hasLine(output, "6553") {
		t.Fatal("partial configuration value was accepted")
	}
}
