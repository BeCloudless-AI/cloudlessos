package state

import "testing"

func TestRuntimeRestartOfferPersistsExactActiveRuntime(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := InferenceRuntime{Engine: "vllm", Model: "owner/model", ExecutionMode: "cluster", LocalRecipeID: "recipe-123"}
	if err := store.CommitInferenceRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-2"); err != nil {
		t.Fatal(err)
	}
	offer := store.Get().RuntimeRestartOffer
	if offer.InstanceID != "daemon-2" || offer.Runtime != runtime || offer.Created == "" {
		t.Fatalf("restart offer = %#v", offer)
	}
	if !store.Get().EngineUnloaded {
		t.Fatal("remembered runtime remained approved for unattended recovery")
	}
	reopened, err := Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get().RuntimeRestartOffer; got != offer {
		t.Fatalf("persisted restart offer = %#v, want %#v", got, offer)
	}
	if err := reopened.ClearRuntimeRestartOffer("stale-daemon"); err != nil {
		t.Fatal(err)
	}
	if reopened.Get().RuntimeRestartOffer.InstanceID != "daemon-2" {
		t.Fatal("a stale browser cleared a newer restart offer")
	}
	if err := reopened.ClearRuntimeRestartOffer("daemon-2"); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Get().RuntimeRestartOffer; got.InstanceID != "" {
		t.Fatalf("restart offer was not cleared: %#v", got)
	}
}

func TestAutomaticRuntimeRestartPreservesActiveRuntimeWithInformationalOffer(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := InferenceRuntime{Engine: "vllm", Model: "owner/model", LocalRecipeID: "recipe-123"}
	if err := store.CommitInferenceRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRuntimeRestartAutomatic(true); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-2"); err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if got.RuntimeRestartOffer.InstanceID != "daemon-2" || !got.RuntimeRestartOffer.Automatic || got.RuntimeRestartOffer.Runtime != runtime || got.EngineUnloaded || got.InferenceRuntime() != runtime {
		t.Fatalf("automatic restart state = %#v", got)
	}
	reopened, err := Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.RuntimeRestartAutomatic() {
		t.Fatal("automatic runtime restart preference was not persisted")
	}
}

func TestRuntimeRestartOfferSkipsIntentionallyUnloadedModel(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitInferenceRuntime(InferenceRuntime{Engine: "vllm", Model: "owner/model", EngineUnloaded: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareRuntimeRestartOffer("daemon-2"); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().RuntimeRestartOffer; got.InstanceID != "" {
		t.Fatalf("unloaded model produced restart offer: %#v", got)
	}
}
