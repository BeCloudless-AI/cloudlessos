package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
)

const (
	maxSparkRunRecipeBytes = 2 << 20
	maxSparkRunIndexBytes  = 8 << 20
)

var (
	sparkRunArenaID = regexp.MustCompile(`^[A-Za-z0-9-]{6,128}$`)
	registryName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

type sparkRunImportRequest struct {
	SourceURL string `json:"sourceUrl"`
	YAML      string `json:"yaml"`
}

type sparkRunRegistry struct {
	Owner, Repository, Ref, Directory string
}

var sparkRunRegistries = map[string]sparkRunRegistry{
	"official":              {"spark-arena", "recipe-registry", "main", "official-recipes"},
	"experimental":          {"spark-arena", "recipe-registry", "main", "experimental-recipes"},
	"community":             {"spark-arena", "community-recipe-registry", "main", "recipes"},
	"eugr":                  {"eugr", "spark-vllm-docker", "main", "recipes"},
	"atlas":                 {"Avarok-Cybersecurity", "atlas-recipes", "main", "recipes"},
	"sparkrun-transitional": {"dbotwinick", "sparkrun-recipe-registry", "main", "transitional/recipes"},
	"sparkrun-testing":      {"dbotwinick", "sparkrun-recipe-registry", "main", "testing/recipes"},
}

func publicAddress(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func sparkRunHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{ForceAttemptHTTP2: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range addresses {
			if !publicAddress(candidate.IP) {
				continue
			}
			if connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port)); dialErr == nil {
				return connection, nil
			}
		}
		return nil, errors.New("recipe host did not resolve to a public address")
	}
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 || req.URL.Scheme != "https" {
				return errors.New("unsafe recipe redirect")
			}
			return nil
		},
	}
}

func normalizeSparkRunURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "@spark-arena/") {
		id := strings.TrimPrefix(raw, "@spark-arena/")
		if !sparkRunArenaID.MatchString(id) {
			return "", errors.New("invalid Spark Arena recipe ID")
		}
		return "https://spark-arena.com/api/recipes/" + id + "/raw", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", errors.New("enter a public HTTPS recipe URL, registry reference, or YAML document")
	}
	if strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 5 && parts[2] == "blob" {
			u.Host = "raw.githubusercontent.com"
			u.Path = "/" + strings.Join(append(parts[:2], parts[3:]...), "/")
		}
	}
	return u.String(), nil
}

func fetchSparkRunBytesLimit(ctx context.Context, client *http.Client, raw string, limit int64) ([]byte, string, error) {
	resolved, err := normalizeSparkRunURL(raw)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resolved, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/yaml, text/yaml, text/plain, application/octet-stream")
	req.Header.Set("User-Agent", "CloudlessOS-SparkRun-Compatibility/1")
	response, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download SparkRun recipe: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("download SparkRun recipe: HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limit {
		return nil, "", errors.New("SparkRun response is too large")
	}
	return data, response.Request.URL.String(), nil
}

func fetchSparkRunBytes(ctx context.Context, client *http.Client, raw string) ([]byte, string, error) {
	return fetchSparkRunBytesLimit(ctx, client, raw, maxSparkRunRecipeBytes)
}

