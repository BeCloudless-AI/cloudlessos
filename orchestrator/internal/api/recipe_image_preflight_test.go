package api

import (
	"errors"
	"strings"
	"testing"
)

func TestSelectRecipeImageManifestUsesRequestedArchitecture(t *testing.T) {
	manifest, err := parseRecipeRegistryManifest(`{
		"digest":"sha256:` + strings.Repeat("1", 64) + `",
		"manifests":[
			{"digest":"sha256:` + strings.Repeat("a", 64) + `","platform":{"os":"linux","architecture":"amd64"}},
			{"digest":"sha256:` + strings.Repeat("b", 64) + `","platform":{"os":"unknown","architecture":"unknown"}},
			{"digest":"sha256:` + strings.Repeat("c", 64) + `","platform":{"os":"linux","architecture":"arm64"}}
		]}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := selectRecipeImageManifest(manifest, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	want := "sha256:" + strings.Repeat("c", 64)
	if got != want {
		t.Fatalf("selected digest = %q, want %q", got, want)
	}
	if _, err := selectRecipeImageManifest(manifest, "ppc64le"); err == nil {
		t.Fatal("missing architecture was accepted")
	}
}

func TestSelectRecipeSingleManifestRequiresDigest(t *testing.T) {
	manifest := recipeRegistryManifest{Digest: "sha256:" + strings.Repeat("d", 64)}
	if got, err := selectRecipeImageManifest(manifest, "arm64"); err != nil || got != manifest.Digest {
		t.Fatalf("single manifest = %q, %v", got, err)
	}
	manifest.Digest = "latest"
	if _, err := selectRecipeImageManifest(manifest, "arm64"); err == nil {
		t.Fatal("mutable single manifest result was accepted")
	}
}

func TestRecipeImageRepositoryPreservesRegistryPort(t *testing.T) {
	for input, want := range map[string]string{
		"registry.example:5000/team/image:latest":              "registry.example:5000/team/image",
		"ghcr.io/team/image@sha256:" + strings.Repeat("a", 64): "ghcr.io/team/image",
		"ubuntu": "ubuntu",
	} {
		if got := recipeImageRepository(input); got != want {
			t.Errorf("repository(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRecipeManifestCompressedBytes(t *testing.T) {
	manifest := recipeRegistryManifest{
		Config: recipeImageDescriptor{Size: 100},
		Layers: []recipeImageDescriptor{{Size: 200}, {Size: 300}, {Size: -1}},
	}
	if got := recipeManifestCompressedBytes(manifest); got != 600 {
		t.Fatalf("compressed bytes = %d", got)
	}
}

func TestReusablePreparedRecipeImageRequiresExactVerifiedDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	image := recipeImagePreflight{Digest: digest, OS: "linux", Architecture: "arm64"}
	if !reusablePreparedRecipeImage(digest, image, nil) {
		t.Fatal("exact prepared image was not reused")
	}
	if reusablePreparedRecipeImage("sha256:"+strings.Repeat("b", 64), image, nil) {
		t.Fatal("different prepared image was reused")
	}
	if reusablePreparedRecipeImage(digest, image, errors.New("inspect failed")) {
		t.Fatal("uninspectable image was reused")
	}
	if reusablePreparedRecipeImage("latest", image, nil) {
		t.Fatal("mutable image identity was reused")
	}
}
