package state

import "testing"

func TestModelDownloadJournalPersistsAndRemovesAtomically(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := ModelDownload{
		ModelID: "owner/model", Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Phase: "downloading",
		BytesDone: 7, BytesTotal: 19,
	}
	if err := store.SetModelDownload(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.ModelDownloads()
	if len(got) != 1 || got[0].ModelID != want.ModelID || got[0].Revision != want.Revision ||
		got[0].BytesDone != want.BytesDone || got[0].BytesTotal != want.BytesTotal ||
		got[0].Started == "" || got[0].Updated == "" {
		t.Fatalf("durable download = %#v", got)
	}
	if err := reopened.RemoveModelDownload(want.ModelID); err != nil {
		t.Fatal(err)
	}
	again, err := Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := again.ModelDownloads(); len(got) != 0 {
		t.Fatalf("removed durable download returned: %#v", got)
	}
}
