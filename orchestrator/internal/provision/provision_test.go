package provision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/platform"
	"github.com/cloudless/orchestrator/internal/state"
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

func TestBootstrapOnlyStartsForNewOrInterruptedInstall(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	t.Setenv("CLOUDLESS_BOOTSTRAP_MODEL", defaultBootstrapModel)
	if !ShouldBootstrap(state.State{}, true) {
		t.Fatal("new Spark install should bootstrap before the large platform model")
	}
	if ShouldBootstrap(state.State{}, false) {
		t.Fatal("an upgraded install with implicit model must not be downgraded")
	}
	interrupted := state.State{ModelPromotion: state.ModelPromotion{Phase: "downloading", Target: "Qwen/Qwen3.6-35B-A3B"}}
	if !ShouldBootstrap(interrupted, false) {
		t.Fatal("an interrupted promotion must resume after reboot")
	}
	selected := state.State{Model: "Qwen/Custom"}
	if ShouldBootstrap(selected, true) {
		t.Fatal("an explicit user model must remain authoritative")
	}
	if ShouldBootstrap(state.State{ExecutionMode: "cluster"}, true) {
		t.Fatal("cluster execution must use the normal distributed launch path")
	}
}

func TestPromotionDownloadMessageIsBounded(t *testing.T) {
	if got := promotionDownloadMessage(150, 100); got != "Downloading the full Cloudless model in the background — 100%" {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestPromotionRequiresModelsAndRealChatContracts(t *testing.T) {
	modelsSeen, chatSeen := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			modelsSeen = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"cloudless"}]}`))
		case "/v1/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			chatSeen = body.Model == "cloudless"
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"READY"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForModelContractAt(ctx, server.URL); err != nil {
		t.Fatal(err)
	}
	if !modelsSeen || !chatSeen {
		t.Fatalf("contract probes: models=%v chat=%v", modelsSeen, chatSeen)
	}
}

func TestPendingBootstrapSurvivesMissingHardwareOnFirstDaemonBoot(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFirstLaunchSetup(state.FirstLaunchSetupInstall); err != nil {
		t.Fatal(err)
	}
	RecordBootstrapPending(store, "waiting for driver")
	p := store.Get().ModelPromotion
	if p.Phase != "waiting-hardware" || p.Target != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("pending promotion = %+v", p)
	}
	if !ShouldBootstrap(store.Get(), false) {
		t.Fatal("persisted pending promotion must resume on a later daemon boot")
	}
}

func TestPendingFirstLaunchDoesNotPrepareBootstrapWithoutConsent(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", platform.DGXSpark)
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	RecordBootstrapPending(store, "waiting for driver")
	if p := store.Get().ModelPromotion; p.Phase != "" || p.Target != "" {
		t.Fatalf("bootstrap was prepared without first-launch consent: %+v", p)
	}
}
