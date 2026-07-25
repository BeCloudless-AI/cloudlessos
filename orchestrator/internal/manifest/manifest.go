// Package manifest consumes the Cloudless validated-versions manifest: a JSON
// (hosted by Cloudless) that pins each app/infra image to a tested digest and the
// default model to a tested revision. When reachable, these pins override the
// catalog's moving tags so devices only run versions Cloudless has validated; when
// absent/unreachable, callers fall back to the catalog defaults. Stdlib-only.
package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/diffusion"
	"github.com/cloudless/orchestrator/internal/models"
)

// DefaultURL is the apps manifest location (override with CLOUDLESS_MANIFEST_URL).
const DefaultURL = "https://samuelcardillo.com/cloudless/cloudless-apps-manifest.json"

// DefaultModelsURL is the "Cloudless highlights" LLM manifest (override with CLOUDLESS_MODELS_URL).
const DefaultModelsURL = "https://samuelcardillo.com/cloudless/cloudless-models.json"

// DefaultDiffusionURL is the "Cloudless highlights" image-model manifest (CLOUDLESS_DIFFUSION_URL).
const DefaultDiffusionURL = "https://samuelcardillo.com/cloudless/cloudless-diffusion.json"

// Pin is a validated image pin (apps + infra).
type Pin struct {
	Image         string            `json:"image"`
	Tag           string            `json:"tag"`
	Digest        string            `json:"digest"`
	Digests       map[string]string `json:"digests,omitempty"`
	Architectures []string          `json:"architectures,omitempty"`
	Verified      bool              `json:"verified"`
	Notes         string            `json:"notes"`
	Rollback      string            `json:"rollback"`
}

// Ref returns the pullable "image@digest" reference, or "" if incomplete.
func (p Pin) Ref() string {
	return p.RefFor(runtime.GOARCH)
}

// CurrentDigest returns the digest selected for the running architecture.
func (p Pin) CurrentDigest() string {
	return p.DigestFor(runtime.GOARCH)
}

// DigestFor returns only a digest explicitly valid for arch.
func (p Pin) DigestFor(arch string) string {
	digest := p.Digests[arch]
	if digest != "" || p.Digest == "" {
		return digest
	}
	if arch == "amd64" && len(p.Architectures) == 0 {
		return p.Digest
	}
	for _, supported := range p.Architectures {
		if supported == arch {
			return p.Digest
		}
	}
	return ""
}

// RefFor returns only a digest explicitly valid for arch. Legacy manifests
// predate ARM64 support, so their single digest remains valid for AMD64 only.
// A shared multi-platform index digest can opt in through Architectures.
func (p Pin) RefFor(arch string) string {
	if p.Image == "" {
		return ""
	}
	digest := p.DigestFor(arch)
	if digest == "" {
		return ""
	}
	return p.Image + "@" + digest
}

// Model is a validated Hugging Face model pin.
type Model struct {
	Repo     string `json:"repo"`
	Revision string `json:"revision"`
	Verified bool   `json:"verified"`
	Notes    string `json:"notes"`
}

// Doc is the parsed manifest.
type Doc struct {
	ManifestVersion int              `json:"manifestVersion"`
	Channel         string           `json:"channel"`
	Updated         string           `json:"updated"`
	Apps            map[string]Pin   `json:"apps"`
	Infra           map[string]Pin   `json:"infra"`
	Models          map[string]Model `json:"models"`
}

// Store fetches and caches the manifest.
type Store struct {
	url string
	ttl time.Duration
	mu  sync.Mutex
	doc *Doc
	at  time.Time
}

// New returns a Store for the given URL ("" disables it → always falls back).
func New(url string) *Store {
	return &Store{url: url, ttl: 10 * time.Minute}
}

// Enabled reports whether a manifest URL is configured.
func (s *Store) Enabled() bool { return s != nil && s.url != "" }

// Get returns the manifest, cached for the TTL. On a fetch error it serves the
// last good copy if available, else returns the error.
func (s *Store) Get(ctx context.Context) (*Doc, error) {
	if !s.Enabled() {
		return nil, nil
	}
	s.mu.Lock()
	if s.doc != nil && time.Since(s.at) < s.ttl {
		d := s.doc
		s.mu.Unlock()
		return d, nil
	}
	s.mu.Unlock()

	d, err := s.fetch(ctx)
	if err != nil {
		s.mu.Lock()
		old := s.doc
		s.mu.Unlock()
		if old != nil {
			return old, nil // stale-but-usable beats nothing
		}
		return nil, err
	}
	s.mu.Lock()
	s.doc, s.at = d, time.Now()
	s.mu.Unlock()
	return d, nil
}

func (s *Store) fetch(ctx context.Context) (*Doc, error) {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest %s: HTTP %d", s.url, resp.StatusCode)
	}
	var d Doc
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("manifest decode: %w", err)
	}
	return &d, nil
}

