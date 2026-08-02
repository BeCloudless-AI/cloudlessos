package recipeops

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestBeginPersistsDetachedRecipeSnapshot(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	recipe.Runtime.Environment = map[string]string{"MODE": "reviewed"}
	operation, err := store.Begin(KindRun, recipe, PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	recipe.Runtime.Environment["MODE"] = "mutated"
	operation.RecipeSnapshot.Runtime.Environment["MODE"] = "returned-mutation"
	persisted, ok := store.Get(operation.ID)
	if !ok || persisted.RecipeSnapshot.Runtime.Environment["MODE"] != "reviewed" {
		t.Fatalf("persisted snapshot = %#v", persisted.RecipeSnapshot.Runtime.Environment)
	}
	if revision, err := RecipeRevision(persisted.RecipeSnapshot); err != nil || revision != persisted.RecipeRevision {
		t.Fatalf("snapshot revision = %q, %v; want %q", revision, err, persisted.RecipeRevision)
	}
}

func TestBeginUniqueAtomicallyAdmitsOneOperation(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const contenders = 24
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := store.BeginUnique(KindCheck, testRecipe(), PhaseChecking)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 || len(store.List()) != 1 {
		t.Fatalf("successful admissions = %d, operations = %d", succeeded, len(store.List()))
	}
}

func TestTombstoneBlocksAdmissionUntilOwnedResourcesAreReconciled(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	operation, err := store.Begin(KindCheck, recipe, PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	resource := Resource{Kind: "staging-checkout", ID: "/var/lib/cloudless/test-checkout", Node: "test"}
	if _, err := store.Claim(operation.ID, resource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(operation.ID, PhaseFailed, errors.New("interrupted")); err != nil {
		t.Fatal(err)
	}
	marked := 0
	if err := store.Tombstone(recipe.ID, func() error { marked++; return nil }); err != nil {
		t.Fatal(err)
	}
	if marked != 1 || !store.IsTombstoned(recipe.ID) {
		t.Fatalf("tombstone was not persisted: marked=%d", marked)
	}
	if _, err := store.BeginUnique(KindCheck, recipe, PhaseChecking); !errors.Is(err, ErrRecipeTombstoned) {
		t.Fatalf("admission error = %v", err)
	}
	purged := 0
	if finalized, err := store.FinalizeTombstone(recipe.ID, func() error { purged++; return nil }); err != nil || finalized || purged != 0 {
		t.Fatalf("premature finalization = %v, purged=%d, err=%v", finalized, purged, err)
	}
	if _, err := store.Release(operation.ID, resource); err != nil {
		t.Fatal(err)
	}
	if finalized, err := store.FinalizeTombstone(recipe.ID, func() error { purged++; return nil }); err != nil || !finalized || purged != 1 {
		t.Fatalf("finalization = %v, purged=%d, err=%v", finalized, purged, err)
	}
	if store.IsTombstoned(recipe.ID) {
		t.Fatal("tombstone remained after verified cleanup")
	}
	if _, err := store.BeginUnique(KindCheck, recipe, PhaseChecking); err != nil {
		t.Fatalf("admission remained blocked: %v", err)
	}
}

func TestMutationBlockerTracksLifecycleAndCleanupObligations(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	checking, err := store.Begin(KindCheck, recipe, PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if blocker, ok := store.MutationBlocker(recipe.ID); !ok || blocker.ID != checking.ID {
		t.Fatalf("checking operation should block mutation: %#v, %v", blocker, ok)
	}
	if _, err := store.Transition(checking.ID, PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}
	if blocker, ok := store.MutationBlocker(recipe.ID); ok {
		t.Fatalf("completed read-only check should not block mutation: %#v", blocker)
	}

	running, err := store.Begin(KindRun, recipe, PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	resource := Resource{Kind: "container", ID: "recipe-engine"}
	if _, err := store.Claim(running.ID, resource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(running.ID, PhaseFailed, errors.New("launch failed")); err != nil {
		t.Fatal(err)
	}
	if blocker, ok := store.MutationBlocker(recipe.ID); !ok || blocker.ID != running.ID {
		t.Fatalf("failed operation with resources should block mutation: %#v, %v", blocker, ok)
	}
	if _, err := store.Release(running.ID, resource); err != nil {
		t.Fatal(err)
	}
	if blocker, ok := store.MutationBlocker(recipe.ID); ok {
		t.Fatalf("reconciled failed operation should not block mutation: %#v", blocker)
	}
}

func TestRecoveryAndCleanupClassification(t *testing.T) {
	for _, phase := range []Phase{PhaseChecking, PhasePreparing, PhaseSwitching, PhaseStarting, PhaseVerifying, PhaseActive, PhaseStopping, PhaseRecovering} {
		if !(Operation{Kind: KindRun, Phase: phase}).NeedsRecovery() {
			t.Errorf("phase %s should require recovery", phase)
		}
	}
	for _, phase := range []Phase{PhaseStopped, PhaseFailed, PhaseAborted} {
		if (Operation{Kind: KindRun, Phase: phase}).NeedsRecovery() {
			t.Errorf("phase %s should not require recovery", phase)
		}
	}
	if (Operation{Kind: KindCheck, Phase: PhasePrepared}).NeedsRecovery() {
		t.Fatal("completed Check should not require recovery")
	}
	for _, resource := range []Resource{{Kind: "container"}, {Kind: "compose-project"}, {Kind: "process-set"}, {Kind: "staging-checkout"}, {Kind: "stable-port"}} {
		if !resource.RequiresCleanup() {
			t.Errorf("%s should require cleanup", resource.Kind)
		}
	}
	for _, resource := range []Resource{{Kind: "image"}, {Kind: "model-cache"}, {Kind: "checkout"}} {
		if resource.RequiresCleanup() {
			t.Errorf("retained %s should not require cleanup", resource.Kind)
		}
	}
}

func TestProgressAndTransitionCheckpointsSurviveReload(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindRun, testRecipe(), PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordProgress(operation.ID, Progress{Stage: "downloading", Message: "Downloading weights", OverallPercent: 40, PhasePercent: 75, Completed: 3, Total: 8, BytesDone: 75, BytesTotal: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordProgress(operation.ID, Progress{Stage: "downloading", Message: "Late event", OverallPercent: 20, PhasePercent: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(operation.ID, PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := reloaded.Get(operation.ID)
	if !ok || persisted.Progress == nil || persisted.Progress.OverallPercent != 40 {
		t.Fatalf("persisted progress = %#v", persisted.Progress)
	}
	if len(persisted.Checkpoints) != 2 || persisted.Checkpoints[0].Phase != PhasePreparing || persisted.Checkpoints[1].Phase != PhasePrepared {
		t.Fatalf("persisted checkpoints = %#v", persisted.Checkpoints)
	}
}

func TestExecuteMutationIsAtomicWithOperationAdmission(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	entered := make(chan struct{})
	release := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- store.ExecuteMutation(recipe.ID, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	admissionDone := make(chan error, 1)
	go func() {
		_, err := store.BeginUnique(KindCheck, recipe, PhaseChecking)
		admissionDone <- err
	}()
	select {
	case err := <-admissionDone:
		t.Fatalf("operation admission escaped mutation lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
	if err := <-admissionDone; err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMutationReturnsDurableBlocker(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	operation, err := store.Begin(KindRun, recipe, PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = store.ExecuteMutation(recipe.ID, func() error { called = true; return nil })
	var conflict *MutationConflict
	if !errors.As(err, &conflict) || conflict.Operation.ID != operation.ID || called {
		t.Fatalf("mutation conflict = %#v, called=%v, err=%v", conflict, called, err)
	}
}

func TestPrunePreservesRecoveryAndNewestReusablePreflight(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	for index := 0; index < 5; index++ {
		operation, beginErr := store.Begin(KindRun, recipe, PhasePreparing)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if _, transitionErr := store.Transition(operation.ID, PhaseFailed, errors.New("expected test failure")); transitionErr != nil {
			t.Fatal(transitionErr)
		}
	}
	active, err := store.Begin(KindRun, recipe, PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	check, err := store.Begin(KindCheck, recipe, PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCheck(check.ID, CheckResult{ID: "contract", Status: CheckPass, Summary: "contract valid"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindPreflight(check.ID, "sha256:"+strings.Repeat("1", 64), "sha256:"+strings.Repeat("2", 64), "sha256:"+strings.Repeat("3", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(check.ID, PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Prune(2, 0); err != nil {
		t.Fatal(err)
	}
	operations := store.List()
	if len(operations) != 4 {
		t.Fatalf("retained operations = %d, want active + preflight + 2 history", len(operations))
	}
	if _, ok := store.Get(active.ID); !ok {
		t.Fatal("operation needing recovery was pruned")
	}
	if _, ok := store.Get(check.ID); !ok {
		t.Fatal("newest reusable preflight was pruned")
	}
}

func TestBeginRunValidatedRequiresExactLaunchablePreflight(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	if _, err := store.BeginRunValidated(recipe); err == nil {
		t.Fatal("run without preflight was admitted")
	}
	check, err := store.Begin(KindCheck, recipe, PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []CheckResult{
		{ID: "image", Status: CheckPass, Summary: "image resolved"},
		{ID: "contract", Status: CheckPass, Summary: "contract preserved"},
	} {
		if _, err := store.RecordCheck(check.ID, result); err != nil {
			t.Fatal(err)
		}
	}
	platform := "sha256:" + strings.Repeat("a", 64)
	cluster := "sha256:" + strings.Repeat("b", 64)
	if _, err := store.BindPreflight(check.ID, "sha256:"+strings.Repeat("c", 64), platform, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(check.ID, PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}
	run, err := store.BeginRunValidated(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if run.Preflight == nil || !run.Preflight.Launchable || run.Preflight.RecipeRevision != run.RecipeRevision {
		t.Fatalf("run preflight = %#v", run.Preflight)
	}

	recipe.Engine.ContainerPort++
	if _, err := store.BeginRunValidated(recipe); err == nil {
		t.Fatal("changed recipe revision reused old preflight")
	}
}

func TestBeginRunValidatedCapturesRollbackTargetAtomically(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	prepareLaunchableCheck(t, store, recipe)
	previous := PreviousRuntime{Engine: "vllm", Model: "owner/previous", ExecutionMode: "cluster"}
	operation, err := store.BeginRunValidatedWithPrevious(recipe, previous)
	if err != nil {
		t.Fatal(err)
	}
	if operation.PreviousRuntime == nil || *operation.PreviousRuntime != previous {
		t.Fatalf("rollback target = %#v", operation.PreviousRuntime)
	}
	previous.Model = "mutated"
	persisted, _ := store.Get(operation.ID)
	if persisted.PreviousRuntime == nil || persisted.PreviousRuntime.Model != "owner/previous" {
		t.Fatalf("persisted rollback target aliased caller: %#v", persisted.PreviousRuntime)
	}
}

func prepareLaunchableCheck(t *testing.T, store *Store, recipe localrecipes.Recipe) {
	t.Helper()
	check, err := store.Begin(KindCheck, recipe, PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCheck(check.ID, CheckResult{ID: "contract", Status: CheckPass, Summary: "contract preserved"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindPreflight(check.ID, "", "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(check.ID, PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWarningPreflightIsRunnableButNotFullyLaunchable(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	check, err := store.Begin(KindCheck, recipe, PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCheck(check.ID, CheckResult{ID: "image", Status: CheckWarning, Summary: "image will be built"}); err != nil {
		t.Fatal(err)
	}
	check, err = store.BindPreflight(check.ID, "", "sha256:"+strings.Repeat("d", 64), "sha256:"+strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	if check.Preflight == nil || !check.Preflight.Runnable || check.Preflight.Launchable {
		t.Fatalf("warning preflight = %#v", check.Preflight)
	}
	if _, err := store.Transition(check.ID, PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginRunValidated(recipe); err != nil {
		t.Fatalf("reviewed build recipe was not admitted: %v", err)
	}
}

func testRecipe() localrecipes.Recipe {
	return localrecipes.Recipe{
		ID: "local-0123456789abcdef", Name: "Test recipe", Platform: "dgx-spark",
		Source:      localrecipes.Source{URL: "https://github.com/cloudless/test", Revision: "abc123"},
		Engine:      localrecipes.Engine{Type: "vllm", Image: "example.invalid/vllm@sha256:abc", ContainerPort: 8890},
		Model:       localrecipes.Model{ID: "example/model", Revision: "def456", MaxContext: 4096},
		Distributed: localrecipes.Distributed{Nodes: 2, MasterPort: 25000},
		Runtime:     localrecipes.Runtime{Adapter: "native", TimeoutMinutes: 60},
		Health:      localrecipes.Health{Scheme: "http", Host: "127.0.0.1", Port: 8890, Path: "/health"},
	}
}

func TestLifecycleTransitions(t *testing.T) {
	valid := [][2]Phase{
		{"", PhaseChecking}, {"", PhasePreparing},
		{PhaseChecking, PhasePrepared},
		{PhasePreparing, PhasePrepared},
		{PhasePrepared, PhaseSwitching},
		{PhaseSwitching, PhaseStarting},
		{PhaseStarting, PhaseVerifying},
		{PhaseVerifying, PhaseActive},
		{PhaseActive, PhaseStopping},
		{PhaseStopping, PhaseStopped},
		{PhaseStarting, PhaseFailed},
		{PhasePreparing, PhaseAborted},
		{PhaseSwitching, PhaseRecovering},
		{PhaseRecovering, PhaseChecking},
		{PhaseRecovering, PhaseStopping},
	}
	for _, transition := range valid {
		if !ValidTransition(transition[0], transition[1]) {
			t.Errorf("expected valid transition %q -> %q", transition[0], transition[1])
		}
	}
	invalid := [][2]Phase{
		{"", PhaseActive},
		{PhaseChecking, PhaseStarting},
		{PhasePreparing, PhaseActive},
		{PhaseActive, PhasePreparing},
		{PhaseStopped, PhaseStarting},
		{PhaseFailed, PhasePreparing},
		{PhaseAborted, PhaseStarting},
	}
	for _, transition := range invalid {
		if ValidTransition(transition[0], transition[1]) {
			t.Errorf("expected invalid transition %q -> %q", transition[0], transition[1])
		}
	}
}

func TestRecipeRevisionIgnoresTimestampsButTracksRuntime(t *testing.T) {
	recipe := testRecipe()
	recipe.ImportedAt = "2026-01-01T00:00:00Z"
	first, err := RecipeRevision(recipe)
	if err != nil {
		t.Fatal(err)
	}
	recipe.ImportedAt = "2026-02-01T00:00:00Z"
	recipe.UpdatedAt = "2026-02-02T00:00:00Z"
	second, err := RecipeRevision(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("timestamps changed executable revision: %q != %q", first, second)
	}
	recipe.Engine.ContainerPort++
	third, err := RecipeRevision(recipe)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("runtime port change did not change executable revision")
	}
}

func TestStorePersistsTransitionsAndOwnership(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindCheck, testRecipe(), PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(operation.ID, "rop-") || len(operation.ID) != 36 {
		t.Fatalf("unexpected operation id %q", operation.ID)
	}
	resource := Resource{Kind: "container", ID: "cloudless-recipe-test", Node: "spark-a"}
	if _, err := store.Claim(operation.ID, resource); err != nil {
		t.Fatal(err)
	}
	resolved := strings.Repeat("b", 40)
	if _, err := store.BindResolvedSource(operation.ID, resolved); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCheck(operation.ID, CheckResult{
		ID: "source", Status: CheckPass, Summary: "Pinned source verified",
		Values: map[string]string{"commit": resolved},
	}); err != nil {
		t.Fatal(err)
	}
	platformFingerprint := "sha256:" + strings.Repeat("e", 64)
	clusterFingerprint := "sha256:" + strings.Repeat("f", 64)
	bound, err := store.BindPreflight(operation.ID, "sha256:"+strings.Repeat("d", 64), platformFingerprint, clusterFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Preflight == nil || !bound.Preflight.Launchable || !bound.Preflight.Matches(bound.RecipeRevision, platformFingerprint, clusterFingerprint) {
		t.Fatalf("bound preflight = %#v", bound.Preflight)
	}
	if _, err := store.RecordCheck(operation.ID, CheckResult{
		ID: "source", Status: CheckPass, Summary: "Pinned source verified again",
		Values: map[string]string{"commit": resolved},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(operation.ID, resource); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(operation.ID, PhasePrepared, nil); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := reopened.Get(operation.ID)
	if !ok {
		t.Fatal("operation was not persisted")
	}
	if persisted.Phase != PhasePrepared || persisted.ResolvedSourceRevision != resolved || len(persisted.Resources) != 1 || len(persisted.Checks) != 1 || persisted.Preflight == nil {
		t.Fatalf("persisted operation = %#v", persisted)
	}
	if persisted.Checks[0].Summary != "Pinned source verified again" || persisted.Checks[0].Values["commit"] != resolved {
		t.Fatalf("persisted check = %#v", persisted.Checks[0])
	}
	persisted.Checks[0].Values["commit"] = "mutated"
	persisted.Preflight.Checks[0].Values["commit"] = "mutated"
	again, _ := reopened.Get(operation.ID)
	if again.Checks[0].Values["commit"] != resolved || again.Preflight.Checks[0].Values["commit"] != resolved {
		t.Fatal("Get returned a mutable check values map")
	}
	if _, err := reopened.BindResolvedSource(operation.ID, strings.Repeat("c", 40)); err == nil {
		t.Fatal("operation accepted a different resolved source revision")
	}
	if _, err := reopened.Transition(operation.ID, PhaseActive, nil); err == nil {
		t.Fatal("invalid transition was accepted")
	}
	if _, err := reopened.Release(operation.ID, resource); err != nil {
		t.Fatal(err)
	}
	if current, _ := reopened.Get(operation.ID); len(current.Resources) != 0 {
		t.Fatalf("resource release was not persisted: %#v", current.Resources)
	}
}

func TestPreflightWarningIsNotLaunchableAndTamperingInvalidatesHash(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindCheck, testRecipe(), PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCheck(operation.ID, CheckResult{ID: "image", Status: CheckWarning, Summary: "Image will be built"}); err != nil {
		t.Fatal(err)
	}
	platformFingerprint := "sha256:" + strings.Repeat("a", 64)
	clusterFingerprint := "sha256:" + strings.Repeat("b", 64)
	operation, err = store.BindPreflight(operation.ID, "", platformFingerprint, clusterFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Preflight == nil || operation.Preflight.Launchable {
		t.Fatalf("warning preflight = %#v", operation.Preflight)
	}
	tampered := *operation.Preflight
	tampered.ClusterFingerprint = "sha256:" + strings.Repeat("c", 64)
	if tampered.Matches(operation.RecipeRevision, platformFingerprint, tampered.ClusterFingerprint) {
		t.Fatal("tampered preflight evidence remained valid")
	}
}

func TestRecordCheckRejectsUnknownStatus(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindCheck, testRecipe(), PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCheck(operation.ID, CheckResult{ID: "image", Status: "maybe", Summary: "Image"}); err == nil {
		t.Fatal("unknown check status was accepted")
	}
}

func TestBeginRejectsKindPhaseMismatch(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Begin(KindCheck, testRecipe(), PhasePreparing); err == nil {
		t.Fatal("check operation started in preparing phase")
	}
	if _, err := store.Begin(KindRun, testRecipe(), PhaseStopping); err == nil {
		t.Fatal("run operation started in stopping phase")
	}
}

func TestStorePersistsTerminalFailure(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindRun, testRecipe(), PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.Transition(operation.ID, PhaseFailed, errors.New("image digest mismatch"))
	if err != nil {
		t.Fatal(err)
	}
	if !failed.Terminal() || failed.Error != "image digest mismatch" {
		t.Fatalf("failed operation = %#v", failed)
	}
}

func TestCheckedOperationIsTerminalWhenPrepared(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindCheck, testRecipe(), PhaseChecking)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := store.Transition(operation.ID, PhasePrepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !checked.Terminal() {
		t.Fatalf("checked operation should be terminal: %#v", checked)
	}
}

func TestRuntimeNameAndActiveLookupSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindRun, testRecipe(), PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []Phase{PhasePrepared, PhaseSwitching, PhaseStarting, PhaseVerifying, PhaseActive} {
		operation, err = store.Transition(operation.ID, phase, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	wantName, err := RuntimeName(operation)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wantName, "cloudless-recipe-") || len(wantName) != len("cloudless-recipe-")+16 {
		t.Fatalf("runtime name = %q", wantName)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	active, ok := reopened.ActiveForRecipe(operation.RecipeID)
	if !ok || active.ID != operation.ID {
		t.Fatalf("active operation = %#v, %v", active, ok)
	}
	gotName, err := RuntimeName(active)
	if err != nil || gotName != wantName {
		t.Fatalf("restored runtime name = %q, %v; want %q", gotName, err, wantName)
	}
}

func TestOpenRejectsCorruptStore(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/recipe-operations/index.json"
	if err := os.MkdirAll(dir+"/recipe-operations", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"operations":{"bad":{"id":"different"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("corrupt operation store was accepted")
	}
}

func TestOpenAcceptsAuthenticHistoricalSnapshotAfterNormalizerMigration(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/recipe-operations/index.json"
	if err := os.MkdirAll(dir+"/recipe-operations", 0o755); err != nil {
		t.Fatal(err)
	}
	recipe := testRecipe()
	recipe.Engine.ServedModelName = "legacy-model-name"
	recipe.Engine.RestartPolicy = "unless-stopped"
	revision, err := snapshotRecipeRevision(recipe)
	if err != nil {
		t.Fatal(err)
	}
	operation := Operation{
		ID: "rop-legacy", Kind: KindStop, RecipeID: recipe.ID, RecipeRevision: revision,
		RecipeSnapshot: recipe, Phase: PhaseFailed, Sequence: 2,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(document{Version: documentVersion, Operations: map[string]Operation{operation.ID: operation}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("authentic historical snapshot was rejected: %v", err)
	}
	operation.RecipeRevision = "sha256:" + strings.Repeat("0", 64)
	payload, _ = json.Marshal(document{Version: documentVersion, Operations: map[string]Operation{operation.ID: operation}})
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("forged historical snapshot revision was accepted")
	}
}

func TestOpenAcceptsAuthenticHistoricalSnapshotBeforeNestedSchemaExpansion(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/recipe-operations/index.json"
	if err := os.MkdirAll(dir+"/recipe-operations", 0o755); err != nil {
		t.Fatal(err)
	}
	operation := json.RawMessage(`{
		"id":"rop-legacy-expanded-schema","kind":"stop","recipeId":"legacy-recipe",
		"recipeRevision":"sha256:placeholder",
		"recipeSnapshot":{
			"id":"legacy-recipe","name":"Legacy","description":"Historical snapshot","platform":"dgx-spark",
			"source":{"url":"","revision":""},
			"engine":{"type":"vllm","image":"example.invalid/image@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","servedModelName":"cloudless","containerPort":8890,"apiPath":"/v1","proxyHost":"host.docker.internal","restartPolicy":"no"},
			"model":{"id":"example/model","revision":"0123456789012345678901234567890123456789","quantization":"none","dtype":"auto","kvCacheDtype":"auto","maxContext":32768,"maxSequences":1,"gpuMemoryUtilization":0.8,"tensorParallel":1,"pipelineParallel":1,"trustRemoteCode":false},
			"distributed":{"nodes":1,"backend":"local","masterPort":29500,"interface":"","hca":"","ibGidIndex":0,"workerAlias":"worker"},
			"runtime":{"adapter":"managed-container-v1","workingDir":"","timeoutMinutes":120,"lifecycle":{"build":{"program":"","args":[]},"download":{"program":"","args":[]},"start":{"program":"","args":[]},"stop":{"program":"","args":[]}}},
			"health":{"scheme":"http","host":"127.0.0.1","port":8890,"path":"/health","timeoutSeconds":1800,"intervalSeconds":5}
		},
		"phase":"stopping","sequence":1,"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:01Z"
	}`)
	revision, err := rawPersistedSnapshotRevision(operation)
	if err != nil {
		t.Fatal(err)
	}
	operation = bytes.Replace(operation, []byte("sha256:placeholder"), []byte(revision), 1)
	payload, err := json.Marshal(map[string]any{"version": documentVersion, "operations": map[string]json.RawMessage{"rop-legacy-expanded-schema": operation}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("authentic pre-expansion snapshot was rejected: %v", err)
	}
	if _, err := store.Transition("rop-legacy-expanded-schema", PhaseStopped, nil); err != nil {
		t.Fatalf("historical operation mutation failed: %v", err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("historical snapshot identity was not preserved after mutation: %v", err)
	}
}

func TestPreparedImageIdentityIsImmutableAndPersists(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := store.Begin(KindRun, testRecipe(), PhasePreparing)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	operation, err = store.BindPreparedImage(operation.ID, "registry.example/runtime:tag@"+digest, digest)
	if err != nil {
		t.Fatal(err)
	}
	if operation.PreparedImageDigest != digest {
		t.Fatalf("prepared image = %#v", operation)
	}
	other := "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if _, err := store.BindPreparedImage(operation.ID, "registry.example/runtime:tag@"+other, other); err == nil {
		t.Fatal("prepared image identity changed after it was bound")
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := reopened.Get(operation.ID)
	if !ok || restored.PreparedImageReference != "registry.example/runtime:tag@"+digest {
		t.Fatalf("restored prepared image = %#v, %v", restored, ok)
	}
}
