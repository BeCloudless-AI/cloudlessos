package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

func TestValidateRenderedRecipeContractRequiresManagedPortAliasAndBind(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "start.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec vllm serve --host ${VLLM_HOST:-0.0.0.0} --port ${ENGINE_PORT} --served-model-name ${SERVED_MODEL_NAME}\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		Engine:  localrecipes.Engine{ServedModelName: localrecipes.CloudlessModelAlias, ContainerPort: 8890, APIPath: "/v1"},
		Health:  localrecipes.Health{Port: 8890, Path: "/health"},
		Runtime: localrecipes.Runtime{Lifecycle: localrecipes.Lifecycle{Start: localrecipes.Command{Program: "bash", Args: []string{"./start.sh"}}}},
	}
	if _, err := validateRenderedRecipeContract(recipe, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec vllm serve --host 127.0.0.1 --port 9999 --served-model-name other\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := validateRenderedRecipeContract(recipe, dir); err == nil {
		t.Fatal("unmanaged runtime contract was accepted")
	}
}

func TestValidateRenderedRecipeContractRejectsNonV1API(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "start.sh"), []byte("#!/bin/sh\nexec vllm serve --host 0.0.0.0 --port ${ENGINE_PORT} --served-model-name cloudless\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	recipe := localrecipes.Recipe{
		Engine:  localrecipes.Engine{ServedModelName: localrecipes.CloudlessModelAlias, ContainerPort: 8890, APIPath: "/custom"},
		Health:  localrecipes.Health{Port: 8890, Path: "/health"},
		Runtime: localrecipes.Runtime{Lifecycle: localrecipes.Lifecycle{Start: localrecipes.Command{Program: "bash", Args: []string{"./start.sh"}}}},
	}
	if _, err := validateRenderedRecipeContract(recipe, dir); err == nil {
		t.Fatal("non-/v1 API contract was accepted")
	}
}
