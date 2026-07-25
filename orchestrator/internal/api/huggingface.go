package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/models"
)

var huggingFaceBaseURL = "https://huggingface.co"

var hfRepoPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (s *Server) huggingFaceToken() string {
	if s == nil || s.state == nil {
		return ""
	}
	token, _ := s.state.HuggingFaceToken()
	return token
}

// normalizeHuggingFaceRepo accepts either org/model or a normal Hub URL. It
// deliberately returns only a canonical repository id; user input is never used
// as a host, which keeps the server-side lookup from becoming an SSRF primitive.
func normalizeHuggingFaceRepo(input string) (string, bool) {
	value := strings.TrimSpace(input)
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		u, err := url.Parse(value)
		if err != nil || !strings.EqualFold(u.Hostname(), "huggingface.co") {
			return "", false
		}
		parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		if len(parts) < 2 {
			return "", false
		}
		decoded := make([]string, 0, 2)
		for _, part := range parts[:2] {
			part, err = url.PathUnescape(part)
			if err != nil {
				return "", false
			}
			decoded = append(decoded, part)
		}
		value = strings.Join(decoded, "/")
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !hfRepoPart.MatchString(parts[0]) || !hfRepoPart.MatchString(parts[1]) ||
		strings.Contains(parts[0], "..") || strings.Contains(parts[1], "..") {
		return "", false
	}
	return strings.Join(parts, "/"), true
}

type hfAPIModel struct {
	ID          string          `json:"id"`
	ModelID     string          `json:"modelId"`
	Author      string          `json:"author"`
	PipelineTag string          `json:"pipeline_tag"`
	LibraryName string          `json:"library_name"`
	Tags        []string        `json:"tags"`
	Downloads   int64           `json:"downloads"`
	Likes       int64           `json:"likes"`
	Gated       json.RawMessage `json:"gated"`
	Private     bool            `json:"private"`
	UsedStorage int64           `json:"usedStorage"`
	Config      struct {
		ModelType string `json:"model_type"`
	} `json:"config"`
	CardData struct {
		License any `json:"license"`
	} `json:"cardData"`
	Safetensors struct {
		Parameters map[string]int64 `json:"parameters"`
		Total      int64            `json:"total"`
	} `json:"safetensors"`
	Siblings []struct {
		Name string `json:"rfilename"`
		Size int64  `json:"size"`
		LFS  *struct {
			Size int64 `json:"size"`
		} `json:"lfs"`
	} `json:"siblings"`
}

type hfSearchView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Author      string   `json:"author"`
	Downloads   int64    `json:"downloads"`
	Likes       int64    `json:"likes"`
	Gated       bool     `json:"gated"`
	Private     bool     `json:"private"`
	Tags        []string `json:"tags"`
	SourceURL   string   `json:"sourceUrl"`
	PipelineTag string   `json:"pipelineTag"`
}

func hfGated(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "false" || string(raw) == "null" || string(raw) == `"false"` {
		return false
	}
	return true
}

func generativePipeline(tag string) bool {
	switch strings.ToLower(tag) {
	case "text-generation", "text2text-generation", "image-text-to-text", "conversational":
		return true
	default:
		return false
	}
}

func hfGetJSON(ctx context.Context, requestPath string, target any) (int, error) {
	return hfGetJSONWithToken(ctx, requestPath, "", target)
}

func hfGetJSONWithToken(ctx context.Context, requestPath, token string, target any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, huggingFaceBaseURL+requestPath, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "CloudlessOS-Model-Manager/1")
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("Hugging Face returned %s", resp.Status)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(target)
}

func compactHFName(id string) string {
	name := path.Base(id)
	return strings.ReplaceAll(name, "-", " ")
}

func displayCount(value int64) string {
	switch {
	case value >= 1_000_000_000:
		return strconv.FormatFloat(float64(value)/1_000_000_000, 'f', 1, 64) + "B"
	case value >= 1_000_000:
		return strconv.FormatFloat(float64(value)/1_000_000, 'f', 1, 64) + "M"
	default:
		return strconv.FormatInt(value, 10)
	}
}

func hfQuant(id string, tags []string) (string, int) {
	joined := strings.ToLower(id + " " + strings.Join(tags, " "))
	for _, q := range []struct {
		need string
		name string
		bits int
	}{{"awq", "AWQ", 4}, {"gptq", "GPTQ", 4}, {"gguf", "GGUF", 4}, {"fp4", "FP4", 4}, {"int4", "INT4", 4}, {"4bit", "4-bit", 4}, {"fp8", "FP8", 8}, {"int8", "INT8", 8}, {"8bit", "8-bit", 8}} {
		if strings.Contains(joined, q.need) {
			return q.name, q.bits
		}
	}
	return "", 16
}

