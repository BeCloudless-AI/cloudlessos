package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/securityaudit"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestRedactRemovesCredentialsAndIdentity(t *testing.T) {
	input := `Authorization: Bearer hf_abcdef password=hunter2 api_key=topsecret "hash": "deadbeef" sk-cloudless-abc123`
	got := Redact(input)
	for _, secret := range []string{"hf_abcdef", "hunter2", "topsecret", "deadbeef", "sk-cloudless-abc123"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q survived redaction: %s", secret, got)
		}
	}
}

func TestSupportBundleRedactsContainerLogs(t *testing.T) {
	oldStatusPath := tailscaleInstallStatusPath
	tailscaleInstallStatusPath = filepath.Join(t.TempDir(), "tailscale-install.json")
	t.Cleanup(func() { tailscaleInstallStatusPath = oldStatusPath })
	if err := os.WriteFile(tailscaleInstallStatusPath, []byte(`{"state":"failed","message":"Repository verification failed"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng := bundleEngine{logs: "token=hf_private password=secret"}
	securityaudit.New(store.Dir()).Append(securityaudit.Event{
		Category: "gateway", Event: "auth", Outcome: "failed", Detail: "Bearer bundle-secret",
	})
	bundle, err := BuildBundle(context.Background(), eng, store, Report{Platform: "test", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	foundAudit, foundTailscaleStatus := false, false
	for _, file := range archive.File {
		foundAudit = foundAudit || file.Name == "security-audit.json"
		foundTailscaleStatus = foundTailscaleStatus || file.Name == "tailscale-install.json"
		reader, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, _ := io.ReadAll(reader)
		reader.Close()
		if strings.Contains(string(data), "hf_private") || strings.Contains(string(data), "password=secret") {
			t.Fatalf("bundle entry %s leaked a secret: %s", file.Name, data)
		}
		if strings.HasSuffix(file.Name, ".json") && !json.Valid(data) {
			t.Fatalf("redaction corrupted JSON entry %s: %s", file.Name, data)
		}
	}
	if !foundAudit {
		t.Fatal("support bundle omitted the unified security audit")
	}
	if !foundTailscaleStatus {
		t.Fatal("support bundle omitted the Tailscale installer failure")
	}
}

func TestDoctorRespectsIntentionalModelUnload(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEngineUnloaded(true); err != nil {
		t.Fatal(err)
	}
	eng := bundleEngine{containers: []engine.Container{
		{Name: "cloudless-open-webui", State: "running"},
		{Name: "cloudless-hermes", State: "running"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	report := Run(ctx, eng, store)
	for _, check := range report.Checks {
		if check.ID == "app-vllm" && check.Status == "fail" {
			t.Fatal("intentionally unloaded engine was reported as failed")
		}
	}
}

func TestOrphanedRecipeRuntimeOffersDistributedStopRepair(t *testing.T) {
	recipe := localrecipes.Recipe{
		ID: "deepseek-dual", Name: "DeepSeek Dual Spark",
		Engine: localrecipes.Engine{Image: "example/deepseek:reviewed"},
	}
	report := AddOrphanedRecipeChecks(
		Report{Overall: "healthy"},
		state.State{EngineUnloaded: true},
		[]engine.Container{{Name: "deepseek-dual-vllm-1", Image: recipe.Engine.Image, State: "running"}},
		[]localrecipes.Recipe{recipe},
	)
	if report.Overall != "needs-attention" || len(report.Checks) != 1 {
		t.Fatalf("orphaned runtime report = %#v", report)
	}
	check, repair := report.Checks[0], report.Repairs[report.Checks[0].ID]
	if repair.Action != "stop-orphaned-recipe" || repair.Target != recipe.ID || check.Status != "fail" {
		t.Fatalf("orphaned runtime repair = %#v, %#v", check, repair)
	}
}

func TestRunningRecipeIsNotOrphanedWhileCloudlessOwnsIt(t *testing.T) {
	recipe := localrecipes.Recipe{ID: "owned", Engine: localrecipes.Engine{Image: "example/owned:1"}}
	report := AddOrphanedRecipeChecks(
		Report{Overall: "healthy"},
		state.State{EngineUnloaded: false},
		[]engine.Container{{Name: "owned-vllm-1", Image: recipe.Engine.Image, State: "running"}},
		[]localrecipes.Recipe{recipe},
	)
	if len(report.Checks) != 0 || report.Overall != "healthy" {
		t.Fatalf("owned runtime was reported as orphaned: %#v", report)
	}
}

func TestReconcileInferenceEndpointUsesStableRuntimeContract(t *testing.T) {
	report := Report{Overall: "needs-attention", Checks: []Check{
		{ID: "app-vllm", Name: "vLLM", Status: "fail", Summary: "Required service is not running."},
		{ID: "container-runtime", Name: "Container runtime", Status: "pass"},
	}}
	current := state.State{Engine: "vllm", LocalRecipeID: "local-recipe", ExecutionMode: "cluster"}
	got := ReconcileInferenceEndpoint(report, current, true, nil)
	if got.Overall != "healthy" {
		t.Fatalf("overall = %q, checks = %#v", got.Overall, got.Checks)
	}
	for _, check := range got.Checks {
		if check.ID == "app-vllm" {
			t.Fatalf("legacy container-name check survived: %#v", got.Checks)
		}
	}
	if got.Checks[len(got.Checks)-1].ID != "inference-engine" || got.Checks[len(got.Checks)-1].Status != "pass" {
		t.Fatalf("stable endpoint check = %#v", got.Checks)
	}
}

func TestReconcileInferenceEndpointDistinguishesStartingFromStopped(t *testing.T) {
	starting := ReconcileInferenceEndpoint(Report{Checks: []Check{{ID: "app-vllm", Status: "pass"}}}, state.State{Engine: "vllm"}, true, os.ErrDeadlineExceeded)
	if starting.Checks[0].Status != "warning" {
		t.Fatalf("starting runtime = %#v", starting.Checks)
	}
	stopped := ReconcileInferenceEndpoint(Report{Checks: []Check{{ID: "app-vllm", Status: "pass"}}}, state.State{Engine: "vllm"}, false, os.ErrNotExist)
	if stopped.Checks[0].Status != "fail" {
		t.Fatalf("stopped runtime = %#v", stopped.Checks)
	}
}

func TestHermesDoctorContractUsesAgentAPI(t *testing.T) {
	hermes := catalog.App{ID: "hermes", Health: catalog.HealthContract{Port: catalog.HermesDashboardPort, Path: "/"}}
	port, path := appHealthEndpoint(hermes)
	if port != catalog.HermesAPIPort || path != "/health" {
		t.Fatalf("Hermes Doctor endpoint = %d%s", port, path)
	}
}

func TestSparkClusterChecksReportUnreadableMigratedState(t *testing.T) {
	checks := sparkClusterChecks(sparkcluster.State{}, os.ErrPermission)
	if len(checks) != 1 || checks[0].ID != "spark-cluster-state" || checks[0].Status != "fail" {
		t.Fatalf("sparkClusterChecks() = %#v", checks)
	}
	if !strings.Contains(checks[0].Action, "Do not disconnect") {
		t.Fatalf("cluster recovery guidance is destructive or incomplete: %#v", checks[0])
	}
}

func TestSparkClusterChecksRequireNetworkProbeCapability(t *testing.T) {
	checks := sparkClusterChecks(sparkcluster.State{Configured: true}, nil)
	if len(checks) != 2 || checks[0].Status != "pass" {
		t.Fatalf("sparkClusterChecks() = %#v", checks)
	}
	// Test the parser separately so the test runner's own capabilities do not
	// determine the expected Doctor result.
	if effectiveCapability([]byte("Name:\ttest\nCapEff:\t0000000000000000\n"), capNetRaw) {
		t.Fatal("zero effective capability set reported CAP_NET_RAW")
	}
	if !effectiveCapability([]byte("CapEff:\t0000000000002000\n"), capNetRaw) {
		t.Fatal("CAP_NET_RAW bit was not detected")
	}
}

func TestSparkClusterStateActionDoesNotExposeLocalPaths(t *testing.T) {
	checks := sparkClusterChecks(sparkcluster.State{}, &os.PathError{Op: "open", Path: filepath.Join("private", "cluster", "state.json"), Err: os.ErrPermission})
	if len(checks) != 1 || !strings.Contains(checks[0].Summary, "permission denied") {
		t.Fatalf("sparkClusterChecks() = %#v", checks)
	}
}

type bundleEngine struct {
	logs       string
	containers []engine.Container
}

func (bundleEngine) Available(context.Context) error                          { return nil }
func (bundleEngine) EnsureNetwork(context.Context, string) error              { return nil }
func (bundleEngine) EnsureVolume(context.Context, string) error               { return nil }
func (bundleEngine) VolumeMountpoint(context.Context, string) (string, error) { return "", nil }
func (bundleEngine) ListVolumes(context.Context) ([]string, error)            { return nil, nil }
func (bundleEngine) RemoveVolume(context.Context, string) error               { return nil }
func (bundleEngine) ConnectNetwork(context.Context, string, string) error     { return nil }
func (bundleEngine) HasAlias(context.Context, string, string) (bool, error)   { return true, nil }
func (bundleEngine) ContainerEnvironment(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (bundleEngine) HermesConfigValue(context.Context, string, string) (string, error) {
	return "", nil
}
func (bundleEngine) HasNVIDIARuntime(context.Context) (bool, error) { return true, nil }
func (bundleEngine) ContainerNamesByLabel(context.Context, string, string) ([]string, error) {
	return nil, nil
}
func (bundleEngine) ContainerNamesByAncestor(context.Context, string) ([]string, error) {
	return nil, nil
}
func (bundleEngine) LogsTail(context.Context, string, int) (string, error)     { return "", nil }
func (bundleEngine) Exec(context.Context, string, ...string) error             { return nil }
func (bundleEngine) Pull(context.Context, string) error                        { return nil }
func (bundleEngine) PullStream(context.Context, string, func(string)) error    { return nil }
func (bundleEngine) Build(context.Context, string, string, func(string)) error { return nil }
func (bundleEngine) RemoveImage(context.Context, string) error                 { return nil }
func (bundleEngine) InspectImage(context.Context, string) (engine.ImageInfo, error) {
	return engine.ImageInfo{}, nil
}
func (bundleEngine) TagImage(context.Context, string, string) error { return nil }
func (bundleEngine) RemoteImageManifest(context.Context, string) (string, error) {
	return "", nil
}
func (bundleEngine) RemoteImageConfig(context.Context, string) (string, error) {
	return "", nil
}
func (bundleEngine) ExportImage(context.Context, string, string) error { return nil }
func (bundleEngine) ListImageDigests(context.Context) ([]engine.ImageDigestRef, error) {
	return nil, nil
}
func (bundleEngine) Run(context.Context, engine.RunSpec) (string, error) { return "", nil }
func (bundleEngine) RunTransient(context.Context, engine.RunSpec) (string, error) {
	return "", nil
}
func (bundleEngine) Stop(context.Context, string) error   { return nil }
func (bundleEngine) Remove(context.Context, string) error { return nil }

func (e bundleEngine) List(context.Context) ([]engine.Container, error) {
	if e.containers != nil {
		return e.containers, nil
	}
	return []engine.Container{{Name: "cloudless-hermes", State: "running"}}, nil
}
func (bundleEngine) Find(context.Context, string) (*engine.Container, error)      { return nil, nil }
func (e bundleEngine) Logs(context.Context, string) (string, error)               { return e.logs, nil }
func (bundleEngine) ImageDigest(context.Context, string) (string, error)          { return "", nil }
func (bundleEngine) RemoteDigest(context.Context, string) (string, error)         { return "", nil }
func (bundleEngine) ContainerImageDigest(context.Context, string) (string, error) { return "", nil }
