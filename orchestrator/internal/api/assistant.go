package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/assistant"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/hardware"
)

// assistantChat streams a grounded reply from the built-in Cloudless Assistant.
// Request: {"messages":[{role,content}...]}. Response: SSE of {"delta":"..."} chunks
// then a final {"done":true,"actions":[...]} with one-click follow-ups.
func (s *Server) assistantChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []assistant.Msg `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	ctx := r.Context()
	actx := s.assistantContext(ctx)

	if !actx.EngineReady {
		send(map[string]any{"delta": "Your local AI engine is still warming up — give it a moment, then ask me again. " +
			"Meanwhile you can browse the App Launcher or pick a model in Settings → Cloudless AI."})
		send(map[string]any{"done": true, "actions": []assistant.Action{}})
		return
	}

	msgs := append([]assistant.Msg{{Role: "system", Content: assistant.SystemPrompt(actx)}}, body.Messages...)
	base := fmt.Sprintf("http://127.0.0.1:%d", catalog.EnginePort)

	var full strings.Builder
	err := assistant.Stream(ctx, base, "cloudless", msgs, func(tok string) {
		full.WriteString(tok)
		send(map[string]any{"delta": tok})
	})
	if err != nil {
		send(map[string]any{"delta": "\n\n(Sorry — I lost contact with the engine. Try again in a moment.)"})
	}

	lastUser := ""
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role == "user" {
			lastUser = body.Messages[i].Content
			break
		}
	}
	send(map[string]any{"done": true, "actions": assistant.Suggest(full.String(), lastUser, actx)})
}

// assistantContext snapshots live OS state for grounding.
func (s *Server) assistantContext(ctx context.Context) assistant.Context {
	st := s.state.Get()
	model := st.Model
	if model == "" {
		model = catalog.DefaultModel()
	}
	active := s.activeEngine(ctx)

	running := map[string]bool{}
	if list, err := s.eng.List(ctx); err == nil {
		for _, c := range list {
			if c.State == "running" {
				running[strings.TrimPrefix(c.Name, "cloudless-")] = true
			}
		}
	}
	return assistant.Context{
		Engine:      active,
		EngineReady: active != "" && engineReady(ctx),
		Model:       model,
		FirstRun:    s.state.FirstRun(),
		Onboarded:   st.Onboarded,
		Running:     running,
		GPU:         gpuSummary(ctx),
	}
}

// gpuSummary renders a short human description of the installed GPU(s).
func gpuSummary(ctx context.Context) string {
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	gpus, _ := hardware.GPUs(c)
	if len(gpus) == 0 {
		return "no NVIDIA GPU detected"
	}
	name := strings.TrimPrefix(gpus[0].Name, "NVIDIA GeForce ")
	gb := gpus[0].MemTotalMB / 1024
	if len(gpus) == 1 {
		return fmt.Sprintf("%s (%d GB)", name, gb)
	}
	return fmt.Sprintf("%d× %s (%d GB each)", len(gpus), name, gb)
}