func hfLicense(info hfAPIModel) string {
	if value, ok := info.CardData.License.(string); ok && value != "" {
		return value
	}
	for _, tag := range info.Tags {
		if value, ok := strings.CutPrefix(tag, "license:"); ok {
			return value
		}
	}
	return "See model card"
}

func usefulHFTags(info hfAPIModel) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tag := range info.Tags {
		lower := strings.ToLower(tag)
		label := ""
		switch {
		case lower == "chat" || lower == "conversational":
			label = "chat"
		case strings.Contains(lower, "code"):
			label = "coding"
		case strings.Contains(lower, "reason"):
			label = "reasoning"
		case strings.Contains(lower, "vision") || lower == "image-text-to-text":
			label = "vision"
		case lower == "multilingual":
			label = "multilingual"
		}
		if label != "" && !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
		if len(out) == 5 {
			break
		}
	}
	return out
}

func hfParameterTotal(info hfAPIModel) int64 {
	if info.Safetensors.Total > 0 {
		return info.Safetensors.Total
	}
	var total int64
	for _, count := range info.Safetensors.Parameters {
		total += count
	}
	return total
}

func hfContextK(ctx context.Context, repo, token string) int {
	var cfg map[string]any
	status, err := hfGetJSONWithToken(ctx, "/"+repo+"/resolve/main/config.json", token, &cfg)
	if err != nil || status != http.StatusOK {
		return 0
	}
	read := func(source map[string]any) int64 {
		for _, key := range []string{"max_position_embeddings", "max_sequence_length", "seq_length", "n_positions"} {
			if value, ok := source[key].(float64); ok && value > 0 {
				return int64(value)
			}
		}
		return 0
	}
	tokens := read(cfg)
	if text, ok := cfg["text_config"].(map[string]any); ok && tokens == 0 {
		tokens = read(text)
	}
	if tokens <= 0 {
		return 0
	}
	return int(math.Ceil(float64(tokens) / 1000))
}

func hfModelFromInfo(ctx context.Context, repo string, info hfAPIModel, token ...string) models.Model {
	authToken := ""
	if len(token) > 0 {
		authToken = token[0]
	}
	params := hfParameterTotal(info)
	quant, bits := hfQuant(repo, info.Tags)
	minGB := 0
	if params > 0 {
		weightGB := float64(params) * float64(bits) / 8 / 1_000_000_000
		minGB = int(math.Ceil(weightGB*1.15 + 2))
	} else if info.UsedStorage > 0 {
		minGB = int(math.Ceil(float64(info.UsedStorage)/1_000_000_000*1.15 + 2))
	}
	tags := usefulHFTags(info)
	joined := strings.ToLower(repo + " " + strings.Join(info.Tags, " "))
	use := "general"
	if strings.Contains(joined, "code") {
		use = "coding"
	} else if strings.Contains(joined, "vision") || info.PipelineTag == "image-text-to-text" {
		use = "vision"
	}
	runtimeStatus := "likely"
	runtimeNote := "Community model metadata indicates a Transformers text-generation repository. Final compatibility is confirmed when the active engine loads it."
	if info.LibraryName != "" && info.LibraryName != "transformers" {
		runtimeStatus = "unverified"
		runtimeNote = "This repository uses " + info.LibraryName + "; the active Cloudless engine may require a custom launch command."
	}
	return models.Model{
		ID: repo, Name: compactHFName(repo), Family: info.Config.ModelType,
		Params: func() string {
			if params > 0 {
				return displayCount(params)
			}
			return "Unknown"
		}(),
		Quant: quant, ContextK: hfContextK(ctx, repo, authToken), MinVRAMGB: minGB,
		Use: use, Tags: tags, ToolCalling: strings.Contains(joined, "chat") || strings.Contains(joined, "instruct"),
		Vision: use == "vision", License: hfLicense(info), Gated: hfGated(info.Gated),
		Description: "Community model from " + strings.Split(repo, "/")[0] + " on Hugging Face. Repository metadata was checked before it was added to CloudlessOS.",
		Source:      "huggingface", SourceURL: huggingFaceBaseURL + "/" + repo,
		RuntimeStatus: runtimeStatus, RuntimeNote: runtimeNote,
	}
}

