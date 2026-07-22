package manifest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func manifestServer(t *testing.T, image string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"manifestVersion":1,"apps":{"hermes":{"image":%q,"tag":"v1","digest":"sha256:abc"}}}`, image)
	}))
}

func TestPinForAcceptsMatchingRepository(t *testing.T) {
	server := manifestServer(t, "nousresearch/hermes-agent")
	defer server.Close()
	store := New(server.URL)
	pin, ok := store.PinFor(context.Background(), "hermes", "nousresearch/hermes-agent:v2026.7.20")
	if !ok || pin.Image != "nousresearch/hermes-agent" {
		t.Fatalf("matching pin rejected: %+v, %v", pin, ok)
	}
}

func TestPinForRejectsStaleRepositoryMigration(t *testing.T) {
	server := manifestServer(t, "samuelcardillo/cloudless-hermes")
	defer server.Close()
	store := New(server.URL)
	if pin, ok := store.PinFor(context.Background(), "hermes", "nousresearch/hermes-agent:v2026.7.20"); ok {
		t.Fatalf("stale incompatible pin accepted: %+v", pin)
	}
}

func TestImageRepositoryHandlesRegistryPortsAndDigests(t *testing.T) {
	got := imageRepository("registry.example:5000/cloudless/hermes:v1@sha256:abc")
	if got != "registry.example:5000/cloudless/hermes" {
		t.Fatalf("repository = %q", got)
	}
}
