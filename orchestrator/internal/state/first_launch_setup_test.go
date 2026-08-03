package state

import "testing"

func TestFreshInstallWaitsForExplicitSetupChoice(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := store.FirstLaunchSetup(); got != FirstLaunchSetupPending {
		t.Fatalf("fresh setup choice = %q, want pending", got)
	}
	if !store.FirstLaunchSetupRequired() || store.StartupProvisioningEnabled() {
		t.Fatal("fresh installation must not provision before consent")
	}
	if !store.Get().EngineUnloaded {
		t.Fatal("fresh installation must accurately report inference as unloaded")
	}
}

func TestFirstLaunchChoicesControlStartupProvisioning(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFirstLaunchSetup(FirstLaunchSetupManual); err != nil {
		t.Fatal(err)
	}
	if store.StartupProvisioningEnabled() || !store.Get().EngineUnloaded {
		t.Fatal("manual setup must remain inert and unloaded")
	}
	if err := store.SetModel("example/model"); err != nil {
		t.Fatal(err)
	}
	if !store.StartupProvisioningEnabled() {
		t.Fatal("a manually selected model must resume normally on later boots")
	}
	if err := store.SetFirstLaunchSetup(FirstLaunchSetupInstall); err != nil {
		t.Fatal(err)
	}
	if !store.StartupProvisioningEnabled() || store.Get().EngineUnloaded {
		t.Fatal("install choice must enable provisioning and load inference")
	}
}

func TestLegacyStateKeepsExistingProvisioningBehavior(t *testing.T) {
	store := &Store{st: State{}}
	if got := store.FirstLaunchSetup(); got != FirstLaunchSetupInstall {
		t.Fatalf("legacy setup choice = %q, want install", got)
	}
	if !store.StartupProvisioningEnabled() {
		t.Fatal("upgrade must not disable an existing installation")
	}
}

func TestFirstLaunchChoiceRejectsUnknownValue(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFirstLaunchSetup("surprise"); err == nil {
		t.Fatal("unknown first-launch choice was accepted")
	}
	if got := store.FirstLaunchSetup(); got != FirstLaunchSetupPending {
		t.Fatalf("invalid choice mutated setup state to %q", got)
	}
}
