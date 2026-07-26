// Package assistant powers the built-in CloudlessOS guide. It grounds a chat in
// live OS state + the app catalog, talks to the local Hermes agent API, and
// turns guidance into one-click OS actions. Stdlib-only.
package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudless/orchestrator/internal/catalog"
)

// Msg is one chat turn (OpenAI shape).
type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Context is the live OS state the assistant is grounded in.
type Context struct {
	Engine        string          // active engine id ("vllm"/"sglang"/""), "" = none yet
	EngineReady   bool            // engine is serving
	Model         string          // served model (HF id)
	FirstRun      bool            // no prior state file at startup
	Onboarded     bool            // finished first-run tour
	Running       map[string]bool // app id -> running
	GPU           string          // human summary, e.g. "RTX 5090 (32 GB)"
	GPUVRAMGB     int             // total VRAM used by Model Manager fit calculations
	GPUMemoryType string          // dedicated | unified | unknown
	ClusterReady  bool            // healthy two-Spark fabric is available for distributed inference
	ClusterPeer   string          // display name of the connected peer
	ClusterMemory int             // approximate combined accelerator memory across both Sparks
	Models        []ModelOption   // curated choices and fit verdicts shown by Model Manager
}

// ModelOption is the authoritative subset of Model Manager data needed for
// compatibility answers. It prevents the assistant from guessing model fit.
type ModelOption struct {
	ID         string
	Name       string
	Params     string
	Quant      string
	MinVRAMGB  int
	Fit        string // fits | tight | over | unknown
	ClusterFit string // distributed fit across a healthy two-Spark cluster
}

// ModelAdvice is a deterministic compatibility answer plus the Model Manager
// destination the GUI should open for review.
type ModelAdvice struct {
	Reply   string
	ModelID string
	Label   string
}

// Action is a one-click follow-up the OS can execute on the user's behalf.
type Action struct {
	Kind  string `json:"kind"`         // install | open | chat | engine | models
	ID    string `json:"id,omitempty"` // app or engine id
	Label string `json:"label"`        // button text
}

type ToolProgress struct {
	Tool       string `json:"tool"`
	Emoji      string `json:"emoji,omitempty"`
	Label      string `json:"label,omitempty"`
	ToolCallID string `json:"toolCallId"`
	Status     string `json:"status"`
}

// plain-language "what it's for", beyond the catalog one-liners.
var useCase = map[string]string{
	"comfyui":    "generate and edit images locally (Stable Diffusion / Flux) with node-based workflows",
	"open-webui": "a separate, optional full-featured chat app (multi-session, file uploads) for the local model",
	"openclaw":   "an autonomous coding agent that can read files and use tools",
	"hermes":     "a personal AI agent you can reach from Telegram or Discord",
}

