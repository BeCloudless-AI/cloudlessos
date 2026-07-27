package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/state"
)

// The Cloudless Proxy: an OpenAI-compatible gateway in front of the active engine,
// authenticated with user-generated API keys. It runs on its own port (separate from
// the no-auth dashboard) so it can be exposed on the LAN / online independently.
const (
	GatewayPort       = 8766
	gatewayLanName    = "cloudless-lan-gateway"
	gatewayTunnelName = "cloudless-tunnel-gateway"
	servedModelName   = "cloudless" // the engine's --served-model-name
)

// GatewayHandler exposes two authenticated surfaces on one shareable listener:
// /v1/* proxies raw model inference, while /agent/v1/* and /agent/api/* proxy
// Hermes' agent APIs. Hermes' dashboard is deliberately not reachable here.
func (s *Server) GatewayHandler() http.Handler {
	engineTarget, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", catalog.EnginePort))
	engineProxy := httputil.NewSingleHostReverseProxy(engineTarget)
	engineProxy.FlushInterval = -1
	orig := engineProxy.Director
	engineProxy.Director = func(req *http.Request) {
		orig(req)
		req.Host = engineTarget.Host
		apiPath, _ := s.activeRecipeGatewaySettings()
		if apiPath != "" && apiPath != "/v1" && strings.HasPrefix(req.URL.Path, "/v1") {
			req.URL.Path = strings.TrimRight(apiPath, "/") + strings.TrimPrefix(req.URL.Path, "/v1")
		}
		req.Header.Del("Authorization") // the engine doesn't need (and shouldn't see) the user's key
	}
	engineProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeOpenAIError(w, http.StatusBadGateway, "The Cloudless engine isn't reachable — make sure a model is loaded.")
	}

	hermesTarget, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", catalog.HermesAPIPort))
	hermesProxy := httputil.NewSingleHostReverseProxy(hermesTarget)
	hermesProxy.FlushInterval = -1
	hermesOrig := hermesProxy.Director
	hermesProxy.Director = func(req *http.Request) {
		hermesOrig(req)
		req.Host = hermesTarget.Host
		req.URL.Path = strings.TrimPrefix(req.URL.Path, "/agent")
		if key, err := apps.HermesAPIKey(s.appConfigDir("hermes")); err == nil {
			req.Header.Set("Authorization", "Bearer "+key)
		} else {
			req.Header.Del("Authorization")
		}
	}
	hermesProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeOpenAIError(w, http.StatusBadGateway, "The Cloudless agent isn't reachable — Hermes may still be starting.")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.authorizeGateway(w, r, state.APIKeyScopeModel)
		if !ok {
			return
		}
		_, modelName := s.activeRecipeGatewaySettings()
		rewriteModel(r, modelName) // let callers use any model name; the engine serves exactly one
		s.serveGatewayProxy(w, r, id, state.APIKeyScopeModel, engineProxy)
	})
	agent := func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.authorizeGateway(w, r, state.APIKeyScopeAgent)
		if !ok {
			return
		}
		s.serveGatewayProxy(w, r, id, state.APIKeyScopeAgent, hermesProxy)
	}
	mux.HandleFunc("/agent/v1/", agent)
	mux.HandleFunc("/agent/api/", agent)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok", "service": "cloudless-proxy", "agentReady": hermesReady(r.Context()),
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service": "Cloudless Proxy",
			"hint":    "Use /v1 for model inference or /agent/v1 for Hermes. Both require a scoped Cloudless key.",
		})
	})
	return mux
}

func (s *Server) authorizeGateway(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	token := bearerToken(r)
	if _, ok := s.state.ValidateAPIKey(token); !ok {
		writeOpenAIError(w, http.StatusUnauthorized, "Invalid API key. Pass a Cloudless key as 'Authorization: Bearer sk-cloudless-…'.")
		return "", false
	}
	id, ok := s.state.ValidateAPIKeyFor(token, scope)
	if !ok {
		writeOpenAIError(w, http.StatusForbidden, "This API key does not have permission to use the requested Cloudless service.")
		return "", false
	}
	return id, true
}

func (s *Server) serveGatewayProxy(w http.ResponseWriter, r *http.Request, id, kind string, proxy http.Handler) {
	rec := &statusRec{ResponseWriter: w, status: http.StatusOK}
	proxy.ServeHTTP(rec, r)
	success := rec.status < 400
	promptTokens, completionTokens := responseUsage(rec.capture)
	s.state.RecordAPIUsageKind(id, kind, success, promptTokens, completionTokens)
	s.usage.RecordAPI(success)
}

// statusRec wraps a ResponseWriter to capture the response status for usage tracking,
// while preserving Flusher so the reverse proxy can still stream SSE.
type statusRec struct {
	http.ResponseWriter
	status  int
	wrote   bool
	capture []byte
}

const usageCaptureLimit = 256 << 10

