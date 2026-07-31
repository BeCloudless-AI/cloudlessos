package catalog

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const ManifestSchema = "cloudless.apps.v2"

//go:embed cloudless-apps-v2.json
var embeddedManifest []byte

type ManifestDocument struct {
	Schema      string `json:"schema"`
	Version     int    `json:"version"`
	Description string `json:"description,omitempty"`
	Apps        []App  `json:"apps"`
	Packs       []Pack `json:"packs,omitempty"`
}

// Pack is a curated, transactional installation experience composed from App
// Manifest v2 components. Apps remain independently reusable dependencies.
type Pack struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Tagline     string   `json:"tagline,omitempty"`
	Long        string   `json:"long,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	Apps        []string `json:"apps"`
	LaunchApp   string   `json:"launchApp,omitempty"`
}

var appIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func mustLoadManifest() ManifestDocument {
	doc, err := ParseManifest(embeddedManifest)
	if err != nil {
		panic("invalid embedded Cloudless app manifest: " + err.Error())
	}
	return doc
}

// ParseManifest validates a declarative catalog before any recipe reaches the
// container engine. Production loads the copy embedded in the signed
// cloudless-orchestrator package; arbitrary remote manifests are never trusted.
func ParseManifest(raw []byte) (ManifestDocument, error) {
	var doc ManifestDocument
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return doc, fmt.Errorf("decode: %w", err)
	}
	if doc.Schema != ManifestSchema || doc.Version != 2 {
		return doc, fmt.Errorf("unsupported schema %q version %d", doc.Schema, doc.Version)
	}
	if len(doc.Apps) == 0 {
		return doc, errors.New("apps must not be empty")
	}
	byID := make(map[string]App, len(doc.Apps))
	for i := range doc.Apps {
		a := &doc.Apps[i]
		if a.SupportLevel == "" {
			if a.Verified {
				a.SupportLevel = "supported"
			} else {
				a.SupportLevel = "preview"
			}
		}
		if err := validateApp(*a); err != nil {
			return doc, fmt.Errorf("apps[%d]: %w", i, err)
		}
		if _, exists := byID[a.ID]; exists {
			return doc, fmt.Errorf("duplicate app id %q", a.ID)
		}
		if a.Resources.VRAMGB == 0 {
			a.Resources.VRAMGB = a.MinVRAMGB
		}
		if a.MinVRAMGB == 0 {
			a.MinVRAMGB = a.Resources.VRAMGB
		}
		if a.Health.Kind == "" {
			a.Health.Kind = "container"
		}
		if a.Health.TimeoutSeconds == 0 {
			a.Health.TimeoutSeconds = 60
		}
		byID[a.ID] = *a
	}
	for _, a := range doc.Apps {
		for _, dependency := range a.Dependencies {
			if dependency == a.ID {
				return doc, fmt.Errorf("%s depends on itself", a.ID)
			}
			if _, ok := byID[dependency]; !ok {
				return doc, fmt.Errorf("%s requires unknown dependency %s", a.ID, dependency)
			}
		}
	}
	if err := validateDependencyGraph(doc.Apps); err != nil {
		return doc, err
	}
	packIDs := map[string]bool{}
	for i, pack := range doc.Packs {
		if !appIDPattern.MatchString(pack.ID) || strings.TrimSpace(pack.Name) == "" || strings.TrimSpace(pack.Description) == "" {
			return doc, fmt.Errorf("packs[%d]: id, name, and description are required", i)
		}
		if packIDs[pack.ID] {
			return doc, fmt.Errorf("duplicate pack id %q", pack.ID)
		}
		packIDs[pack.ID] = true
		if len(pack.Apps) == 0 {
			return doc, fmt.Errorf("pack %s must contain at least one app", pack.ID)
		}
		if len(pack.Apps) < 2 {
			return doc, fmt.Errorf("pack %s must contain at least two applications; publish a single product as an app", pack.ID)
		}
		seen := map[string]bool{}
		for _, id := range pack.Apps {
			app, ok := byID[id]
			if !ok {
				return doc, fmt.Errorf("pack %s contains unknown app %s", pack.ID, id)
			}
			if !app.Hidden {
				return doc, fmt.Errorf("pack %s component %s must be hidden from the standalone launcher", pack.ID, id)
			}
			if seen[id] {
				return doc, fmt.Errorf("pack %s repeats app %s", pack.ID, id)
			}
			seen[id] = true
		}
		if pack.LaunchApp != "" && !seen[pack.LaunchApp] {
			return doc, fmt.Errorf("pack %s launchApp must be one of its apps", pack.ID)
		}
	}
	return doc, nil
}

func validateApp(a App) error {
	if !appIDPattern.MatchString(a.ID) {
		return fmt.Errorf("invalid id %q", a.ID)
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("name is required")
	}
	if !slices.Contains([]string{"experimental", "preview", "supported"}, a.SupportLevel) {
		return fmt.Errorf("unsupported support level %q", a.SupportLevel)
	}
	if !a.Service && !a.Hidden {
		if strings.TrimSpace(a.Category) == "" || strings.TrimSpace(a.Tagline) == "" ||
			strings.TrimSpace(a.Long) == "" || len(a.Examples) < 3 {
			return errors.New("visible applications require category, tagline, long description, and at least three examples")
		}
	}
	if strings.TrimSpace(a.Image) == "" && len(a.ArchImages) == 0 && strings.TrimSpace(a.Build) == "" {
		return errors.New("image, archImages, or build is required")
	}
	for host, container := range a.Ports {
		if host < 1 || host > 65535 || container < 1 || container > 65535 {
			return fmt.Errorf("invalid port mapping %d:%d", host, container)
		}
	}
	if a.EmbeddedPath != "" {
		expected := "/apps/" + a.ID + "/"
		if a.EmbeddedPath != expected {
			return fmt.Errorf("embeddedPath must be %q", expected)
		}
		if len(a.Ports) != 1 {
			return errors.New("embedded apps must expose exactly one port")
		}
	}
	if !a.Service && !a.Hidden {
		if len(a.Ports) == 0 || !strings.HasPrefix(a.OpenPath, "/") {
			return errors.New("visible applications require a launch port and absolute openPath")
		}
		if len(a.Volumes) == 0 && a.DataPath == "" && len(a.Config) == 0 {
			return errors.New("visible applications require an explicit persistence contract")
		}
	}
	if a.Resources.MemoryGB < 0 || a.Resources.DiskGB < 0 || a.Resources.VRAMGB < 0 || a.MinVRAMGB < 0 {
		return errors.New("resource values must be non-negative")
	}
	if !slices.Contains([]string{"", "container", "http", "openai"}, a.Health.Kind) {
		return fmt.Errorf("unsupported health kind %q", a.Health.Kind)
	}
	if (a.Health.Kind == "http" || a.Health.Kind == "openai") && !strings.HasPrefix(a.Health.Path, "/") {
		return errors.New("http health path must begin with /")
	}
	if a.Health.Port < 0 || a.Health.Port > 65535 {
		return errors.New("health port is invalid")
	}
	if a.Health.Port > 0 {
		if _, ok := a.Ports[a.Health.Port]; !ok {
			return errors.New("health port must be one of the app's published host ports")
		}
	}
	if a.LLM != nil && a.LLM.Consumes {
		if !slices.Contains([]string{"gateway", "direct"}, a.LLM.Route) {
			return fmt.Errorf("invalid LLM route %q", a.LLM.Route)
		}
		if !slices.Contains([]string{"none", "dynamic"}, a.LLM.Pinning) {
			return fmt.Errorf("invalid LLM pinning %q", a.LLM.Pinning)
		}
	}
	if !slices.Contains([]string{"", "none", "opt-in"}, a.Exposure.LAN) ||
		!slices.Contains([]string{"", "none", "opt-in"}, a.Exposure.Public) {
		return errors.New("exposure must be none or opt-in")
	}
	if a.Exposure.Public == "opt-in" && !a.Exposure.RequireAuth {
		return errors.New("public exposure requires authentication")
	}
	if a.Admin != nil && a.Admin.Pass != "" {
		return errors.New("published applications must not embed a fixed administrator password")
	}
	for key, value := range a.Env {
		normalizedKey := strings.ToUpper(strings.TrimSpace(key))
		credentialShaped := strings.Contains(normalizedKey, "API_KEY") ||
			strings.HasSuffix(normalizedKey, "_PASSWORD") ||
			strings.HasSuffix(normalizedKey, "_SECRET")
		if credentialShaped && strings.TrimSpace(value) != "" &&
			!strings.HasPrefix(value, "cloudless-managed://") {
			return fmt.Errorf("%s must be blank or use a cloudless-managed credential", key)
		}
		if strings.HasPrefix(value, "cloudless-managed://") {
			name := strings.TrimSpace(strings.TrimPrefix(value, "cloudless-managed://"))
			if !appIDPattern.MatchString(name) {
				return fmt.Errorf("%s has invalid managed credential name %q", key, name)
			}
		}
	}
	if hasPasswordlessNotebookCommand(a.Command) {
		if !a.LocalOnly || a.Exposure.LAN != "none" || a.Exposure.Public != "none" ||
			a.Exposure.Risk != "code-execution-ui" {
			return errors.New("passwordless notebook runtimes must be local-only, non-shareable code-execution surfaces")
		}
	}
	return nil
}

func hasPasswordlessNotebookCommand(command []string) bool {
	blankToken, blankPassword := false, false
	for _, arg := range command {
		switch strings.TrimSpace(arg) {
		case "--ServerApp.token=":
			blankToken = true
		case "--ServerApp.password=":
			blankPassword = true
		}
	}
	return blankToken || blankPassword
}

func validateDependencyGraph(all []App) error {
	byID := make(map[string]App, len(all))
	for _, a := range all {
		byID[a.ID] = a
	}
	visiting := map[string]bool{}
	done := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("dependency cycle at %s", id)
		}
		if done[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range byID[id].Dependencies {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		done[id] = true
		return nil
	}
	for id := range byID {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// Dependencies returns the supported dependency closure in start order. The
// requested app itself is not included.
func Dependencies(id string) ([]App, error) {
	root, ok := Get(id)
	if !ok {
		return nil, fmt.Errorf("unknown or unsupported app %q", id)
	}
	seen := map[string]bool{}
	var out []App
	var visit func(App) error
	visit = func(a App) error {
		for _, depID := range a.Dependencies {
			if seen[depID] {
				continue
			}
			dep, ok := Get(depID)
			if !ok {
				return fmt.Errorf("%s requires %s, which is unavailable on this system", a.ID, depID)
			}
			if err := visit(dep); err != nil {
				return err
			}
			seen[depID] = true
			out = append(out, dep)
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	return out, nil
}

// Packs returns packs whose complete component set is supported by this host.
func Packs() []Pack {
	out := make([]Pack, 0, len(manifestDocument.Packs))
	for _, pack := range manifestDocument.Packs {
		supported := true
		for _, id := range pack.Apps {
			if _, ok := Get(id); !ok {
				supported = false
				break
			}
		}
		if supported {
			copy := pack
			copy.Apps = append([]string(nil), pack.Apps...)
			out = append(out, copy)
		}
	}
	return out
}

func GetPack(id string) (Pack, bool) {
	for _, pack := range Packs() {
		if pack.ID == id {
			return pack, true
		}
	}
	return Pack{}, false
}

// PackInstallOrder returns dependencies and pack components once each in
// deterministic startup order.
func PackInstallOrder(id string) ([]App, error) {
	pack, ok := GetPack(id)
	if !ok {
		return nil, fmt.Errorf("unknown or unsupported pack %q", id)
	}
	seen := map[string]bool{}
	var result []App
	for _, appID := range pack.Apps {
		dependencies, err := Dependencies(appID)
		if err != nil {
			return nil, err
		}
		for _, app := range append(dependencies, mustGet(appID)) {
			if !seen[app.ID] {
				seen[app.ID] = true
				result = append(result, app)
			}
		}
	}
	return result, nil
}

func mustGet(id string) App {
	app, ok := Get(id)
	if !ok {
		panic("validated manifest app became unavailable: " + id)
	}
	return app
}
