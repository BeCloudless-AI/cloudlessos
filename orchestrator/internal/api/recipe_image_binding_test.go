package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImmutableRecipeImageReferenceReplacesMutableDigest(t *testing.T) {
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := immutableRecipeImageReference("registry.example:5000/runtime:latest@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", digest)
	if err != nil {
		t.Fatal(err)
	}
	if got != "registry.example:5000/runtime:latest@"+digest {
		t.Fatalf("reference = %q", got)
	}
}

func TestWritePreparedRecipeImageReferenceUpdatesDiskAndCommandEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.dspark")
	if err := os.WriteFile(path, []byte("DSPARK_VLLM_IMAGE=runtime:latest\nENGINE_IMAGE=runtime:latest\nOTHER=kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"DSPARK_VLLM_IMAGE": "runtime:latest", "ENGINE_IMAGE": "runtime:latest"}
	reference := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := writePreparedRecipeImageReference(dir, environment, reference); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "runtime:latest") || !strings.Contains(string(payload), "OTHER=kept") || environment["ENGINE_IMAGE"] != reference {
		t.Fatalf("environment file=%q command environment=%#v", payload, environment)
	}
}