func (r *statusRec) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}
func (r *statusRec) Write(b []byte) (int, error) {
	r.wrote = true
	// Keep only the tail: JSON usage is near the end and streaming APIs emit it in
	// their final SSE event. This bounds memory even for very large generations.
	if len(b) >= usageCaptureLimit {
		r.capture = append(r.capture[:0], b[len(b)-usageCaptureLimit:]...)
	} else {
		over := len(r.capture) + len(b) - usageCaptureLimit
		if over > 0 {
			copy(r.capture, r.capture[over:])
			r.capture = r.capture[:len(r.capture)-over]
		}
		r.capture = append(r.capture, b...)
	}
	return r.ResponseWriter.Write(b)
}
func (r *statusRec) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// responseUsage extracts OpenAI-compatible token counts from a regular JSON
// response or from the final `data:` event of a streaming SSE response.
func responseUsage(body []byte) (prompt, completion int64) {
	type usageFields struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		InputTokens      int64 `json:"input_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
	}
	type envelope struct {
		Usage *usageFields `json:"usage"`
	}
	parse := func(b []byte) (int64, int64, bool) {
		var e envelope
		if json.Unmarshal(b, &e) != nil || e.Usage == nil {
			return 0, 0, false
		}
		p, c := e.Usage.PromptTokens, e.Usage.CompletionTokens
		if p == 0 {
			p = e.Usage.InputTokens
		}
		if c == 0 {
			c = e.Usage.OutputTokens
		}
		return p, c, true
	}
	if p, c, ok := parse(bytes.TrimSpace(body)); ok {
		return p, c
	}
	// A large non-streaming response may exceed the rolling capture. Decode the
	// trailing usage object directly even when the beginning of the JSON was dropped.
	if at := bytes.LastIndex(body, []byte(`"usage"`)); at >= 0 {
		if colon := bytes.IndexByte(body[at:], ':'); colon >= 0 {
			var u usageFields
			if json.NewDecoder(bytes.NewReader(body[at+colon+1:])).Decode(&u) == nil {
				p, c := u.PromptTokens, u.CompletionTokens
				if p == 0 {
					p = u.InputTokens
				}
				if c == 0 {
					c = u.OutputTokens
				}
				return p, c
			}
		}
	}
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if p, c, ok := parse(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))); ok {
			prompt, completion = p, c
		}
	}
	return prompt, completion
}

// bearerToken pulls the key from "Authorization: Bearer <key>" (case-insensitive scheme).
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// rewriteModel forces the request body's "model" to the engine's served name, so a
// caller can send any model id (e.g. the real HF id, or "gpt-4") and it just works.
// Best-effort: on any parse issue the original body is preserved untouched.
func (s *Server) activeRecipeGatewaySettings() (string, string) {
	current := s.state.Get()
	if current.LocalRecipeID != "" && !current.EngineUnloaded {
		if recipe, ok, err := s.recipes.Get(current.LocalRecipeID); err == nil && ok {
			path, name := recipe.Engine.APIPath, recipe.Engine.ServedModelName
			if path == "" {
				path = "/v1"
			}
			if name == "" {
				name = servedModelName
			}
			return path, name
		}
	}
	return "/v1", servedModelName
}

func rewriteModel(r *http.Request, activeModel string) {
	if r.Method != http.MethodPost || r.Body == nil {
		return
	}
	p := r.URL.Path
	if !(strings.HasSuffix(p, "/chat/completions") || strings.HasSuffix(p, "/completions") || strings.HasSuffix(p, "/embeddings")) {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	r.Body.Close()
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return
	}
	var m map[string]any
	if json.Unmarshal(body, &m) == nil {
		if _, has := m["model"]; has {
			m["model"] = activeModel
			if nb, err := json.Marshal(m); err == nil {
				body = nb
			}
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
}

// writeOpenAIError emits an OpenAI-style error envelope so SDKs surface it cleanly.
func writeOpenAIError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg, "type": "invalid_request_error", "code": nil},
	})
}

// ---- management API (on the dashboard listener) ----

// keyView is an API key without its secret hash (safe to send to the UI).
type keyView struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Prefix           string `json:"prefix"`
	Created          string `json:"created"`
	LastUsed         string `json:"lastUsed,omitempty"`
	Requests         int64  `json:"requests"`
	Successes        int64  `json:"successes"`
	Failures         int64  `json:"failures"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	TotalTokens      int64  `json:"totalTokens"`
	Scope            string `json:"scope"`
	ModelRequests    int64  `json:"modelRequests"`
	AgentRequests    int64  `json:"agentRequests"`
}

