package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
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
	gatewayLanName    = "cloudless-lan-gateway"
	gatewayTunnelName = "cloudless-tunnel-gateway"
	servedModelName   = "cloudless" // private engine identity; never user-editable
)

var inferenceAliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,63}$`)

func (s *Server) inferenceContract() state.InferenceContract { return s.state.InferenceContract() }

// GatewayHandler exposes two authenticated surfaces on one shareable listener:
// /v1/* proxies an allow-listed model inference surface, while /agent/v1/*
// proxies only Hermes' OpenAI-compatible chat surface. Administrative APIs and
// Hermes' dashboard are deliberately not reachable here.
func (s *Server) GatewayHandler() http.Handler {
	engineTarget, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", catalog.EnginePort))
	engineProxy := httputil.NewSingleHostReverseProxy(engineTarget)
	engineProxy.FlushInterval = -1
	orig := engineProxy.Director
	engineProxy.Director = func(req *http.Request) {
		orig(req)
		// Backend inference always stays on the reserved private engine socket.
		// The configurable port belongs only to this authenticated gateway.
		req.Host = engineTarget.Host
		apiPath, _ := s.activeRecipeGatewaySettings()
		if apiPath != "" && apiPath != "/v1" && strings.HasPrefix(req.URL.Path, "/v1") {
			req.URL.Path = strings.TrimRight(apiPath, "/") + strings.TrimPrefix(req.URL.Path, "/v1")
		}
		req.Header.Del("Authorization") // the engine doesn't need (and shouldn't see) the user's key
	}
	engineProxy.ModifyResponse = func(resp *http.Response) error {
		// Present the install-wide client alias even though every private backend
		// deliberately keeps serving the internal "cloudless" identity.
		alias := s.inferenceContract().ModelAlias
		contentType := strings.ToLower(resp.Header.Get("Content-Type"))
		if alias == servedModelName || (!strings.Contains(contentType, "json") && !strings.Contains(contentType, "event-stream")) {
			return nil
		}
		resp.Body = &gatewayAliasBody{src: resp.Body, reader: bufio.NewReader(resp.Body), alias: alias}
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		return nil
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
		if !gatewayRouteAllowed(r.Method, r.URL.Path, false) {
			writeOpenAIError(w, http.StatusNotFound, "This route is not exposed by the Cloudless model gateway.")
			return
		}
		id, ok := s.authorizeGateway(w, r, state.APIKeyScopeModel)
		if !ok {
			return
		}
		_, modelName := s.activeRecipeGatewaySettings()
		rewriteModel(r, modelName) // let callers use any model name; the engine serves exactly one
		s.serveGatewayProxy(w, r, id, state.APIKeyScopeModel, engineProxy)
	})
	agent := func(w http.ResponseWriter, r *http.Request) {
		if !gatewayRouteAllowed(r.Method, strings.TrimPrefix(r.URL.Path, "/agent"), true) {
			writeOpenAIError(w, http.StatusNotFound, "This route is not exposed by the Cloudless agent gateway.")
			return
		}
		id, ok := s.authorizeGateway(w, r, state.APIKeyScopeAgent)
		if !ok {
			return
		}
		s.serveGatewayProxy(w, r, id, state.APIKeyScopeAgent, hermesProxy)
	}
	mux.HandleFunc("/agent/v1/", agent)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok", "service": "cloudless-proxy", "agentReady": hermesReady(r.Context()),
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			writeOpenAIError(w, http.StatusNotFound, "This route is not exposed by the Cloudless gateway.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"service": "Cloudless Proxy",
			"hint":    "Use /v1 for model inference or /agent/v1 for Hermes. Both require a scoped Cloudless key.",
		})
	})
	return mux
}

func gatewayRouteAllowed(method, path string, agent bool) bool {
	if method == http.MethodGet && path == "/v1/models" {
		return true
	}
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/chat/completions":
		return true
	case "/v1/completions", "/v1/embeddings":
		return !agent
	default:
		return false
	}
}

// gatewayAliasBody rewrites line-oriented JSON/SSE without buffering a streamed
// completion. It also handles ordinary one-line JSON responses at EOF.
type gatewayAliasBody struct {
	src     io.ReadCloser
	reader  *bufio.Reader
	pending []byte
	alias   string
}

func (b *gatewayAliasBody) Read(dst []byte) (int, error) {
	for len(b.pending) == 0 {
		line, err := b.reader.ReadBytes('\n')
		if len(line) > 0 {
			encoded, _ := json.Marshal(b.alias)
			line = bytes.ReplaceAll(line, []byte(`"model":"cloudless"`), append([]byte(`"model":`), encoded...))
			line = bytes.ReplaceAll(line, []byte(`"id":"cloudless"`), append([]byte(`"id":`), encoded...))
			b.pending = line
		}
		if err != nil && len(b.pending) == 0 {
			return 0, err
		}
	}
	n := copy(dst, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *gatewayAliasBody) Close() error { return b.src.Close() }

func (s *Server) authorizeGateway(w http.ResponseWriter, r *http.Request, scope string) (string, bool) {
	source := gatewayRequestSource(r)
	if allowed, retryAfter := s.gatewaySourceRateLimiter().allow(source); !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		s.auditGateway(gatewayAuditEvent{
			Event: "gateway-rate-limit", Outcome: "denied", Scope: scope,
			Method: r.Method, Path: r.URL.Path, Source: source,
			Status: http.StatusTooManyRequests, Detail: "source request limit exceeded",
		})
		writeOpenAIError(w, http.StatusTooManyRequests, "This client is sending requests too quickly. Retry after the indicated delay.")
		return "", false
	}
	token := bearerToken(r)
	id, authenticated := s.state.ValidateAPIKey(token)
	if !authenticated {
		s.auditGateway(gatewayAuditEvent{
			Event: "gateway-auth", Outcome: "denied", Scope: scope,
			Method: r.Method, Path: r.URL.Path, Source: gatewayRequestSource(r),
			Status: http.StatusUnauthorized, Detail: "invalid credential",
		})
		writeOpenAIError(w, http.StatusUnauthorized, "Invalid API key. Pass a Cloudless key as 'Authorization: Bearer sk-cloudless-…'.")
		return "", false
	}
	id, ok := s.state.ValidateAPIKeyFor(token, scope)
	if !ok {
		s.auditGateway(gatewayAuditEvent{
			Event: "gateway-auth", Outcome: "denied", KeyID: id, Scope: scope,
			Method: r.Method, Path: r.URL.Path, Source: gatewayRequestSource(r),
			Status: http.StatusForbidden, Detail: "scope denied",
		})
		writeOpenAIError(w, http.StatusForbidden, "This API key does not have permission to use the requested Cloudless service.")
		return "", false
	}
	limiter, _ := s.gatewaySecurity()
	if allowed, retryAfter := limiter.allow(id); !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		s.auditGateway(gatewayAuditEvent{
			Event: "gateway-rate-limit", Outcome: "denied", KeyID: id, Scope: scope,
			Method: r.Method, Path: r.URL.Path, Source: gatewayRequestSource(r),
			Status: http.StatusTooManyRequests, Detail: "per-key request limit exceeded",
		})
		writeOpenAIError(w, http.StatusTooManyRequests, "This API key is sending requests too quickly. Retry after the indicated delay.")
		return "", false
	}
	return id, true
}

func (s *Server) serveGatewayProxy(w http.ResponseWriter, r *http.Request, id, kind string, proxy http.Handler) {
	started := time.Now()
	rec := &statusRec{ResponseWriter: w, status: http.StatusOK}
	proxy.ServeHTTP(rec, r)
	success := rec.status < 400
	promptTokens, completionTokens := responseUsage(rec.capture)
	s.state.RecordAPIUsageKind(id, kind, success, promptTokens, completionTokens)
	if s.usage != nil {
		s.usage.RecordAPI(success)
	}
	outcome := "success"
	if !success {
		outcome = "error"
	}
	s.auditGateway(gatewayAuditEvent{
		Event: "gateway-request", Outcome: outcome, KeyID: id, Scope: kind,
		Method: r.Method, Path: r.URL.Path, Source: gatewayRequestSource(r),
		Status: rec.status, DurationMS: time.Since(started).Milliseconds(),
	})
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
	// This is deliberately independent of persisted recipe data. Recipes,
	// custom engines and command overrides may choose implementation details,
	// but none of them can redirect the OS gateway or change the private model
	// identity used by every Cloudless client.
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

	contract := s.inferenceContract()
	var tailConnected, tailOn bool
	var tailIP, tailDNS string
	if s.remoteAccess != nil {
		tailStatus := s.remoteAccess.Status(ctx)
		tailConnected = tailStatus.Connected
		tailOn = tailConnected && tailStatus.ServesTCP(contract.Port)
		tailIP = tailscaleIPv4(tailStatus.IPs)
		tailDNS = tailStatus.DNSName
	}
	modelURL, agentURL := gatewayClientURLs(lanOn, ip, contract.Port)
	model := s.state.Get().Model
	if model == "" {
		model = catalog.DefaultModel()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"port":       contract.Port,
		"servedName": contract.ModelAlias, // what callers put in "model"
		"model":      model,               // the real model behind it
		"localURL":   modelURL,
		"agent": map[string]any{
			"ready": hermesReady(ctx), "servedName": "hermes-agent",
			"localURL": agentURL,
		},
		"keys": keys,
		"lan": map[string]any{
			"enabled": lanOn, "ip": ip, "url": lanURL(lanOn, ip, contract.Port), "agentURL": agentLanURL(lanOn, ip, contract.Port),
		},
		"tailnet": map[string]any{
			"connected": tailConnected, "enabled": tailOn, "ip": tailIP, "dnsName": tailDNS,
			"url": tailnetURL(tailOn, tailIP, contract.Port, "/v1"), "agentURL": tailnetURL(tailOn, tailIP, contract.Port, "/agent/v1"),
		},
		"tunnel": map[string]any{
			"enabled": tunOn, "url": tunURL, "modelURL": appendURLPath(tunURL, "/v1"), "agentURL": appendURLPath(tunURL, "/agent/v1"),
		},
		"policy": map[string]any{
			"authenticationRequired": true,
			"scopedKeys":             true,
			"requestsPerMinute":      gatewayRequestsPerMinute,
			"burst":                  gatewayBurst,
			"auditEnabled":           true,
		},
	})
}

func (s *Server) gatewayAuditGet(w http.ResponseWriter, r *http.Request) {
	s.securityAuditGet(w, r)
}

func (s *Server) securityAuditGet(w http.ResponseWriter, r *http.Request) {
	_, audit := s.gatewaySecurity()
	if audit == nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []gatewayAuditEvent{}})
		return
	}
	events, err := audit.Latest(100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read security audit history"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func validateInferenceContract(contract state.InferenceContract) error {
	contract = contract.Normalized()
	if contract.Port < 1024 || contract.Port > 65535 {
		return fmt.Errorf("API port must be between 1024 and 65535")
	}
	for _, reserved := range []int{8000, 8642, 8765, 9119} {
		if contract.Port == reserved {
			return fmt.Errorf("port %d is reserved by CloudlessOS", contract.Port)
		}
	}
	if !inferenceAliasPattern.MatchString(contract.ModelAlias) {
		return fmt.Errorf("model name must be 1-64 URL-safe characters without spaces")
	}
	return nil
}

// inferenceContractSet updates the single client-facing identity. Recipes and
// custom engines cannot override it; their private details stay behind the
// authenticated gateway.
func (s *Server) inferenceContractSet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "gateway-contract" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "explicit API identity confirmation required"})
		return
	}
	var requested state.InferenceContract
	if err := json.NewDecoder(r.Body).Decode(&requested); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	requested = requested.Normalized()
	if err := validateInferenceContract(requested); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	previous := s.inferenceContract()
	if requested.Port != previous.Port {
		if s.gatewayRebind == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the API listener cannot be reconfigured in this environment"})
			return
		}
		if err := s.gatewayRebind(requested.Port); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "that port is unavailable: " + err.Error()})
			return
		}
	}
	if err := s.state.SetInferenceContract(requested); err != nil {
		if requested.Port != previous.Port && s.gatewayRebind != nil {
			_ = s.gatewayRebind(previous.Port)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if requested.Port != previous.Port {
		s.restartGatewayExposure(r.Context(), previous.Port, requested.Port)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"port": requested.Port, "servedName": requested.ModelAlias,
		"localURL": fmt.Sprintf("http://localhost:%d/v1", requested.Port),
	})
}

func (s *Server) restartGatewayExposure(parent context.Context, previousPort, port int) {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	lan, _ := s.eng.Find(ctx, gatewayLanName)
	tunnel, _ := s.eng.Find(ctx, gatewayTunnelName)
	lanEnabled := lan != nil && lan.State == "running"
	tunnelEnabled := tunnel != nil && tunnel.State == "running"
	tailnetEnabled := false
	if s.remoteAccess != nil {
		tailnetEnabled = s.remoteAccess.Status(ctx).ServesTCP(previousPort)
	}
	_ = s.eng.Remove(ctx, gatewayLanName)
	_ = s.eng.Remove(ctx, gatewayTunnelName)
	if lanEnabled {
		if ip := provision.PrimaryLANIP(); ip != "" {
			_, _ = s.eng.Run(ctx, gatewayLANSpec(s.infraImage(ctx, "socat", catalog.SocatImage), ip, port))
		}
	}
	if tunnelEnabled {
		_, _ = s.eng.Run(ctx, gatewayTunnelSpec(s.infraImage(ctx, "cloudflared", catalog.CloudflaredImage), port))
	}
	if tailnetEnabled {
		_ = s.remoteAccess.SetAPIServe(ctx, false, previousPort)
		_ = s.remoteAccess.SetAPIServe(ctx, true, port)
	}
}

func lanURL(on bool, ip string, port int) string {
	if on && ip != "" {
		return fmt.Sprintf("http://%s:%d/v1", ip, port)
	}
	return ""
}

func agentLanURL(on bool, ip string, port int) string {
	if on && ip != "" {
		return fmt.Sprintf("http://%s:%d/agent/v1", ip, port)
	}
	return ""
}

func tailscaleIPv4(ips []string) string {
	for _, value := range ips {
		if ip := net.ParseIP(value); ip != nil && ip.To4() != nil {
			return value
		}
	}
	return ""
}

func tailnetURL(on bool, ip string, port int, path string) string {
	if !on || ip == "" {
		return ""
	}
	return fmt.Sprintf("http://%s:%d%s", ip, port, path)
}

// gatewayClientURLs returns the address clients should actually copy from the
// API Access page. Loopback is correct while access is machine-local; once the
// LAN forwarder is running, other devices need the machine's LAN address.
func gatewayClientURLs(lanEnabled bool, ip string, port int) (string, string) {
	if modelURL, agentURL := lanURL(lanEnabled, ip, port), agentLanURL(lanEnabled, ip, port); modelURL != "" && agentURL != "" {
		return modelURL, agentURL
	}
	return fmt.Sprintf("http://localhost:%d/v1", port), fmt.Sprintf("http://localhost:%d/agent/v1", port)
}

func appendURLPath(base, path string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + path
}

func (s *Server) keyCreate(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "gateway-key-create" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "explicit API key creation confirmation required"})
		return
	}
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
	s.auditGateway(gatewayAuditEvent{Event: "gateway-key", Outcome: "created", KeyID: k.ID, Scope: k.Scope})
	// The full secret is returned ONCE here and never again.
	writeJSON(w, http.StatusOK, map[string]any{
		"id": k.ID, "name": k.Name, "prefix": k.Prefix, "created": k.Created, "scope": k.Scope, "key": secret,
	})
}

func (s *Server) keyDelete(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "gateway-key-revoke" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "explicit API key revocation confirmation required"})
		return
	}
	id := r.PathValue("id")
	if err := s.state.DeleteAPIKey(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.auditGateway(gatewayAuditEvent{Event: "gateway-key", Outcome: "revoked", KeyID: id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// gatewayLanSet / gatewayTunnelSet expose the gateway port on the LAN / online,
// using the same host-networked socat + cloudflared sidecars apps use.
func (s *Server) gatewayLanSet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "gateway-lan" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "explicit local-network access confirmation required"})
		return
	}
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
		s.auditGateway(gatewayAuditEvent{Event: "gateway-exposure", Outcome: "disabled", Scope: "lan"})
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "url": ""})
		return
	}
	if len(s.state.APIKeys()) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "create a scoped API key before enabling network access"})
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
	port := s.inferenceContract().Port
	spec := gatewayLANSpec(img, ip, port)
	if _, err := s.eng.Run(ctx, spec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.auditGateway(gatewayAuditEvent{Event: "gateway-exposure", Outcome: "enabled", Scope: "lan", Source: ip})
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "ip": ip, "url": lanURL(true, ip, port), "agentURL": agentLanURL(true, ip, port),
	})
}

func gatewayLANSpec(image, ip string, port int) engine.RunSpec {
	return engine.RunSpec{Name: gatewayLanName, Image: image, Network: "host", Args: []string{
		fmt.Sprintf("TCP-LISTEN:%d,bind=%s,fork,reuseaddr", port, ip),
		fmt.Sprintf("TCP:127.0.0.1:%d", port),
	}}
}

func (s *Server) gatewayTailnetSet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "gateway-tailnet" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "explicit Tailnet API access confirmation required"})
		return
	}
	var body struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if s.remoteAccess == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Tailscale is unavailable"})
		return
	}
	if body.Enable && len(s.state.APIKeys()) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "create a scoped API key before enabling Tailnet access"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status := s.remoteAccess.Status(ctx)
	if body.Enable && (!status.Installed || !status.Connected) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "connect Tailscale before enabling Tailnet API access"})
		return
	}
	port := s.inferenceContract().Port
	if err := s.remoteAccess.SetAPIServe(ctx, body.Enable, port); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	ip := tailscaleIPv4(status.IPs)
	outcome := "disabled"
	if body.Enable {
		outcome = "enabled"
	}
	s.auditGateway(gatewayAuditEvent{Event: "gateway-exposure", Outcome: outcome, Scope: "tailnet", Source: ip})
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": body.Enable, "ip": ip,
		"url": tailnetURL(body.Enable, ip, port, "/v1"), "agentURL": tailnetURL(body.Enable, ip, port, "/agent/v1"),
	})
}

func (s *Server) gatewayTunnelSet(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "gateway-public" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "explicit public-access confirmation required"})
		return
	}
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
		s.auditGateway(gatewayAuditEvent{Event: "gateway-exposure", Outcome: "disabled", Scope: "public"})
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "url": ""})
		return
	}
	if len(s.state.APIKeys()) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "create a scoped API key before enabling public access"})
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
	if _, err := s.eng.Run(ctx, gatewayTunnelSpec(img, s.inferenceContract().Port)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.auditGateway(gatewayAuditEvent{Event: "gateway-exposure", Outcome: "enabled", Scope: "public"})
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

func gatewayTunnelSpec(image string, port int) engine.RunSpec {
	return engine.RunSpec{Name: gatewayTunnelName, Image: image, Network: "host",
		Args: []string{"tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://localhost:%d", port)}}
}
