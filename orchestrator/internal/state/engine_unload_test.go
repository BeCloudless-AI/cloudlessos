package state

import "testing"

func TestEngineUnloadedPersistsUntilExplicitlyLoaded(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !store.Get().EngineUnloaded {
		t.Fatal("new installations must stay unloaded until the user chooses a setup path")
	}
	if err := store.SetEngineUnloaded(true); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Get().EngineUnloaded {
		t.Fatal("unloaded state did not survive reopening the state store")
	}
	if err := reopened.SetEngineUnloaded(false); err != nil {
		t.Fatal(err)
	}
	if reopened.Get().EngineUnloaded {
		t.Fatal("explicit model load did not clear unloaded state")
	}
}