func (s *Server) gatewayGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	keys := []keyView{}
	for _, k := range s.state.APIKeys() {
		keys = append(keys, keyView{
			ID: k.ID, Name: k.Name, Prefix: k.Prefix, Created: k.Created, LastUsed: k.LastUsed,
			Requests: k.Requests, Successes: k.Successes, Failures: k.Failures,
			PromptTokens: k.PromptTokens, CompletionTokens: k.CompletionTokens,
			TotalTokens: k.PromptTokens + k.CompletionTokens,
			Scope:       state.NormalizeAPIKeyScope(k.Scope), ModelRequests: k.ModelRequests, AgentRequests: k.AgentRequests,
		})
	}

	// Exposure status (reuses the same socat/cloudflared sidecars as apps).
	lc, _ := s.eng.Find(ctx, gatewayLanName)
	lanOn := lc != nil && lc.State == "running"
	ip := provision.PrimaryLANIP()
	tc, _ := s.eng.Find(ctx, gatewayTunnelName)
	tunOn := tc != nil && tc.State == "running"
	tunURL := ""
	if tunOn {
		tunURL = s.tunnelURL(ctx, gatewayTunnelName)
	}

	model := s.state.Get().Model
	if model == "" {
		model = catalog.DefaultModel()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"port":       GatewayPort,
		"servedName": servedModelName, // what callers put in "model"
		"model":      model,           // the real model behind it
		"localURL":   fmt.Sprintf("http://localhost:%d/v1", GatewayPort),
		"agent": map[string]any{
			"ready": hermesReady(ctx), "servedName": "hermes-agent",
			"localURL": fmt.Sprintf("http://localhost:%d/agent/v1", GatewayPort),
		},
		"keys": keys,
		"lan": map[string]any{
			"enabled": lanOn, "ip": ip, "url": lanURL(lanOn, ip), "agentURL": agentLanURL(lanOn, ip),
		},
		"tunnel": map[string]any{
			"enabled": tunOn, "url": tunURL, "modelURL": appendURLPath(tunURL, "/v1"), "agentURL": appendURLPath(tunURL, "/agent/v1"),
		},
	})
}

func lanURL(on bool, ip string) string {
	if on && ip != "" {
		return fmt.Sprintf("http://%s:%d/v1", ip, GatewayPort)
	}
	return ""
}

func agentLanURL(on bool, ip string) string {
	if on && ip != "" {
		return fmt.Sprintf("http://%s:%d/agent/v1", ip, GatewayPort)
	}
	return ""
}

func appendURLPath(base, path string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + path
}

func (s *Server) keyCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "API key"
	}
	scope := state.NormalizeAPIKeyScope(body.Scope)
	secret, k, err := s.state.AddAPIKey(name, scope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The full secret is returned ONCE here and never again.
	writeJSON(w, http.StatusOK, map[string]any{
		"id": k.ID, "name": k.Name, "prefix": k.Prefix, "created": k.Created, "scope": k.Scope, "key": secret,
	})
}

func (s *Server) keyDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.state.DeleteAPIKey(r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// gatewayLanSet / gatewayTunnelSet expose the gateway port on the LAN / online,
// using the same host-networked socat + cloudflared sidecars apps use.
func (s *Server) gatewayLanSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if !body.Enable {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		_ = s.eng.Remove(ctx, gatewayLanName)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "url": ""})
		return
	}
	ip := provision.PrimaryLANIP()
	if ip == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not detect this machine's network address"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	_ = s.eng.Remove(ctx, gatewayLanName)
	img := s.infraImage(ctx, "socat", catalog.SocatImage)
	if err := s.eng.Pull(ctx, img); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not fetch forwarder: " + err.Error()})
		return
	}
	spec := engine.RunSpec{
		Name: gatewayLanName, Image: img, Network: "host",
		Args: []string{
			fmt.Sprintf("TCP-LISTEN:%d,bind=%s,fork,reuseaddr", GatewayPort, ip),
			fmt.Sprintf("TCP:127.0.0.1:%d", GatewayPort),
		},
	}
	if _, err := s.eng.Run(ctx, spec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "ip": ip, "url": lanURL(true, ip), "agentURL": agentLanURL(true, ip),
	})
}

func (s *Server) gatewayTunnelSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if !body.Enable {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		_ = s.eng.Remove(ctx, gatewayTunnelName)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "url": ""})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	_ = s.eng.Remove(ctx, gatewayTunnelName)
	img := s.infraImage(ctx, "cloudflared", catalog.CloudflaredImage)
	if err := s.eng.Pull(ctx, img); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not fetch cloudflared: " + err.Error()})
		return
	}
	if _, err := s.eng.Run(ctx, engine.RunSpec{
		Name: gatewayTunnelName, Image: img, Network: "host",
		Args: []string{"tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://localhost:%d", GatewayPort)},
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	url := ""
	for url == "" {
		if url = s.tunnelURL(ctx, gatewayTunnelName); url != "" {
			break
		}
		select {
		case <-ctx.Done():
			writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "url": "", "pending": true})
			return
		case <-time.After(600 * time.Millisecond):
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "url": url, "modelURL": appendURLPath(url, "/v1"), "agentURL": appendURLPath(url, "/agent/v1"),
	})
}