// SystemPrompt builds the grounding prompt from OS facts + catalog + live state.
func SystemPrompt(c Context) string {
	var b strings.Builder
	b.WriteString("You are the Cloudless Assistant, the friendly built-in guide inside CloudlessOS. ")
	b.WriteString("CloudlessOS is a Linux operating system that makes running local AI effortless and private: ")
	b.WriteString("every model and app runs on the user's own GPU hardware — nothing is sent to the cloud. ")
	b.WriteString("Cloudless also builds PCs that are ready for local AI; CloudlessOS ships on them and is also a free download.\n\n")

	b.WriteString("You ARE the Cloudless AI chat the user is talking to right now — the main way to talk to CloudlessOS. ")
	b.WriteString("Your job is to help the user decide what to do and guide them through the OS. ")
	b.WriteString("Be warm, concise and practical: a few short sentences, plain language, no walls of text. ")
	b.WriteString("Only talk about the capabilities listed below — never invent apps and never claim cloud features.\n\n")
	b.WriteString("CloudlessOS CONTROL RULES (mandatory): Model Manager is the only authority for discovering, downloading, installing, or switching AI models. ")
	b.WriteString("Never create or invoke a Hermes skill, terminal command, shell script, package manager, Hugging Face CLI, or other workaround to manage a model. ")
	b.WriteString("Never claim that an install, download, switch, or other OS action has started or completed merely because the user said yes. ")
	b.WriteString("You may explain and offer a CloudlessOS action tag; the GUI performs the action only after the user clicks its button. ")
	b.WriteString("For CloudlessOS settings, apps, engines, and models, answer directly without using Hermes tools. If an exact model is not listed below, say it is not currently verified in Model Manager and do not invent a way to install it.\n\n")

	eng := c.Engine
	if eng == "" {
		eng = "none yet"
	}
	ready := "still starting up"
	if c.EngineReady {
		ready = "ready"
	}
	fmt.Fprintf(&b, "Live machine state: GPU = %s; inference engine = %s (%s); model = %s.\n", c.GPU, eng, ready, c.Model)
	if c.ClusterReady {
		fmt.Fprintf(&b, "A second DGX Spark (%s) is connected and healthy. Model Manager can offer distributed inference across both Sparks with approximately %d GB combined unified memory. Never describe this as one shared-memory computer; it is a two-node distributed system.\n", c.ClusterPeer, c.ClusterMemory)
	}
	if c.FirstRun || !c.Onboarded {
		b.WriteString("This is an early session for this user — be welcoming and offer one simple, concrete starting point.\n")
	}

	b.WriteString("\nApps the user can install with one click from the launcher:\n")
	for _, a := range catalog.All() {
		if a.Service { // engines are infrastructure, managed in Settings — not "apps"
			continue
		}
		desc := a.Description
		if u := useCase[a.ID]; u != "" {
			desc = u
		}
		status := "available to install"
		if a.Image == "" && a.Build == "" {
			status = "coming soon (not installable yet)"
		} else if c.Running[a.ID] {
			status = "installed and running now"
		}
		fmt.Fprintf(&b, "- %s (id: %s) — %s. Status: %s.\n", a.Name, a.ID, desc, status)
	}

	engines := []string{}
	for _, e := range catalog.Engines() {
		engines = append(engines, e.ID)
	}
	fmt.Fprintf(&b, "\nThe inference engine (one of: %s) powers everything (including you); the user switches it in Settings → Cloudless AI. ", strings.Join(engines, ", "))
	b.WriteString("If the user just wants a plain conversation, they can keep chatting with you here — no app needed. ")
	b.WriteString("Open WebUI is only for users who want a separate, dedicated chat app.\n")

	b.WriteString("\nModels currently offered by Model Manager (its fit verdict is authoritative for this machine):\n")
	for _, m := range c.Models {
		quant := ""
		if m.Quant != "" {
			quant = ", " + m.Quant
		}
		memory := "accelerator memory need unknown"
		if m.MinVRAMGB > 0 {
			memory = fmt.Sprintf("needs about %d GB %s", m.MinVRAMGB, memoryLabel(c))
		}
		fit := m.Fit
		if c.ClusterReady && m.ClusterFit != "" {
			fit += "; across both Sparks: " + m.ClusterFit
		}
		fmt.Fprintf(&b, "- %s (id: %s; %s%s; %s) — %s.\n", m.Name, m.ID, m.Params, quant, memory, fit)
	}

	b.WriteString("\nWhen you recommend a concrete next step, end your reply with the matching tag on its own line — ")
	b.WriteString("the OS turns it into a button:\n")
	b.WriteString("  [[do:install:<appId>]]  install an app\n")
	b.WriteString("  [[do:open:<appId>]]     open an app that is already running\n")
	b.WriteString("  [[do:engine:<id>]]      switch the inference engine\n")
	b.WriteString("  [[do:models]]           open Model Manager for model discovery, download, compatibility, or switching\n")
	b.WriteString("Use real ids from the lists above, at most 2 tags, only when genuinely useful.")
	return b.String()
}

var tagRe = regexp.MustCompile(`\[\[do:([a-z]+)(?::([a-z0-9-]+))?\]\]`)