// Pin returns the validated pin for an id (checking apps then infra), ok only if
// it carries a digest. Errors/absence → ok=false (caller uses the catalog default).
func (s *Store) Pin(ctx context.Context, id string) (Pin, bool) {
	d, err := s.Get(ctx)
	if err != nil || d == nil {
		return Pin{}, false
	}
	if p, ok := d.Apps[id]; ok && p.Ref() != "" {
		return p, true
	}
	if p, ok := d.Infra[id]; ok && p.Ref() != "" {
		return p, true
	}
	return Pin{}, false
}

// PinFor returns a pin only when it targets the same image repository as the
// built-in catalog entry. A hosted manifest can lag behind a catalog migration;
// accepting a pin for the old repository would run an incompatible image with
// the new command, environment and mounts. In that case callers safely fall
// back to the catalog image until the hosted manifest catches up.
func (s *Store) PinFor(ctx context.Context, id, expectedImage string) (Pin, bool) {
	p, ok := s.Pin(ctx, id)
	if !ok || imageRepository(p.Image) != imageRepository(expectedImage) {
		return Pin{}, false
	}
	return p, true
}

func imageRepository(ref string) string {
	ref = strings.TrimSpace(strings.SplitN(ref, "@", 2)[0])
	lastSlash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > lastSlash {
		ref = ref[:colon]
	}
	return strings.ToLower(ref)
}

// ModelPin returns the validated model pin by key (e.g. "default").
func (s *Store) ModelPin(ctx context.Context, key string) (Model, bool) {
	d, err := s.Get(ctx)
	if err != nil || d == nil {
		return Model{}, false
	}
	m, ok := d.Models[key]
	return m, ok && m.Revision != ""
}

// Channel returns the manifest channel ("" if unavailable).
func (s *Store) Channel(ctx context.Context) string {
	d, err := s.Get(ctx)
	if err != nil || d == nil {
		return ""
	}
	return d.Channel
}

// ModelsDoc is the "Cloudless highlights" model manifest.
type ModelsDoc struct {
	ManifestVersion int            `json:"manifestVersion"`
	Updated         string         `json:"updated"`
	Highlights      []models.Model `json:"highlights"`
}

// ModelsStore fetches and caches the hosted highlights manifest.
type ModelsStore struct {
	url string
	ttl time.Duration
	mu  sync.Mutex
	doc *ModelsDoc
	at  time.Time
}

// NewModels returns a ModelsStore ("" disables it → callers fall back to the built-in list).
func NewModels(url string) *ModelsStore { return &ModelsStore{url: url, ttl: 10 * time.Minute} }

// Enabled reports whether a models manifest URL is configured.
func (s *ModelsStore) Enabled() bool { return s != nil && s.url != "" }

// Highlights returns the hosted curated models, or nil if unavailable (use the fallback).
func (s *ModelsStore) Highlights(ctx context.Context) []models.Model {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	if s.doc != nil && time.Since(s.at) < s.ttl {
		d := s.doc
		s.mu.Unlock()
		return d.Highlights
	}
	s.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, s.url, nil)
	if err == nil {
		if resp, derr := http.DefaultClient.Do(req); derr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var d ModelsDoc
				if json.NewDecoder(resp.Body).Decode(&d) == nil && len(d.Highlights) > 0 {
					s.mu.Lock()
					s.doc, s.at = &d, time.Now()
					s.mu.Unlock()
					return d.Highlights
				}
			}
		}
	}
	s.mu.Lock()
	old := s.doc
	s.mu.Unlock()
	if old != nil {
		return old.Highlights // stale-but-usable
	}
	return nil
}

// DiffusionDoc is the "Cloudless highlights" image-model manifest.
type DiffusionDoc struct {
	ManifestVersion int               `json:"manifestVersion"`
	Updated         string            `json:"updated"`
	Highlights      []diffusion.Model `json:"highlights"`
}

// DiffusionStore fetches and caches the hosted image-model highlights manifest.
type DiffusionStore struct {
	url string
	ttl time.Duration
	mu  sync.Mutex
	doc *DiffusionDoc
	at  time.Time
}

// NewDiffusion returns a DiffusionStore ("" disables it → callers use the built-in list).
func NewDiffusion(url string) *DiffusionStore {
	return &DiffusionStore{url: url, ttl: 10 * time.Minute}
}

// Enabled reports whether a diffusion manifest URL is configured.
func (s *DiffusionStore) Enabled() bool { return s != nil && s.url != "" }

// Highlights returns the hosted curated image models, or nil if unavailable.
func (s *DiffusionStore) Highlights(ctx context.Context) []diffusion.Model {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	if s.doc != nil && time.Since(s.at) < s.ttl {
		d := s.doc
		s.mu.Unlock()
		return d.Highlights
	}
	s.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, s.url, nil)
	if err == nil {
		if resp, derr := http.DefaultClient.Do(req); derr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var d DiffusionDoc
				if json.NewDecoder(resp.Body).Decode(&d) == nil && len(d.Highlights) > 0 {
					s.mu.Lock()
					s.doc, s.at = &d, time.Now()
					s.mu.Unlock()
					return d.Highlights
				}
			}
		}
	}
	s.mu.Lock()
	old := s.doc
	s.mu.Unlock()
	if old != nil {
		return old.Highlights
	}
	return nil
}
