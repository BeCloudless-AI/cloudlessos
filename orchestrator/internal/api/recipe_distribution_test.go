package api

import (
	"errors"
	"strings"
	"testing"
)

func TestReconcileLoadedRecipeImageRestoresMissingTransferReference(t *testing.T) {
	const expected = "sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"
	const reference = "cloudless/recipe-preflight:3430d6614a8e2925"
	images := map[string]string{expected: expected}

	err := reconcileLoadedRecipeImage(expected, reference,
		func(image string) string { return images[image] },
		func(source, target string) error {
			images[target] = images[source]
			return nil
		})
	if err != nil {
		t.Fatalf("reconcile image: %v", err)
	}
	if got := images[reference]; got != expected {
		t.Fatalf("transfer reference resolved to %q, want %q", got, expected)
	}
}

func TestReconcileLoadedRecipeImageReplacesStaleTransferReference(t *testing.T) {
	const expected = "sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"
	const reference = "cloudless/recipe-preflight:3430d6614a8e2925"
	images := map[string]string{
		expected:  expected,
		reference: "sha256:older",
	}

	err := reconcileLoadedRecipeImage(expected, reference,
		func(image string) string { return images[image] },
		func(source, target string) error {
			images[target] = images[source]
			return nil
		})
	if err != nil {
		t.Fatalf("reconcile image: %v", err)
	}
	if got := images[reference]; got != expected {
		t.Fatalf("stale transfer reference was not replaced: got %q", got)
	}
}

func TestReconcileLoadedRecipeImageRejectsMissingExpectedContent(t *testing.T) {
	err := reconcileLoadedRecipeImage("sha256:expected", "cloudless/recipe-preflight:expected",
		func(string) string { return "" },
		func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "did not load the expected runtime image content") {
		t.Fatalf("expected missing-content error, got %v", err)
	}
}

func TestReconcileLoadedRecipeImageReportsTagFailure(t *testing.T) {
	const expected = "sha256:expected"
	err := reconcileLoadedRecipeImage(expected, "cloudless/recipe-preflight:expected",
		func(image string) string {
			if image == expected {
				return expected
			}
			return ""
		},
		func(string, string) error { return errors.New("tag denied") })
	if err == nil || !strings.Contains(err.Error(), "restore verified runtime image reference: tag denied") {
		t.Fatalf("expected tag failure, got %v", err)
	}
}

func TestReconcileLoadedRecipeImageVerifiesRestoredReference(t *testing.T) {
	const expected = "sha256:expected"
	err := reconcileLoadedRecipeImage(expected, "cloudless/recipe-preflight:expected",
		func(image string) string {
			if image == expected {
				return expected
			}
			return "sha256:wrong"
		},
		func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "does not resolve to the verified content") {
		t.Fatalf("expected restored-reference verification failure, got %v", err)
	}
}

func TestRecipeImageTransferReferenceUsesImmutableContentID(t *testing.T) {
	reference, err := recipeImageTransferReference("sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8")
	if err != nil {
		t.Fatalf("transfer reference: %v", err)
	}
	if want := "cloudless/recipe-transfer:3430d6614a8e2925"; reference != want {
		t.Fatalf("transfer reference = %q, want %q", reference, want)
	}
}

func TestRecipeImageTransferReferenceRejectsRegistryDigest(t *testing.T) {
	if _, err := recipeImageTransferReference("ghcr.io/example/runtime@sha256:3430d6614a8e2925f34d059af6caf05aff42387326db4d05639a60f10f2654d8"); err == nil {
		t.Fatal("expected registry digest reference to be rejected as a local content ID")
	}
}