// Suggest derives one-click actions from the model's reply (its [[do:...]] tags),
// falling back to keyword intent on the user's last message so guidance is always
// actionable even with a small model. Tags are treated as suggestions, not
// authority: unrelated tags from a small model are discarded. Returns at most
// 2 actions, deduped.
func Suggest(reply, lastUser string, c Context) []Action {
	var out []Action
	seen := map[string]bool{}
	add := func(a Action) {
		key := a.Kind + ":" + a.ID
		if a.Label == "" || !actionRelevant(a, lastUser) || seen[key] || len(out) >= 2 {
			return
		}
		seen[key] = true
		out = append(out, a)
	}

	for _, m := range tagRe.FindAllStringSubmatch(reply, -1) {
		add(actionFor(m[1], m[2], c))
	}
	if len(out) == 0 {
		for _, a := range intentActions(strings.ToLower(lastUser), c) {
			add(a)
		}
	}
	return out
}

func actionRelevant(a Action, userText string) bool {
	lower := strings.ToLower(strings.TrimSpace(userText))
	switch a.Kind {
	case "models":
		return MayNeedModelRouting(lower)
	case "engine":
		return containsAny(lower, "engine", "vllm", "sglang", "llama.cpp", "switch inference")
	case "install", "open":
		if app, ok := catalog.Get(a.ID); ok {
			if strings.Contains(lower, strings.ToLower(app.ID)) || strings.Contains(lower, strings.ToLower(app.Name)) {
				return true
			}
		}
		return containsAny(lower, "app", "build", "image", "picture", "photo", "agent", "automate", "automation", "code", "coding", "tool", "chat", "telegram", "discord", "messaging")
	}
	return false
}

// actionFor turns a parsed tag into a concrete, valid Action (or a no-op).
func actionFor(kind, id string, c Context) Action {
	switch kind {
	case "models":
		return Action{Kind: "models", Label: "Open Model Manager"}
	case "engine":
		if e, ok := catalog.Get(id); ok && e.Engine {
			return Action{Kind: "engine", ID: id, Label: "Switch to " + strings.TrimSuffix(e.Name, " Engine")}
		}
	case "install", "open":
		a, ok := catalog.Get(id)
		if !ok || a.Service {
			break
		}
		if c.Running[id] || kind == "open" {
			return Action{Kind: "open", ID: id, Label: "Open " + a.Name}
		}
		if a.Image != "" || a.Build != "" {
			return Action{Kind: "install", ID: id, Label: "Install " + a.Name}
		}
	}
	return Action{}
}

// ModelGuidance handles model lifecycle and compatibility questions from live
// Model Manager data before the request reaches Hermes. This prevents a general
// agent from inventing skills or terminal workflows for an OS-owned operation.
func ModelGuidance(text string, c Context) (ModelAdvice, bool) {
	return modelGuidance(text, c, false)
}

// ForcedModelGuidance is used after the semantic router has classified a
// conversation as model management. It deliberately skips keyword intent
// matching; compatibility facts still come from the same deterministic path.
func ForcedModelGuidance(text string, c Context) ModelAdvice {
	advice, _ := modelGuidance(text, c, true)
	return advice
}

