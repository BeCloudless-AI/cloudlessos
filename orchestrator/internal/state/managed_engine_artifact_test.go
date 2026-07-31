package state

import "testing"

func TestManagedEngineArtifactPersists(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := ManagedEngineArtifact{
		EngineID: "vllm", Image: "example/vllm@sha256:abc",
		DownloadedDigest: "sha256:abc", ActiveDigest: "sha256:abc",
		Source: "cloudless", Channel: "stable", Verified: true,
	}
	if err := store.SetManagedEngineArtifact(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.ManagedEngineArtifact("vllm")
	if !ok || got.EngineID != want.EngineID || got.Image != want.Image ||
		got.DownloadedDigest != want.DownloadedDigest || got.ActiveDigest != want.ActiveDigest ||
		got.Source != want.Source || got.Channel != want.Channel || !got.Verified || got.Updated == "" {
		t.Fatalf("managed artifact did not survive reopen: %#v, ok=%v", got, ok)
	}
}
