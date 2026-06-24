// Package assistant powers the built-in CloudlessOS guide. It grounds a chat in
// live OS state + the app catalog, talks to the local OpenAI-compatible engine,
// and turns guidance into one-click OS actions. Stdlib-only.
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
	Engine      string          // active engine id ("vllm"/"sglang"/""), "" = none yet
	EngineReady bool            // engine is serving
	Model       string          // served model (HF id)
	FirstRun    bool            // no prior state file at startup
	Onboarded   bool            // finished first-run tour
	Running     map[string]bool // app id -> running
	GPU         string          // human summary, e.g. "RTX 5090 (32 GB)"
}

// Action is a one-click follow-up the OS can execute on the user's behalf.
type Action struct {
	Kind  string `json:"kind"`         // install | open | chat | engine
	ID    string `json:"id,omitempty"` // app or engine id
	Label string `json:"label"`        // button text
}

// plain-language "what it's for", beyond the catalog one-liners.
var useCase = map[string]string{
	"comfyui":     "generate and edit images locally (Stable Diffusion / Flux) with node-based workflows",
	"open-webui":  "a separate, optional full-featured chat app (multi-session, file uploads) for the local model",
	"openclaw":    "an autonomous coding agent that can read files and use tools",
	"hermes":      "a personal AI agent you can reach from Telegram or Discord",
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

	eng := c.Engine
	if eng == "" {
		eng = "none yet"
	}
	ready := "still starting up"
	if c.EngineReady {
		ready = "ready"
	}
	fmt.Fprintf(&b, "Live machine state: GPU = %s; inference engine = %s (%s); model = %s.\n", c.GPU, eng, ready, c.Model)
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

	b.WriteString("\nWhen you recommend a concrete next step, end your reply with the matching tag on its own line — ")
	b.WriteString("the OS turns it into a button:\n")
	b.WriteString("  [[do:install:<appId>]]  install an app\n")
	b.WriteString("  [[do:open:<appId>]]     open an app that is already running\n")
	b.WriteString("  [[do:engine:<id>]]      switch the inference engine\n")
	b.WriteString("Use real ids from the lists above, at most 2 tags, only when genuinely useful.")
	return b.String()
}

var tagRe = regexp.MustCompile(`\[\[do:([a-z]+)(?::([a-z0-9-]+))?\]\]`)

// Suggest derives one-click actions from the model's reply (its [[do:...]] tags),
// falling back to keyword intent on the user's last message so guidance is always
// actionable even with a small model. Returns at most 3, deduped.
func Suggest(reply, lastUser string, c Context) []Action {
	var out []Action
	seen := map[string]bool{}
	add := func(a Action) {
		key := a.Kind + ":" + a.ID
		if a.Label == "" || seen[key] || len(out) >= 3 {
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

// actionFor turns a parsed tag into a concrete, valid Action (or a no-op).
func actionFor(kind, id string, c Context) Action {
	switch kind {
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
func Stream(ctx context.Context, baseURL, model string, msgs []Msg, onToken func(string)) error {
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
	req.Header.Set("Authorization", "Bearer cloudless")

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
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			onToken(chunk.Choices[0].Delta.Content)
		}
	}
	return sc.Err()
}