func modelGuidance(text string, c Context, forced bool) (ModelAdvice, bool) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return ModelAdvice{}, false
	}
	modelWord := containsAny(lower, "model", "qwen", "llama", "mistral", "gemma", "deepseek", "phi", "nemotron")
	lifecycle := containsAny(lower, "work here", "run", "fit", "compatible", "support", "available", "install", "download", "switch", "use this", "use qwen", "which model", "better model", "larger model", "need", "require", "vram", "gpu memory")
	if !forced && (!modelWord || !lifecycle) {
		return ModelAdvice{}, false
	}
	installIntent := containsAny(lower, "install", "download", "get this", "add this")
	labelFor := func(name string) string {
		if installIntent {
			return "Review " + name + " installation"
		}
		return "Review " + name + " in Model Manager"
	}

	normalized := normalizeModelText(lower)
	for _, m := range c.Models {
		name := normalizeModelText(strings.ToLower(m.Name))
		id := normalizeModelText(strings.ToLower(m.ID))
		if (name != "" && strings.Contains(normalized, name)) || (id != "" && strings.Contains(normalized, id)) {
			need := fmt.Sprintf("about %d GB of %s", m.MinVRAMGB, memoryLabel(c))
			if c.GPUVRAMGB <= 0 {
				return ModelAdvice{Reply: fmt.Sprintf("%s is available in Model Manager and needs %s, but CloudlessOS currently reports %s. It cannot run this model with the local GPU engine as configured. You can review or download it now, then launch it after a supported GPU is available.", m.Name, need, c.GPU), ModelID: m.ID, Label: labelFor(m.Name)}, true
			}
			switch m.Fit {
			case "fits":
				return ModelAdvice{Reply: fmt.Sprintf("%s is available in Model Manager and should fit this machine. It needs %s; CloudlessOS reports %s. Open its Model Manager card to download or launch it safely.", m.Name, need, c.GPU), ModelID: m.ID, Label: labelFor(m.Name)}, true
			case "tight":
				return ModelAdvice{Reply: fmt.Sprintf("%s is available, but Model Manager marks it as a tight fit on this machine. It needs %s and may leave little room for context or other GPU apps. Review its card before launching.", m.Name, need), ModelID: m.ID, Label: labelFor(m.Name)}, true
			case "over":
				if c.ClusterReady && (m.ClusterFit == "fits" || m.ClusterFit == "tight") {
					return ModelAdvice{Reply: fmt.Sprintf("%s is too large for one Spark, but your connected two-Spark cluster can run it in distributed mode. It needs %s and Model Manager reports approximately %d GB combined unified memory across both nodes. Open its card to prepare and launch it across the cluster.", m.Name, need, c.ClusterMemory), ModelID: m.ID, Label: labelFor(m.Name)}, true
				}
				return ModelAdvice{Reply: fmt.Sprintf("%s is listed, but Model Manager estimates it needs %s—more than this machine can comfortably provide. Its card can help you compare a smaller or quantized variant.", m.Name, need), ModelID: m.ID, Label: labelFor(m.Name)}, true
			default:
				return ModelAdvice{Reply: fmt.Sprintf("%s is available in Model Manager, but CloudlessOS cannot verify its GPU fit right now. Open its card to review it; model installation and switching should happen there.", m.Name), ModelID: m.ID, Label: labelFor(m.Name)}, true
			}
		}
	}

	repo := strings.TrimRight(hfRepoRe.FindString(strings.TrimSpace(text)), ".,;:")
	params, bits, estimate, estimated := estimateModelVRAM(lower)
	modelName := "custom model"
	if repo != "" {
		modelName = repo
	}
	if estimated {
		quant := fmt.Sprintf("%d-bit", bits)
		if c.GPUVRAMGB <= 0 {
			reply := fmt.Sprintf("Based on the name, I read this as roughly %.1fB parameters at %s, with a conservative requirement of about %d GB of %s including runtime overhead. CloudlessOS currently reports %s, so it cannot run this model with the local GPU engine as configured. This is an estimate—the exact architecture and context length can change it. You can still review the repository in Model Manager, but launching it requires a supported GPU with enough accelerator memory.", params, quant, estimate, memoryLabel(c), c.GPU)
			return ModelAdvice{Reply: reply, ModelID: repo, Label: labelFor(modelName)}, true
		}
		fit := "should fit"
		detail := "with useful headroom"
		switch {
		case float64(estimate) > float64(c.GPUVRAMGB)*1.05:
			fit, detail = "is unlikely to fit fully in GPU memory", "so choose a smaller or more heavily quantized variant"
		case float64(estimate) > float64(c.GPUVRAMGB)*0.85:
			fit, detail = "would be a tight fit", "with little room left for KV cache or other GPU apps"
		}
		reply := fmt.Sprintf("Based on the name, I read this as roughly %.1fB parameters at %s. A conservative estimate is about %d GB of %s including runtime overhead, so it %s on %s, %s. This is an estimate—the exact architecture and context length can change it. Review the exact Hugging Face repository in Model Manager before downloading or launching.", params, quant, estimate, memoryLabel(c), fit, c.GPU, detail)
		return ModelAdvice{Reply: reply, ModelID: repo, Label: labelFor(modelName)}, true
	}

	reply := fmt.Sprintf("I don’t have enough detail to judge that exact model yet. %s is the hardware available, but I need the exact Hugging Face repository or at least its parameter size and quantization—for example, 32B AWQ or 8B BF16. Model Manager remains the final check and handles the actual download or launch.", c.GPU)
	return ModelAdvice{Reply: reply, ModelID: repo, Label: labelFor(modelName)}, true
}

