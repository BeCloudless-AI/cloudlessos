package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
)

type recipeSmokeEngine struct {
	engine.Engine
	spec        engine.RunSpec
	err         error
	calls       int
	stoppedName string
	removedName string
}

func (runtime *recipeSmokeEngine) RunTransient(_ context.Context, spec engine.RunSpec) (string, error) {
	runtime.calls++
	runtime.spec = spec
	return "ok", runtime.err
}

func (runtime *recipeSmokeEngine) Stop(_ context.Context, name string) error {
	runtime.stoppedName = name
	return nil
}

func (runtime *recipeSmokeEngine) Remove(_ context.Context, name string) error {
	runtime.removedName = name
	return nil
}

func TestRecipeImageSmokeTestIsIsolatedAndBounded(t *testing.T) {
	runtime := &recipeSmokeEngine{}
	smoke := &localrecipes.ContainerSmokeTest{
		Program: "/opt/runtime-venv/bin/python", Args: []string{"-c", "from xgrammar import normalize_tool_choice"}, TimeoutSeconds: 45,
	}
	if err := runRecipeImageSmokeTest(context.Background(), runtime, "sha256:runtime", smoke); err != nil {
		t.Fatal(err)
	}
	if runtime.calls != 1 || runtime.spec.Image != "sha256:runtime" || runtime.spec.EntryPoint != smoke.Program || !strings.HasPrefix(runtime.spec.Name, "cloudless-recipe-smoke-") {
		t.Fatalf("unexpected transient smoke spec: %#v", runtime.spec)
	}
	if runtime.spec.Network != "none" || runtime.spec.GPUs != "" || !runtime.spec.ReadOnly || runtime.spec.Memory != "4g" || runtime.spec.MemorySwap != "4g" {
		t.Fatalf("smoke test escaped its bounded no-network/no-GPU profile: %#v", runtime.spec)
	}
	if len(runtime.spec.CapDrop) != 1 || runtime.spec.CapDrop[0] != "ALL" || len(runtime.spec.SecurityOpts) != 1 || runtime.spec.PidsLimit != 256 {
		t.Fatalf("smoke test hardening is incomplete: %#v", runtime.spec)
	}
}

func TestRecipeImageSmokeTestReportsContainerFailure(t *testing.T) {
	runtime := &recipeSmokeEngine{err: errors.New("missing symbol")}
	err := runRecipeImageSmokeTest(context.Background(), runtime, "sha256:runtime", &localrecipes.ContainerSmokeTest{
		Program: "python", Args: []string{"-c", "pass"}, TimeoutSeconds: 30,
	})
	if err == nil || !strings.Contains(err.Error(), "missing symbol") {
		t.Fatalf("expected contained smoke failure, got %v", err)
	}
	if runtime.stoppedName == "" || runtime.stoppedName != runtime.spec.Name || runtime.removedName != runtime.spec.Name {
		t.Fatalf("failed smoke container was not cleaned up: spec=%q stop=%q remove=%q", runtime.spec.Name, runtime.stoppedName, runtime.removedName)
	}
}

func TestRecipeImageSmokeTestIsOptional(t *testing.T) {
	runtime := &recipeSmokeEngine{}
	if err := runRecipeImageSmokeTest(context.Background(), runtime, "sha256:runtime", nil); err != nil || runtime.calls != 0 {
		t.Fatalf("optional smoke test unexpectedly ran: err=%v calls=%d", err, runtime.calls)
	}
}
