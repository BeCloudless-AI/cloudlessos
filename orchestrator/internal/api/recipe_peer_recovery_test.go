package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestRecipePeerRecoveryTargetsUseJournaledTopology(t *testing.T) {
	operation := recipeops.Operation{Resources: []recipeops.Resource{
		{Kind: "container-set", ID: "op", Node: "local"},
		{Kind: "container-set", ID: "op", Node: "spark-b", Locator: "worker"},
		{Kind: "compose-project", ID: "project-b", Node: "spark-b", Locator: "worker"},
		{Kind: "compose-project", ID: "project-b", Node: "spark-b", Locator: "worker"},
		{Kind: "compose-project", ID: "project-c", Node: "spark-c", Locator: "worker-2"},
	}}
	targets := recipePeerRecoveryTargets(operation, "local")
	if len(targets) != 2 || targets[0].Alias != "worker" || len(targets[0].ComposeProjects) != 1 || targets[1].Alias != "worker-2" {
		t.Fatalf("peer recovery targets = %#v", targets)
	}
}

func TestUnreachablePeerCleanupClaimSurvivesUntilAbsenceIsProven(t *testing.T) {
	previousRoot, previousSSH := recipeRuntimeRoot, recipeSSHExecutable
	recipeRuntimeRoot = t.TempDir()
	ssh := filepath.Join(t.TempDir(), "ssh")
	recipeSSHExecutable = ssh
	t.Cleanup(func() {
		recipeRuntimeRoot, recipeSSHExecutable = previousRoot, previousSSH
	})
	writeSSH := func(body string) {
		t.Helper()
		if err := os.WriteFile(ssh, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeSSH("echo 'peer unreachable' >&2; exit 255")

	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "peer cleanup",
		Model: localrecipes.Model{ID: "owner/model", Revision: "revision"},
	}
	if err := os.MkdirAll(recipeCheckout(recipe), 0o700); err != nil {
		t.Fatal(err)
	}
	operations, err := recipeops.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := operations.Begin(recipeops.KindRun, recipe, recipeops.PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	peerClaim := recipeops.Resource{Kind: "container-set", ID: operation.ID, Node: "spark-b", Locator: "worker"}
	if _, err := operations.Claim(operation.ID, peerClaim); err != nil {
		t.Fatal(err)
	}
	server := &Server{recipeOps: operations}
	remaining, err := server.releaseMissingRecipeResources(context.Background(), operation.ID, recipe)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0] != peerClaim {
		t.Fatalf("unreachable peer claim was cleared: %#v", remaining)
	}

	writeSSH("echo missing")
	remaining, err = server.releaseMissingRecipeResources(context.Background(), operation.ID, recipe)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("verified missing peer claim remains: %#v", remaining)
	}
	persisted, _ := operations.Get(operation.ID)
	for _, resource := range persisted.Resources {
		if resource == peerClaim {
			t.Fatalf("verified missing claim was not released: %#v", persisted.Resources)
		}
	}
}

func TestPeerInventoryCanProveExactContainerIsMissing(t *testing.T) {
	previousRoot, previousSSH := recipeRuntimeRoot, recipeSSHExecutable
	recipeRuntimeRoot = t.TempDir()
	binDir := t.TempDir()
	ssh := filepath.Join(binDir, "ssh")
	logPath := filepath.Join(binDir, "calls")
	recipeSSHExecutable = ssh
	t.Cleanup(func() {
		recipeRuntimeRoot, recipeSSHExecutable = previousRoot, previousSSH
	})
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + recipeShellQuote(logPath) + "\necho missing\n"
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "peer inventory",
		Model: localrecipes.Model{ID: "owner/model", Revision: "revision"},
	}
	if err := os.MkdirAll(recipeCheckout(recipe), 0o700); err != nil {
		t.Fatal(err)
	}
	inspector := &recipeResourceInspector{server: &Server{}, recipe: recipe, localNode: "spark-a"}
	observation := inspector.Inspect(context.Background(), recipeops.Resource{
		Kind: "container", ID: "container-id", Node: "spark-b", Locator: "worker",
	})
	if observation.Presence != recipeops.PresenceMissing {
		t.Fatalf("peer container observation = %#v", observation)
	}
	payload, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `container) docker container inspect "$identity"`) {
		t.Fatalf("peer inventory probe does not inspect exact containers: %q", payload)
	}
}

func TestPeerCleanupAttemptsEveryNodeWhenOneIsUnreachable(t *testing.T) {
	previousRoot, previousSSH := recipeRuntimeRoot, recipeSSHExecutable
	recipeRuntimeRoot = t.TempDir()
	binDir := t.TempDir()
	ssh := filepath.Join(binDir, "ssh")
	logPath := filepath.Join(binDir, "calls")
	recipeSSHExecutable = ssh
	t.Cleanup(func() {
		recipeRuntimeRoot, recipeSSHExecutable = previousRoot, previousSSH
	})
	script := "#!/bin/sh\nprintf '%s\\n' \"$3\" >> " + recipeShellQuote(logPath) + "\nif [ \"$3\" = worker-b ]; then exit 255; fi\nexit 0\n"
	if err := os.WriteFile(ssh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{ID: "local-0123456789abcdef", Name: "peer cleanup"}
	if err := os.MkdirAll(recipeCheckout(recipe), 0o700); err != nil {
		t.Fatal(err)
	}
	operation := recipeops.Operation{ID: "rop-peer-cleanup", Resources: []recipeops.Resource{
		{Kind: "container-set", ID: "rop-peer-cleanup", Node: "spark-b", Locator: "worker-b"},
		{Kind: "compose-project", ID: "project-b", Node: "spark-b", Locator: "worker-b"},
		{Kind: "container-set", ID: "rop-peer-cleanup", Node: "spark-c", Locator: "worker-c"},
	}}
	err := (&Server{}).cleanupInterruptedRecipePeers(context.Background(), operation, recipe)
	if err == nil || !strings.Contains(err.Error(), "spark-b") {
		t.Fatalf("peer cleanup error = %v", err)
	}
	payload, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	calls := strings.Fields(string(payload))
	if len(calls) != 2 || !containsString(calls, "worker-b") || !containsString(calls, "worker-c") {
		t.Fatalf("cleanup did not attempt every peer: %q", payload)
	}
}