func (s *Server) huggingFaceSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 || len(query) > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter at least 2 characters"})
		return
	}
	if repo, ok := normalizeHuggingFaceRepo(query); ok {
		query = repo
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	token := s.huggingFaceToken()
	values := url.Values{"search": {query}, "sort": {"downloads"}, "direction": {"-1"}, "limit": {"18"}, "full": {"true"}}
	var remote []hfAPIModel
	status, err := hfGetJSONWithToken(ctx, "/api/models?"+values.Encode(), token, &remote)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not search Hugging Face right now"})
		return
	}
	_ = status
	results := []hfSearchView{}
	for _, info := range remote {
		repo := info.ID
		if repo == "" {
			repo = info.ModelID
		}
		if _, ok := normalizeHuggingFaceRepo(repo); !ok || (info.Private && token == "") || !generativePipeline(info.PipelineTag) {
			continue
		}
		results = append(results, hfSearchView{ID: repo, Name: compactHFName(repo), Author: strings.Split(repo, "/")[0], Downloads: info.Downloads, Likes: info.Likes, Gated: hfGated(info.Gated), Private: info.Private, Tags: usefulHFTags(info), SourceURL: huggingFaceBaseURL + "/" + repo, PipelineTag: info.PipelineTag})
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": query, "results": results})
}

func (s *Server) huggingFaceImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	repo, ok := normalizeHuggingFaceRepo(body.ID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter a Hugging Face URL or repository id such as Qwen/Qwen2.5-7B-Instruct"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	token := s.huggingFaceToken()
	var info hfAPIModel
	status, err := hfGetJSONWithToken(ctx, "/api/models/"+repo+"?blobs=true", token, &info)
	if err != nil {
		if status == http.StatusNotFound {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "model repository not found"})
			return
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this repository is private or unavailable"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not read this Hugging Face repository"})
		return
	}
	if (info.Private && token == "") || !generativePipeline(info.PipelineTag) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "this is not a public generative language-model repository"})
		return
	}
	model := hfModelFromInfo(ctx, repo, info, token)
	if err := s.state.UpsertCustomModel(model); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the imported model"})
		return
	}
	gpuGB, _ := acceleratorMemory(r.Context())
	have := s.downloadedModels(r.Context())
	current := s.state.Get().Model
	if current == "" {
		current = catalog.DefaultModel()
	}
	writeJSON(w, http.StatusOK, modelView{Model: model, Fit: fitFor(model.MinVRAMGB, gpuGB), Active: repo == current, Downloaded: have[repo]})
}

type hfAccountView struct {
	Connected bool   `json:"connected"`
	Valid     bool   `json:"valid"`
	Name      string `json:"name,omitempty"`
	FullName  string `json:"fullName,omitempty"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

type hfWhoAmI struct {
	Name      string `json:"name"`
	FullName  string `json:"fullname"`
	AvatarURL string `json:"avatarUrl"`
}

func huggingFaceWhoAmI(ctx context.Context, token string) (hfAccountView, int, error) {
	var who hfWhoAmI
	status, err := hfGetJSONWithToken(ctx, "/api/whoami-v2", token, &who)
	if err != nil {
		return hfAccountView{Connected: true}, status, err
	}
	return hfAccountView{Connected: true, Valid: true, Name: who.Name, FullName: who.FullName, AvatarURL: who.AvatarURL}, status, nil
}

func (s *Server) huggingFaceAccount(w http.ResponseWriter, r *http.Request) {
	token, err := s.state.HuggingFaceToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read Hugging Face account"})
		return
	}
	if token == "" {
		writeJSON(w, http.StatusOK, hfAccountView{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	account, status, err := huggingFaceWhoAmI(ctx, token)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			writeJSON(w, http.StatusOK, account)
			return
		}
		writeJSON(w, http.StatusOK, account)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) huggingFaceConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if body.Token = strings.TrimSpace(body.Token); len(body.Token) < 10 || len(body.Token) > 4096 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter a valid Hugging Face access token"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	account, status, err := huggingFaceWhoAmI(ctx, body.Token)
	if err != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Hugging Face rejected this token"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not verify the Hugging Face account"})
		return
	}
	if err := s.state.SetHuggingFaceToken(body.Token); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not securely save the Hugging Face account"})
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) huggingFaceDisconnect(w http.ResponseWriter, _ *http.Request) {
	if err := s.state.ClearHuggingFaceToken(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not disconnect the Hugging Face account"})
		return
	}
	writeJSON(w, http.StatusOK, hfAccountView{})
}

// importedModels returns stable ordering for deterministic Model Manager output.
func (s *Server) importedModels() []models.Model {
	stored := s.state.CustomModels()
	out := make([]models.Model, 0, len(stored))
	for _, model := range stored {
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
