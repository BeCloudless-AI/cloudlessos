package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/communityrecipes"
	"gopkg.in/yaml.v3"
)

const defaultAPI = "https://becloudless.ai/api/v1/recipes"

type client struct {
	base string
	key  string
	http *http.Client
}

func usage() {
	fmt.Fprintln(os.Stderr, `Cloudless community recipe CLI

Usage:
  cloudless recipes validate <manifest.yaml>
  cloudless recipes publish <manifest.yaml> [--api-key KEY] [--api URL]
  cloudless recipes status <submission-id> [--api-key KEY] [--api URL]

CLOUDLESS_API_KEY and CLOUDLESS_COMMUNITY_API can provide the two flags.`)
}

func parseOptions(arguments []string) (positional []string, api, key string, err error) {
	api = strings.TrimRight(os.Getenv("CLOUDLESS_COMMUNITY_API"), "/")
	if api == "" {
		api = defaultAPI
	}
	key = os.Getenv("CLOUDLESS_API_KEY")
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--api", "--api-key":
			if index+1 >= len(arguments) {
				return nil, "", "", fmt.Errorf("%s requires a value", arguments[index])
			}
			if arguments[index] == "--api" {
				api = strings.TrimRight(arguments[index+1], "/")
			} else {
				key = arguments[index+1]
			}
			index++
		default:
			if strings.HasPrefix(arguments[index], "--") {
				return nil, "", "", fmt.Errorf("unknown option %s", arguments[index])
			}
			positional = append(positional, arguments[index])
		}
	}
	return positional, api, key, nil
}

func readManifest(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > communityrecipes.MaxReleaseBytes {
		return nil, errors.New("manifest must be between 1 byte and 2 MiB")
	}
	var manifest map[string]any
	if strings.EqualFold(filepath.Ext(path), ".json") {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		err = decoder.Decode(&manifest)
	} else {
		err = yaml.Unmarshal(raw, &manifest)
	}
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := communityrecipes.ValidateManagedManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func randomID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("retry-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value)
}

func (c *client) request(ctx context.Context, method, path string, body any, output any, idempotent bool) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.key != "" {
		request.Header.Set("Authorization", "Bearer "+c.key)
	}
	if idempotent {
		request.Header.Set("Idempotency-Key", randomID())
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 3<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var payload struct{ Error, Code string }
		_ = json.Unmarshal(raw, &payload)
		if payload.Error == "" {
			payload.Error = strings.TrimSpace(string(raw))
		}
		return response.StatusCode, fmt.Errorf("community API: %s", payload.Error)
	}
	if output != nil && len(raw) != 0 {
		if err := json.Unmarshal(raw, output); err != nil {
			return response.StatusCode, fmt.Errorf("decode community response: %w", err)
		}
	}
	return response.StatusCode, nil
}

func object(value any, name string) (map[string]any, error) {
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("manifest %s must be an object", name)
	}
	return result, nil
}

func stringValue(parent map[string]any, key string) string {
	value, _ := parent[key].(string)
	return value
}

func publish(ctx context.Context, c *client, path string) error {
	if !strings.HasPrefix(c.key, "cld_alpha_") {
		return errors.New("set --api-key or CLOUDLESS_API_KEY to a cld_alpha_ publisher key")
	}
	manifest, err := readManifest(path)
	if err != nil {
		return err
	}
	metadata, _ := object(manifest["metadata"], "metadata")
	recipeData, _ := object(manifest["recipe"], "recipe")
	title := stringValue(metadata, "name")
	slug := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, title), "-")
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	summary := stringValue(metadata, "description")
	category := stringValue(metadata, "category")
	tags, _ := metadata["tags"].([]any)
	cleanTags := make([]string, 0, len(tags))
	for _, value := range tags {
		if tag, ok := value.(string); ok {
			cleanTags = append(cleanTags, tag)
		}
	}
	createBody := map[string]any{"slug": slug, "title": title, "summary": summary, "description": stringValue(recipeData, "description"), "category": category, "tags": cleanTags}
	var created struct {
		Recipe struct{ ID, Slug string } `json:"recipe"`
	}
	status, err := c.request(ctx, http.MethodPost, "", createBody, &created, true)
	if err != nil && status != http.StatusConflict {
		return err
	}
	if status == http.StatusConflict {
		var mine struct {
			Recipes []struct{ ID, Slug string } `json:"recipes"`
		}
		if _, err := c.request(ctx, http.MethodGet, "/me/recipes", nil, &mine, false); err != nil {
			return err
		}
		for _, recipe := range mine.Recipes {
			if recipe.Slug == slug {
				created.Recipe = recipe
				break
			}
		}
		if created.Recipe.ID == "" {
			return errors.New("recipe slug already exists but is not owned by this account")
		}
	}
	version := stringValue(metadata, "version")
	var revision struct {
		Revision struct{ ID string } `json:"revision"`
	}
	if _, err := c.request(ctx, http.MethodPost, "/"+created.Recipe.ID+"/revisions", map[string]any{"version": version, "changelog": "Published with the Cloudless CLI", "manifest": manifest}, &revision, true); err != nil {
		return err
	}
	var submitted struct {
		Revision struct{ ID, Status string } `json:"revision"`
	}
	if _, err := c.request(ctx, http.MethodPost, "/"+created.Recipe.ID+"/revisions/"+revision.Revision.ID+"/submit", map[string]any{}, &submitted, true); err != nil {
		return err
	}
	fmt.Printf("Submitted %s %s for validation\nsubmission=%s\nstatus=%s\n", slug, version, submitted.Revision.ID, submitted.Revision.Status)
	return nil
}

func run() error {
	if len(os.Args) < 3 || os.Args[1] != "recipes" {
		usage()
		return errors.New("expected a recipes command")
	}
	positional, api, key, err := parseOptions(os.Args[3:])
	if err != nil {
		return err
	}
	command := os.Args[2]
	if len(positional) != 1 {
		return fmt.Errorf("%s requires exactly one file or submission ID", command)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c := &client{base: api, key: key, http: &http.Client{Timeout: 40 * time.Second}}
	switch command {
	case "validate":
		manifest, err := readManifest(positional[0])
		if err != nil {
			return err
		}
		digest, _, err := communityrecipes.Digest(manifest)
		if err != nil {
			return err
		}
		fmt.Printf("Container recipe is structurally valid for submission.\nmanifestDigest=%s\n", digest)
		return nil
	case "publish":
		return publish(ctx, c, positional[0])
	case "status":
		if !strings.HasPrefix(c.key, "cld_alpha_") {
			return errors.New("status requires a Cloudless API key")
		}
		var result any
		if _, err := c.request(ctx, http.MethodGet, "/submissions/"+positional[0], nil, &result, false); err != nil {
			return err
		}
		encoded, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(encoded))
		return nil
	default:
		usage()
		return fmt.Errorf("unknown recipes command %q", command)
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