func resolveRegistryRecipe(ctx context.Context, client *http.Client, reference string) ([]byte, string, error) {
	parts := strings.SplitN(strings.TrimPrefix(reference, "@"), "/", 2)
	if len(parts) != 2 || !registryName.MatchString(parts[1]) {
		return nil, "", errors.New("registry reference must look like @official/recipe-name")
	}
	registry, ok := sparkRunRegistries[parts[0]]
	if !ok {
		return nil, "", fmt.Errorf("registry @%s is not built into Cloudless; use a direct HTTPS YAML URL", parts[0])
	}
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", registry.Owner, registry.Repository, registry.Ref)
	treeBytes, _, err := fetchSparkRunBytesLimit(ctx, client, treeURL, maxSparkRunIndexBytes)
	if err != nil {
		return nil, "", err
	}
	var tree struct {
		Truncated bool `json:"truncated"`
		Tree      []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(treeBytes, &tree); err != nil || tree.Truncated {
		return nil, "", errors.New("recipe registry index is invalid or incomplete")
	}
	wanted := strings.TrimSuffix(strings.TrimSuffix(parts[1], ".yaml"), ".yml")
	matches := []string{}
	for _, entry := range tree.Tree {
		if entry.Type != "blob" || !strings.HasPrefix(entry.Path, registry.Directory+"/") {
			continue
		}
		ext := strings.ToLower(path.Ext(entry.Path))
		if (ext == ".yaml" || ext == ".yml") && strings.EqualFold(strings.TrimSuffix(path.Base(entry.Path), ext), wanted) {
			matches = append(matches, entry.Path)
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return nil, "", fmt.Errorf("recipe %q was not found in @%s", wanted, parts[0])
	}
	if len(matches) > 1 {
		return nil, "", fmt.Errorf("recipe %q is ambiguous in @%s; use a direct URL", wanted, parts[0])
	}
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", registry.Owner, registry.Repository, registry.Ref, matches[0])
	return fetchSparkRunBytes(ctx, client, rawURL)
}

func resolveSparkRunRecipe(ctx context.Context, request sparkRunImportRequest) ([]byte, string, error) {
	if strings.TrimSpace(request.YAML) != "" {
		if len(request.YAML) > maxSparkRunRecipeBytes {
			return nil, "", errors.New("SparkRun recipe is larger than 2 MB")
		}
		source := strings.TrimSpace(request.SourceURL)
		if source != "" {
			var err error
			source, err = normalizeSparkRunURL(source)
			if err != nil {
				return nil, "", err
			}
		}
		return []byte(request.YAML), source, nil
	}
	reference := strings.TrimSpace(request.SourceURL)
	if reference == "" {
		return nil, "", errors.New("enter a recipe URL, registry reference, or YAML document")
	}
	client := sparkRunHTTPClient()
	if strings.HasPrefix(reference, "@") && !strings.HasPrefix(reference, "@spark-arena/") {
		return resolveRegistryRecipe(ctx, client, reference)
	}
	return fetchSparkRunBytes(ctx, client, reference)
}

func decodeSparkRunImportRequest(w http.ResponseWriter, r *http.Request) (sparkRunImportRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSparkRunRecipeBytes+16<<10)
	var request sparkRunImportRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "recipe source is required"})
		return request, false
	}
	return request, true
}

func (s *Server) sparkRunRecipePreview(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeSparkRunImportRequest(w, r)
	if !ok {
		return
	}
	data, sourceURL, err := resolveSparkRunRecipe(r.Context(), request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	preview, err := localrecipes.ParseSparkRun(data, sourceURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	providerOutput, providerErr := s.validateSparkRunProvider(r.Context(), data)
	if providerErr != nil {
		preview.Compatible = false
		preview.ProviderReady = false
		preview.ProviderMessage = providerErr.Error()
		preview.Unsupported = append(preview.Unsupported, providerErr.Error())
	} else {
		preview.ProviderReady = true
		preview.ProviderMessage = strings.TrimSpace(providerOutput)
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) sparkRunRecipeImport(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeSparkRunImportRequest(w, r)
	if !ok {
		return
	}
	data, sourceURL, err := resolveSparkRunRecipe(r.Context(), request)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	preview, err := localrecipes.ParseSparkRun(data, sourceURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	providerOutput, providerErr := s.validateSparkRunProvider(r.Context(), data)
	if providerErr != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": providerErr.Error(), "unsupported": []string{providerErr.Error()}})
		return
	}
	preview.ProviderReady = true
	preview.ProviderMessage = strings.TrimSpace(providerOutput)
	if !preview.Compatible {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the pinned SparkRun provider cannot run this recipe", "unsupported": preview.Unsupported})
		return
	}
	recipe, err := s.recipes.CreateImported(preview.Draft, "sparkrun")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, recipe)
}
