package state

import "testing"

func TestDisplayPreferencePersists(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := DisplayPreference{Output: "DP-0", Width: 2560, Height: 1440}
	if err := store.SetDisplayPreference(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.DisplayPreference(); got != want {
		t.Fatalf("display preference = %+v, want %+v", got, want)
	}
}
