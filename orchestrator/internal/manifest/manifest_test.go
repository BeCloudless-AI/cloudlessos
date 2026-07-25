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

func TestPinRefForArchitecture(t *testing.T) {
	pin := Pin{
		Image:  "example/cloudless",
		Images: map[string]string{"arm64": "vendor/cloudless-arm"},
		Digests: map[string]string{
			"amd64": "sha256:amd",
			"arm64": "sha256:arm",
		},
	}
	if got := pin.RefFor("arm64"); got != "vendor/cloudless-arm@sha256:arm" {
		t.Fatalf("arm64 ref = %q", got)
	}
	if got := pin.RefFor("amd64"); got != "example/cloudless@sha256:amd" {
		t.Fatalf("amd64 ref = %q", got)
	}
	if got := pin.DigestFor("arm64"); got != "sha256:arm" {
		t.Fatalf("arm64 digest = %q", got)
	}
}

func TestLegacyPinDoesNotLeakAMD64DigestToARM64(t *testing.T) {
	pin := Pin{Image: "example/cloudless", Digest: "sha256:legacy"}
	if got := pin.RefFor("amd64"); got == "" {
		t.Fatal("legacy AMD64 pin was rejected")
	}
	if got := pin.RefFor("arm64"); got != "" {
		t.Fatalf("legacy digest was reused on ARM64: %q", got)
	}
}

func TestSharedIndexDigestDeclaresArchitectures(t *testing.T) {
	pin := Pin{
		Image:         "example/cloudless",
		Digest:        "sha256:index",
		Architectures: []string{"amd64", "arm64"},
	}
	if got := pin.RefFor("arm64"); got != "example/cloudless@sha256:index" {
		t.Fatalf("shared index ref = %q", got)
	}
}
