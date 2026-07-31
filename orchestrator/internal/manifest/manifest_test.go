package manifest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	store := New(server.URL + "/manifest.json")
	pin, ok := store.PinFor(context.Background(), "hermes", "nousresearch/hermes-agent:v2026.7.20")
	if !ok || pin.Image != "nousresearch/hermes-agent" {
		t.Fatalf("matching pin rejected: %+v, %v", pin, ok)
	}
}

func TestPinForRejectsStaleRepositoryMigration(t *testing.T) {
	server := manifestServer(t, "samuelcardillo/cloudless-hermes")
	defer server.Close()
	store := New(server.URL + "/manifest.json")
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

func TestSignedManifestIsAcceptedOnlyAfterVerification(t *testing.T) {
	server := manifestServer(t, "nousresearch/hermes-agent")
	defer server.Close()
	t.Setenv("CLOUDLESS_MANIFEST_REQUIRE_SIGNATURE", "1")
	original := verifyManifestSignature
	t.Cleanup(func() { verifyManifestSignature = original })
	called := false
	verifyManifestSignature = func(document, signature []byte, keyring string) error {
		called = true
		if len(document) == 0 || len(signature) == 0 {
			t.Fatal("verifier did not receive both artifacts")
		}
		return nil
	}
	store := New(server.URL + "/manifest.json")
	if _, err := store.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("signed production manifest bypassed verification")
	}
}

func TestInvalidManifestSignatureFailsClosed(t *testing.T) {
	server := manifestServer(t, "nousresearch/hermes-agent")
	defer server.Close()
	t.Setenv("CLOUDLESS_MANIFEST_REQUIRE_SIGNATURE", "1")
	original := verifyManifestSignature
	t.Cleanup(func() { verifyManifestSignature = original })
	verifyManifestSignature = func(document, signature []byte, keyring string) error {
		return errors.New("bad signature")
	}
	store := New(server.URL + "/manifest.json")
	if _, err := store.Get(context.Background()); err == nil {
		t.Fatal("manifest with an invalid signature was accepted")
	}
}

func TestSignedManifestSurvivesRestartFromVerifiedOfflineCache(t *testing.T) {
	t.Setenv("CLOUDLESS_MANIFEST_REQUIRE_SIGNATURE", "1")
	t.Setenv("CLOUDLESS_MANIFEST_CACHE_DIR", t.TempDir())
	original := verifyManifestSignature
	t.Cleanup(func() { verifyManifestSignature = original })
	verifyManifestSignature = func(document, signature []byte, keyring string) error {
		if len(document) == 0 || len(signature) == 0 {
			return errors.New("missing signed cache material")
		}
		return nil
	}
	server := manifestServer(t, "nousresearch/hermes-agent")
	url := server.URL + "/manifest.json"
	if _, err := New(url).Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	doc, err := New(url).Get(context.Background())
	if err != nil || doc == nil || doc.Apps["hermes"].Digest != "sha256:abc" {
		t.Fatalf("verified offline cache was not used: doc=%#v err=%v", doc, err)
	}
}

func TestTamperedSignedOfflineCacheIsRejected(t *testing.T) {
	t.Setenv("CLOUDLESS_MANIFEST_REQUIRE_SIGNATURE", "1")
	t.Setenv("CLOUDLESS_MANIFEST_CACHE_DIR", t.TempDir())
	original := verifyManifestSignature
	t.Cleanup(func() { verifyManifestSignature = original })
	verifyManifestSignature = func(document, signature []byte, keyring string) error {
		if strings.Contains(string(document), "tampered") {
			return errors.New("bad signature")
		}
		return nil
	}
	server := manifestServer(t, "nousresearch/hermes-agent")
	url := server.URL + "/manifest.json"
	if _, err := New(url).Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	documentPath, _ := manifestCachePaths(url)
	if err := os.WriteFile(documentPath, []byte(`{"manifestVersion":1,"channel":"tampered"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if _, err := New(url).Get(context.Background()); err == nil {
		t.Fatal("tampered signed cache was accepted")
	}
}

func TestUnsupportedManifestVersionIsRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"manifestVersion":99,"apps":{}}`))
	}))
	defer server.Close()
	if _, err := New(server.URL + "/manifest.json").Get(context.Background()); err == nil {
		t.Fatal("unknown manifest schema was accepted")
	}
}

func modelManifestServer(t *testing.T, version int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".asc") {
			_, _ = w.Write([]byte("signature"))
			return
		}
		fmt.Fprintf(w, `{"manifestVersion":%d,"highlights":[{"id":"example/model","name":"Example","fitProfiles":[{"id":"profile","evidence":"measured","source":"lab","engine":"vllm","architectures":["amd64"],"memoryTypes":["dedicated"],"minNodes":1,"maxNodes":1,"contextK":32,"requiredPerNodeGB":12}]}]}`, version)
	}))
}

func TestUnsignedModelManifestCannotSupplyFitProfiles(t *testing.T) {
	server := modelManifestServer(t, 2)
	defer server.Close()
	highlights := NewModels(server.URL + "/cloudless-models.json").Highlights(context.Background())
	if len(highlights) != 1 || len(highlights[0].FitProfiles) != 0 {
		t.Fatalf("unsigned model manifest supplied launch evidence: %#v", highlights)
	}
}

func TestSignedV2ModelManifestCanSupplyFitProfiles(t *testing.T) {
	server := modelManifestServer(t, 2)
	defer server.Close()
	t.Setenv("CLOUDLESS_MANIFEST_REQUIRE_SIGNATURE", "1")
	original := verifyManifestSignature
	t.Cleanup(func() { verifyManifestSignature = original })
	verifyManifestSignature = func(document, signature []byte, keyring string) error { return nil }
	highlights := NewModels(server.URL + "/cloudless-models.json").Highlights(context.Background())
	if len(highlights) != 1 || len(highlights[0].FitProfiles) != 1 ||
		highlights[0].FitProfiles[0].Evidence != "measured" {
		t.Fatalf("verified model fit profile was not preserved: %#v", highlights)
	}
}

func TestSignedLegacyModelManifestCannotSupplyFitProfiles(t *testing.T) {
	server := modelManifestServer(t, 1)
	defer server.Close()
	t.Setenv("CLOUDLESS_MANIFEST_REQUIRE_SIGNATURE", "1")
	original := verifyManifestSignature
	t.Cleanup(func() { verifyManifestSignature = original })
	verifyManifestSignature = func(document, signature []byte, keyring string) error { return nil }
	highlights := NewModels(server.URL + "/cloudless-models.json").Highlights(context.Background())
	if len(highlights) != 1 || len(highlights[0].FitProfiles) != 0 {
		t.Fatalf("legacy schema supplied launch evidence: %#v", highlights)
	}
}

func TestInvalidSignedModelManifestFailsClosed(t *testing.T) {
	server := modelManifestServer(t, 2)
	defer server.Close()
	t.Setenv("CLOUDLESS_MANIFEST_REQUIRE_SIGNATURE", "1")
	original := verifyManifestSignature
	t.Cleanup(func() { verifyManifestSignature = original })
	verifyManifestSignature = func(document, signature []byte, keyring string) error {
		return errors.New("bad signature")
	}
	if highlights := NewModels(server.URL + "/cloudless-models.json").Highlights(context.Background()); highlights != nil {
		t.Fatalf("invalid signed model manifest was accepted: %#v", highlights)
	}
}
