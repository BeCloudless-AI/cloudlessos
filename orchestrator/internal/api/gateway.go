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

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/provision"
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

// GatewayHandler builds the HTTP handler for the gateway listener: every /v1/* call
// is key-checked, then reverse-proxied to the local engine.
func (s *Server) GatewayHandler() http.Handler {
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", catalog.EnginePort))
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.FlushInterval = -1 // flush immediately so token streaming (SSE) isn't buffered
	orig := rp.Director
	rp.Director = func(req *http.Request) {
		orig(req)
		req.Host = target.Host
		req.Header.Del("Authorization") // the engine doesn't need (and shouldn't see) the user's key
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeOpenAIError(w, http.StatusBadGateway, "The Cloudless engine isn't reachable — make sure a model is loaded.")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.state.ValidateAPIKey(bearerToken(r))
		if !ok {
			writeOpenAIError(w, http.StatusUnauthorized, "Invalid API key. Pass a Cloudless key as 'Authorization: Bearer sk-cloudless-…'.")
			return
		}
		s.state.RecordUsage(id)
		rewriteModel(r) // let callers use any model name; the engine serves exactly one
		rec := &statusRec{ResponseWriter: w, status: 200}
		rp.ServeHTTP(rec, r)
		s.usage.RecordAPI(rec.status < 400) // success rate (API traffic)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "cloudless-proxy"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"service": "Cloudless Proxy",
			"hint":    "OpenAI-compatible API. POST /v1/chat/completions with Authorization: Bearer <your Cloudless key>.",
		})
	})
	return mux
}

// statusRec wraps a ResponseWriter to capture the response status for usage tracking,
// while preserving Flusher so the reverse proxy can still stream SSE.
type statusRec struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRec) WriteHeader(code int) {
	if !r.wrote {
		r.status, r.wrote = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}
func (r *statusRec) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}
func (r *statusRec) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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
func rewriteModel(r *http.Request) {
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
			m["model"] = servedModelName
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
	ID       string `json:"id"`
	Name     string `json:"name"`
	Prefix   string `json:"prefix"`
	Created  string `json:"created"`
	LastUsed string `json:"lastUsed,omitempty"`
	Requests int64  `json:"requests"`
}

func (s *Server) gatewayGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	keys := []keyView{}
	for _, k := range s.state.APIKeys() {
		keys = append(keys, keyView{k.ID, k.Name, k.Prefix, k.Created, k.LastUsed, k.Requests})
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
		"keys":       keys,
		"lan":        map[string]any{"enabled": lanOn, "ip": ip, "url": lanURL(lanOn, ip)},
		"tunnel":     map[string]any{"enabled": tunOn, "url": tunURL},
	})
}

func lanURL(on bool, ip string) string {
	if on && ip != "" {
		return fmt.Sprintf("http://%s:%d/v1", ip, GatewayPort)
	}
	return ""
}

func (s *Server) keyCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "API key"
	}
	secret, k, err := s.state.AddAPIKey(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The full secret is returned ONCE here and never again.
	writeJSON(w, http.StatusOK, map[string]any{
		"id": k.ID, "name": k.Name, "prefix": k.Prefix, "created": k.Created, "key": secret,
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
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "ip": ip, "url": lanURL(true, ip)})
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
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "url": url})
}
