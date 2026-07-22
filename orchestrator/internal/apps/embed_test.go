package apps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResetHermesModelPreservesUnrelatedConfiguration(t *testing.T) {
	dir := t.TempDir()
	config := `model:
  provider: ollama
  model: "some-other-model"
  base_url: "http://elsewhere:11434/v1"
  api_key: "other"

terminal:
  backend: ssh

custom_user_setting:
  keep_me: true
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ResetHermesModel(dir); err != nil {
		t.Fatal(err)
	}
	gotBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"provider: custom",
		`model: "cloudless"`,
		`base_url: "http://127.0.0.1:8000/v1"`,
		"backend: ssh",
		"keep_me: true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("reset config missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "some-other-model") || strings.Contains(got, "http://elsewhere") {
		t.Fatalf("old Hermes model connection survived reset:\n%s", got)
	}
}