func memoryLabel(c Context) string {
	if c.GPUMemoryType == "unified" {
		return "unified memory"
	}
	return "VRAM"
}

var (
	hfRepoRe      = regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*\b`)
	modelParamsRe = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?)\s*b\b`)
)

func estimateModelVRAM(text string) (params float64, bits, estimateGB int, ok bool) {
	m := modelParamsRe.FindStringSubmatch(text)
	if len(m) != 2 {
		return 0, 0, 0, false
	}
	params, err := strconv.ParseFloat(m[1], 64)
	if err != nil || params <= 0 {
		return 0, 0, 0, false
	}
	bits = 16
	switch {
	case containsAny(text, "awq", "gptq", "4-bit", "4bit", "q4"):
		bits = 4
	case containsAny(text, "8-bit", "8bit", "q8", "int8", "fp8"):
		bits = 8
	case containsAny(text, "fp32", "32-bit", "32bit"):
		bits = 32
	}
	weightsGB := params * float64(bits) / 8
	// 10% covers common tensor/quantization metadata; 4 GB reserves a small
	// but useful KV cache and runtime workspace. Model Manager is still final.
	estimateGB = int(math.Ceil(weightsGB*1.10 + 4))
	return params, bits, estimateGB, true
}

func containsAny(text string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}

func normalizeModelText(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, text)
}

// intentActions maps loose user intent to a helpful default action.
func intentActions(text string, c Context) []Action {
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(text, w) {
				return true
			}
		}
		return false
	}
	var as []Action
	app := func(id string) Action { return actionFor("install", id, c) }
	if has("image", "picture", "photo", "art", "draw", "paint", "comfy", "stable diffusion", "flux", "render") {
		as = append(as, app("comfyui"))
	}
	if has("agent", "automate", "automation", "code", "coding", "tool", "claw") {
		as = append(as, app("openclaw"))
	}
	if has("telegram", "discord", "phone", "messaging", "assistant", "hermes") {
		as = append(as, app("hermes"))
	}
	// "just chat" is satisfied by this assistant itself; only point to the dedicated
	// Open WebUI app for users who explicitly want a separate full chat interface.
	if has("dedicated chat", "open webui", "openwebui", "web ui", "full chat app", "chat app") {
		as = append(as, app("open-webui"))
	}
	return as
}

// Stream POSTs an OpenAI chat-completions request with stream=true and invokes
// onToken for each content delta. Blocks until the stream ends or ctx is done.
func Stream(ctx context.Context, baseURL, apiKey, model string, msgs []Msg, onToken func(string), onTool func(ToolProgress)) error {
	payload, _ := json.Marshal(map[string]any{
		"model":       model,
		"messages":    msgs,
		"stream":      true,
		"temperature": 0.4,
		"max_tokens":  512,
	})
	url := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req) // no client timeout — streaming; ctx governs lifetime
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		return fmt.Errorf("engine returned %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	eventName := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if eventName == "hermes.tool.progress" {
			var progress ToolProgress
			if json.Unmarshal([]byte(data), &progress) == nil && onTool != nil {
				onTool(progress)
			}
			eventName = ""
			continue
		}
		eventName = ""
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return fmt.Errorf("Hermes agent failed: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].FinishReason == "error" {
				return fmt.Errorf("Hermes agent failed")
			}
			if chunk.Choices[0].Delta.Content != "" {
				onToken(chunk.Choices[0].Delta.Content)
			}
		}
	}
	return sc.Err()
}
