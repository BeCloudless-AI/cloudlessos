package localrecipes

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// NativeManifest is Cloudless's portable, reviewable recipe format. Execution
// data stays in the same Draft structure used by the local editor, leaving a
// stable foundation for a future native community service.
type NativeManifest struct {
	Schema   string `yaml:"schema" json:"schema"`
	Metadata struct {
		Name        string   `yaml:"name" json:"name"`
		Description string   `yaml:"description" json:"description"`
		Version     string   `yaml:"version" json:"version"`
		Author      string   `yaml:"author" json:"author"`
		Category    string   `yaml:"category" json:"category"`
		Tags        []string `yaml:"tags" json:"tags"`
	} `yaml:"metadata" json:"metadata"`
	Recipe Draft `yaml:"recipe" json:"recipe"`
}

func ParseNativeManifest(data []byte) (NativeManifest, Draft, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return NativeManifest{}, Draft{}, errors.New("Cloudless recipe must be between 1 byte and 2 MB")
	}
	var manifest NativeManifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return NativeManifest{}, Draft{}, fmt.Errorf("parse Cloudless recipe: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return NativeManifest{}, Draft{}, errors.New("Cloudless recipe must contain exactly one document")
	}
	if manifest.Schema != "cloudless.recipe/v1" {
		return NativeManifest{}, Draft{}, errors.New("unsupported Cloudless recipe schema")
	}
	if manifest.Recipe.Name == "" {
		manifest.Recipe.Name = manifest.Metadata.Name
	}
	if manifest.Recipe.Description == "" {
		manifest.Recipe.Description = manifest.Metadata.Description
	}
	draft, err := validateDraft(manifest.Recipe)
	if err != nil {
		return NativeManifest{}, Draft{}, err
	}
	return manifest, draft, nil
}
