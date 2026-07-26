package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/assistant"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/hardware"
	modelcatalog "github.com/cloudless/orchestrator/internal/models"
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
	lastUser := ""
	for i := len(body.Messages) - 1; i >= 0; i-- {
		if body.Messages[i].Role == "user" {
			lastUser = body.Messages[i].Content
			break
		}
	}
	if advice, handled := assistant.ModelGuidance(lastUser, actx); handled {
		send(map[string]any{"delta": advice.Reply})
		send(map[string]any{"done": true, "actions": []assistant.Action{{Kind: "models", ID: advice.ModelID, Label: advice.Label}}})
		return
	}

	if !actx.EngineReady {
		send(map[string]any{"delta": "Your local AI engine is still warming up — give it a moment, then ask me again. " +
			"Meanwhile you can browse the App Launcher or pick a model in Settings → Cloudless AI."})
		send(map[string]any{"done": true, "actions": []assistant.Action{}})
		return
	}
	// Keyword routing catches obvious requests without latency. Ambiguous requests
	// with a genuine model-management signal get a fact-free semantic classification;
	// ordinary chat bypasses the small classifier entirely. A positive result still
	// goes through the deterministic Model Manager advisor.
	modelRequest := assistant.RecentUserText(body.Messages, 3)
	if assistant.MayNeedModelRouting(modelRequest) {
		if modelControl, err := assistant.SemanticModelControl(ctx, fmt.Sprintf("http://127.0.0.1:%d", catalog.EnginePort), "cloudless", body.Messages); err == nil && modelControl {
			advice := assistant.ForcedModelGuidance(modelRequest, actx)
			send(map[string]any{"delta": advice.Reply})
			send(map[string]any{"done": true, "actions": []assistant.Action{{Kind: "models", ID: advice.ModelID, Label: advice.Label}}})
			return
		}
	}
	if !hermesReady(ctx) {
		send(map[string]any{"delta": "Your Cloudless agent is still starting. Hermes will be ready as soon as its local service finishes loading."})
		send(map[string]any{"done": true, "actions": []assistant.Action{}})
		return
	}

	msgs := append([]assistant.Msg{{Role: "system", Content: assistant.SystemPrompt(actx)}}, body.Messages...)
	base := fmt.Sprintf("http://127.0.0.1:%d", catalog.HermesAPIPort)
	apiKey, err := apps.HermesAPIKey(s.appConfigDir("hermes"))
	if err != nil {
		send(map[string]any{"delta": "Cloudless could not unlock the local Hermes service. Restart CloudlessOS and try again."})
		send(map[string]any{"done": true, "actions": []assistant.Action{}})
		return
	}

	var full strings.Builder
	err = assistant.Stream(ctx, base, apiKey, "hermes-agent", msgs, func(tok string) {
		full.WriteString(tok)
		send(map[string]any{"delta": tok})
	}, func(progress assistant.ToolProgress) {
		send(map[string]any{"tool": progress})
	})
	if err != nil {
		send(map[string]any{"delta": "\n\n(Sorry — I lost contact with the engine. Try again in a moment.)"})
	}

	send(map[string]any{"done": true, "actions": assistant.Suggest(full.String(), lastUser, actx)})
}

func hermesReady(ctx context.Context) bool {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", catalog.HermesAPIPort), nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *Server) hermesStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	container, _ := s.eng.Find(ctx, "cloudless-hermes")
	running := container != nil && container.State == "running"
	writeJSON(w, http.StatusOK, map[string]any{
		"installed":    container != nil,
		"running":      running,
		"ready":        running && hermesReady(ctx),
		"dashboardURL": fmt.Sprintf("http://127.0.0.1:%d/", catalog.HermesDashboardPort),
		"agentURL":     fmt.Sprintf("http://127.0.0.1:%d/v1", catalog.HermesAPIPort),
	})
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
	modelCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	modelList := s.mfModels.Highlights(modelCtx)
	cancel()
	modelList = modelcatalog.Merge(modelList)
	gpuGB, memoryType := acceleratorMemory(ctx)
	cluster := clusterCompute(ctx, gpuGB)
	modelOptions := make([]assistant.ModelOption, 0, len(modelList))
	currentListed := false
	for _, m := range modelList {
		if m.ID == model {
			currentListed = true
		}
		clusterFit := ""
		if cluster.DistributedReady {
			clusterFit = fitFor(m.MinVRAMGB, cluster.CombinedMemoryGB)
		}
		modelOptions = append(modelOptions, assistant.ModelOption{
			ID: m.ID, Name: m.Name, Params: m.Params, Quant: m.Quant,
			MinVRAMGB: m.MinVRAMGB, Fit: fitFor(m.MinVRAMGB, gpuGB), ClusterFit: clusterFit,
		})
	}
	if !currentListed && model != "" {
		name := model
		if at := strings.LastIndex(name, "/"); at >= 0 && at+1 < len(name) {
			name = name[at+1:]
		}
		modelOptions = append(modelOptions, assistant.ModelOption{ID: model, Name: name, Fit: "unknown"})
	}
	return assistant.Context{
		Engine:        active,
		EngineReady:   active != "" && engineReady(ctx),
		Model:         model,
		FirstRun:      s.state.FirstRun(),
		Onboarded:     st.Onboarded,
		Running:       running,
		GPU:           gpuSummary(ctx),
		GPUVRAMGB:     gpuGB,
		GPUMemoryType: memoryType,
		ClusterReady:  cluster.DistributedReady,
		ClusterPeer:   cluster.PeerName,
		ClusterMemory: cluster.CombinedMemoryGB,
		Models:        modelOptions,
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
		if gpus[0].MemoryType == "unified" {
			return fmt.Sprintf("%s (%d GB unified memory)", name, gb)
		}
		return fmt.Sprintf("%s (%d GB)", name, gb)
	}
	return fmt.Sprintf("%d× %s (%d GB each)", len(gpus), name, gb)
}
