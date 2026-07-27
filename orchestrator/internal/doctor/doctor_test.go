package doctor

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestRedactRemovesCredentialsAndIdentity(t *testing.T) {
	input := `Authorization: Bearer hf_abcdef password=hunter2 api_key=topsecret "hash": "deadbeef" sk-cloudless-abc123`
	got := Redact(input)
	for _, secret := range []string{"hf_abcdef", "hunter2", "topsecret", "deadbeef", "sk-cloudless-abc123"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q survived redaction: %s", secret, got)
		}
	}
}

func TestSupportBundleRedactsContainerLogs(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng := bundleEngine{logs: "token=hf_private password=secret"}
	bundle, err := BuildBundle(context.Background(), eng, store, Report{Platform: "test", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		reader, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, _ := io.ReadAll(reader)
		reader.Close()
		if strings.Contains(string(data), "hf_private") || strings.Contains(string(data), "password=secret") {
			t.Fatalf("bundle entry %s leaked a secret: %s", file.Name, data)
		}
		if strings.HasSuffix(file.Name, ".json") && !json.Valid(data) {
			t.Fatalf("redaction corrupted JSON entry %s: %s", file.Name, data)
		}
	}
}

func TestDoctorRespectsIntentionalModelUnload(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEngineUnloaded(true); err != nil {
		t.Fatal(err)
	}
	eng := bundleEngine{containers: []engine.Container{
		{Name: "cloudless-open-webui", State: "running"},
		{Name: "cloudless-hermes", State: "running"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	report := Run(ctx, eng, store)
	for _, check := range report.Checks {
		if check.ID == "app-vllm" && check.Status == "fail" {
			t.Fatal("intentionally unloaded engine was reported as failed")
		}
	}
}

type bundleEngine struct {
	logs       string
	containers []engine.Container
}

func (bundleEngine) Available(context.Context) error                           { return nil }
func (bundleEngine) EnsureNetwork(context.Context, string) error               { return nil }
func (bundleEngine) ConnectNetwork(context.Context, string, string) error      { return nil }
func (bundleEngine) HasAlias(context.Context, string, string) (bool, error)    { return true, nil }
func (bundleEngine) Exec(context.Context, string, ...string) error             { return nil }
func (bundleEngine) Pull(context.Context, string) error                        { return nil }
func (bundleEngine) PullStream(context.Context, string, func(string)) error    { return nil }
func (bundleEngine) Build(context.Context, string, string, func(string)) error { return nil }
func (bundleEngine) RemoveImage(context.Context, string) error                 { return nil }
func (bundleEngine) Run(context.Context, engine.RunSpec) (string, error)       { return "", nil }
func (bundleEngine) Stop(context.Context, string) error                        { return nil }
func (bundleEngine) Remove(context.Context, string) error                      { return nil }

func (e bundleEngine) List(context.Context) ([]engine.Container, error) {
	if e.containers != nil {
		return e.containers, nil
	}
	return []engine.Container{{Name: "cloudless-hermes", State: "running"}}, nil
}
func (bundleEngine) Find(context.Context, string) (*engine.Container, error)      { return nil, nil }
func (e bundleEngine) Logs(context.Context, string) (string, error)               { return e.logs, nil }
func (bundleEngine) ImageDigest(context.Context, string) (string, error)          { return "", nil }
func (bundleEngine) RemoteDigest(context.Context, string) (string, error)         { return "", nil }
func (bundleEngine) ContainerImageDigest(context.Context, string) (string, error) { return "", nil }
func (bundleEngine) Output(context.Context, ...string) (string, error)            { return "", nil }
