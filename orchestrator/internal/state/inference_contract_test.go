package state

import "testing"

func TestInferenceContractDefaultsAndPersists(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := store.InferenceContract(); got.Port != DefaultInferenceAPIPort || got.ModelAlias != DefaultInferenceAlias {
		t.Fatalf("default contract = %#v", got)
	}
	want := InferenceContract{Port: 18766, ModelAlias: "my-cloudless"}
	if err := store.SetInferenceContract(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.InferenceContract(); got != want {
		t.Fatalf("persisted contract = %#v, want %#v", got, want)
	}
}
