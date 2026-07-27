package state

import "testing"

func TestModelPromotionPersistsTimeline(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := ModelPromotion{Phase: "downloading", Bootstrap: "small", Target: "full", Active: "small"}
	if err := store.SetModelPromotion(p); err != nil {
		t.Fatal(err)
	}
	got := store.Get().ModelPromotion
	if got.Started == "" || got.Updated == "" {
		t.Fatalf("promotion timestamps were not stamped: %+v", got)
	}
	if got.Target != "full" || got.Active != "small" {
		t.Fatalf("promotion state was not preserved: %+v", got)
	}
}

func TestInstalledPacksAreDurableAndIdempotent(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPackInstalled("research", true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPackInstalled("research", true); err != nil {
		t.Fatal(err)
	}
	if got := store.Packs(); len(got) != 1 || got[0] != "research" {
		t.Fatalf("packs = %v", got)
	}
	if err := store.SetPackInstalled("research", false); err != nil {
		t.Fatal(err)
	}
	if got := store.Packs(); len(got) != 0 {
		t.Fatalf("removed pack persisted: %v", got)
	}
}
