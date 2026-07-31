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
	"net/http"
	"regexp"
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
	ClusterReady  bool            // healthy multi-Spark fabric is available for distributed inference
	ClusterPeer   string          // legacy display name of the first connected worker
	ClusterNodes  int             // coordinator plus all workers
	ClusterMemory int             // approximate aggregate accelerator memory across the cluster
	Models        []ModelOption   // curated choices and fit verdicts shown by Model Manager
}

// ModelOption is the authoritative subset of Model Manager data needed for
// compatibility answers. It prevents the assistant from guessing model fit.
type ModelOption struct {
	ID                       string
	Name                     string
	Params                   string
	Quant                    string
	Fit                      string // fits | tight | over | unknown
	ClusterFit               string // distributed fit across a healthy Spark cluster
	RequiredPerNodeGB        float64
	ClusterRequiredPerNodeGB float64
	Evidence                 string // measured | reviewed-estimate | missing
	ClusterEvidence          string
	FitReason                string
	ClusterFitReason         string
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
	b.WriteString("If a tool is unavailable, denied, or fails, say so once and do not retry it in a loop or claim the task is progressing. ")
	b.WriteString("If the user asks to stop or abort, stop proposing or attempting the action and direct them to the visible CloudlessOS Abort control when one exists. ")
	b.WriteString("Never treat conversational consent as permission to execute an OS action; wait for the user to click the relevant CloudlessOS control. ")
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
		nodes := max(2, c.ClusterNodes)
		fmt.Fprintf(&b, "%d DGX Sparks are connected and healthy. Model Manager can offer distributed inference with approximately %d GB aggregate unified memory. Never describe this as one shared-memory computer; it is a %d-node distributed system.\n", nodes, c.ClusterMemory, nodes)
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
		memory := "no matching reviewed runtime profile"
		if m.RequiredPerNodeGB > 0 {
			prefix := "about "
			if m.Evidence == "measured" {
				prefix = ""
			}
			memory = fmt.Sprintf("profile requires %s%.1f GB %s per node", prefix, m.RequiredPerNodeGB, memoryLabel(c))
		}
		fit := m.Fit
		if c.ClusterReady && m.ClusterFit != "" {
			fit += fmt.Sprintf("; across %d Sparks: %s", max(2, c.ClusterNodes), m.ClusterFit)
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
			need := "an unknown amount of " + memoryLabel(c)
			if m.RequiredPerNodeGB > 0 {
				prefix := "about "
				if m.Evidence == "measured" {
					prefix = ""
				}
				need = fmt.Sprintf("%s%.1f GB of %s per node", prefix, m.RequiredPerNodeGB, memoryLabel(c))
			}
			if c.GPUVRAMGB <= 0 {
				return ModelAdvice{Reply: fmt.Sprintf("%s is available in Model Manager, but CloudlessOS currently reports %s and cannot validate a local launch. You can review or download it now; Model Manager will only claim compatibility when both hardware and an exact runtime profile are available.", m.Name, c.GPU), ModelID: m.ID, Label: labelFor(m.Name)}, true
			}
			switch m.Fit {
			case "fits":
				return ModelAdvice{Reply: fmt.Sprintf("%s is available in Model Manager and should fit this machine. It needs %s; CloudlessOS reports %s. Open its Model Manager card to download or launch it safely.", m.Name, need, c.GPU), ModelID: m.ID, Label: labelFor(m.Name)}, true
			case "tight":
				return ModelAdvice{Reply: fmt.Sprintf("%s is available, but Model Manager marks it as a tight fit on this machine. It needs %s and may leave little room for context or other GPU apps. Review its card before launching.", m.Name, need), ModelID: m.ID, Label: labelFor(m.Name)}, true
			case "over":
				if c.ClusterReady && (m.ClusterFit == "fits" || m.ClusterFit == "tight") {
					nodes := max(2, c.ClusterNodes)
					return ModelAdvice{Reply: fmt.Sprintf("%s is too large for one Spark, but your connected %d-Spark cluster can run it in distributed mode. It needs %s and Model Manager reports approximately %d GB aggregate unified memory. Open its card to prepare and launch it across the cluster.", m.Name, nodes, need, c.ClusterMemory), ModelID: m.ID, Label: labelFor(m.Name)}, true
				}
				return ModelAdvice{Reply: fmt.Sprintf("%s is listed, but its matching runtime profile requires %s—more than this machine can comfortably provide. Its card can help you compare a smaller or quantized variant.", m.Name, need), ModelID: m.ID, Label: labelFor(m.Name)}, true
			default:
				reason := strings.TrimSpace(m.FitReason)
				if reason == "" {
					reason = "no reviewed profile matches this exact model, engine, architecture, and topology"
				}
				return ModelAdvice{Reply: fmt.Sprintf("%s is available in Model Manager, but CloudlessOS does not have a verified fit for this setup: %s. Parameter count and quantization names are not enough to predict loaded memory. Open its card to review it; model installation and switching should happen there.", m.Name, reason), ModelID: m.ID, Label: labelFor(m.Name)}, true
			}
		}
	}

	repo := strings.TrimRight(hfRepoRe.FindString(strings.TrimSpace(text)), ".,;:")
	modelName := "custom model"
	if repo != "" {
		modelName = repo
	}
	reply := fmt.Sprintf("I can’t determine whether that exact model will run from its name, parameter count, quantization label, or repository size alone. %s is the hardware available, but loaded memory also depends on the exact artifact, inference engine, architecture, context, and topology. Open the repository in Model Manager; CloudlessOS will show “Not reviewed” until a matching runtime profile exists, and it handles the actual download or launch.", c.GPU)
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
