package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudless/orchestrator/internal/recipeops"
)

func TestRecipeRuntimeTreeDigestTracksExecutableContentButNotPrivateSSHHome(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "start.sh"), []byte("#!/bin/sh\necho first\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cloudless-home", ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cloudless-home", ".ssh", "config"), []byte("private host alias"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"PATH": os.Getenv("PATH"), "HOME": filepath.Join(dir, ".cloudless-home")}
	first, err := recipeRuntimeTreeDigest(context.Background(), dir, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cloudless-home", ".ssh", "config"), []byte("changed private alias"), 0o600); err != nil {
		t.Fatal(err)
	}
	privateChange, err := recipeRuntimeTreeDigest(context.Background(), dir, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	if privateChange != first {
		t.Fatalf("private SSH home changed runtime digest: %s != %s", privateChange, first)
	}
	if err := os.WriteFile(filepath.Join(dir, "start.sh"), []byte("#!/bin/sh\necho second\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeChange, err := recipeRuntimeTreeDigest(context.Background(), dir, environment, nil)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeChange == first {
		t.Fatal("changed executable content retained the old runtime digest")
	}
}

func TestRecipeNodeAttestationBindsEveryRuntimeIdentity(t *testing.T) {
	operation := recipeops.Operation{ID: "operation", RecipeRevision: "sha256:recipe", ResolvedSourceRevision: "source-commit"}
	left := newRecipeNodeAttestation("Spark A", operation, "sha256:image", "sha256:model", "sha256:runtime")
	right := newRecipeNodeAttestation("Spark B", operation, "sha256:image", "sha256:model", "sha256:runtime")
	if left.Combined != right.Combined {
		t.Fatalf("identical node inputs produced different attestations: %#v %#v", left, right)
	}
	changed := newRecipeNodeAttestation("Spark B", operation, "sha256:image", "sha256:model-two", "sha256:runtime")
	if changed.Combined == left.Combined {
		t.Fatal("changed model identity retained the old combined attestation")
	}
	check := recipeNodeAttestationCheck([]recipeNodeAttestation{left, right})
	if check.Status != recipeops.CheckPass || check.Values["node.1.runtime"] != "sha256:runtime" {
		t.Fatalf("node consistency check = %#v", check)
	}
}
