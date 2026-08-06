package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testBroker(t *testing.T, authorize BrokerAuthorizer) (*BrokerClient, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(dir, "missing-docker.sock"))
	binary := filepath.Join(dir, "docker")
	script := `#!/bin/sh
if [ "$1" = pull ]; then
  printf '%s\n' 'layer-a: Pulling fs layer' 'layer-a: Pull complete'
fi
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "engine.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = ServeBroker(ctx, listener, authorize, &Docker{bin: binary}) }()
	return NewBrokerClient(socket), cancel
}

func TestBrokerClientStreamsTypedEngineEvents(t *testing.T) {
	client, cancel := testBroker(t, func(net.Conn) error { return nil })
	defer cancel()
	if err := client.Available(context.Background()); err != nil {
		t.Fatal(err)
	}
	var lines []string
	if err := client.PullStream(context.Background(), "cloudless/runtime:test", func(line string) {
		lines = append(lines, line)
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lines, []string{"layer-a: Pulling fs layer", "layer-a: Pull complete"}) {
		t.Fatalf("streamed lines = %#v", lines)
	}
}

func TestBrokerNonRunRequestOmitsRunSpecForRollingCompatibility(t *testing.T) {
	payload, err := json.Marshal(brokerRequest{Action: "container.remove", Name: "cloudless-recipe-test"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"spec"`) || strings.Contains(string(payload), `"restartPolicy"`) {
		t.Fatalf("non-run request leaked RunSpec protocol fields: %s", payload)
	}
}

func TestBrokerRejectsUnauthorizedAndUnknownRequests(t *testing.T) {
	client, cancel := testBroker(t, func(net.Conn) error { return os.ErrPermission })
	defer cancel()
	if err := client.Available(context.Background()); err == nil {
		t.Fatal("unauthorized broker call succeeded")
	}

	client, cancel = testBroker(t, func(net.Conn) error { return nil })
	defer cancel()
	conn, err := net.Dial("unix", client.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"action":"available","command":"docker run --privileged"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var response brokerResponse
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == "" {
		t.Fatal("request with unknown command field was accepted")
	}
}

func TestBrokerReviewedRecipeActionRequiresConfiguredExecutor(t *testing.T) {
	client, cancel := testBroker(t, func(net.Conn) error { return nil })
	defer cancel()
	spec := ReviewedRecipeDockerSpec{
		OperationID:    "rop-00112233445566778899aabbccddeeff",
		RecipeRevision: "sha256:test",
		WorkingDir:     "/var/lib/cloudless/recipes-runtime/test",
		Args:           []string{"compose", "ps"},
	}
	if _, err := client.ReviewedRecipeDocker(context.Background(), spec); err == nil {
		t.Fatal("recipe Docker action succeeded without a configured policy executor")
	}
}

func TestBrokerReviewedRecipeActionPassesCompleteIdentity(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "engine.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	want := ReviewedRecipeDockerSpec{
		OperationID:    "rop-00112233445566778899aabbccddeeff",
		RecipeRevision: "sha256:test",
		WorkingDir:     "/var/lib/cloudless/recipes-runtime/test",
		Args:           []string{"compose", "ps"},
	}
	executor := func(_ context.Context, got ReviewedRecipeDockerSpec) (string, error) {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reviewed recipe request = %#v, want %#v", got, want)
		}
		return "ok", nil
	}
	go func() {
		_ = ServeBrokerWithRecipeExecutor(
			ctx, listener, func(net.Conn) error { return nil }, &Docker{bin: binary}, executor,
		)
	}()
	client := NewBrokerClient(socket)
	output, err := client.ReviewedRecipeDocker(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	if output != "ok" {
		t.Fatalf("reviewed recipe output = %q", output)
	}
}
