package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const modelControlClassifierPrompt = `Classify only the latest user request, using earlier turns solely as context.
Return exactly MODEL_CONTROL when the user is asking to discover, compare, select, install, download, remove, switch, run, size, or check hardware/VRAM compatibility for an AI model. Follow-up questions about such a task are MODEL_CONTROL too.
Return exactly OTHER for ordinary conversation, app management, image generation, coding tasks, or general conceptual questions about AI models that do not ask for a CloudlessOS model-management or hardware-fit decision.
Do not answer the request and do not add punctuation.`

// MayNeedModelRouting is a cheap precision gate in front of the local semantic
// classifier. Small local models are useful for recognizing unfamiliar model
// names, but should never get a chance to turn an unrelated chat request into
// a Model Manager action. Explicit model vocabulary is always eligible. An
// unfamiliar product name is eligible only when the user also asks a concrete
// install, switch, run, fit, or machine-compatibility question.
func MayNeedModelRouting(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	explicit := containsAny(lower,
		"model", "llm", "language model", "hugging face", "huggingface", "vram", "gpu memory",
		"parameter", "quantization", "quantized", "awq", "gptq", "gguf", "bf16", "fp16", "fp8",
		"qwen", "llama", "mistral", "gemma", "deepseek", "phi", "nemotron")
	if explicit || modelParamsRe.MatchString(lower) || hfRepoRe.MatchString(lower) {
		return true
	}
	return containsAny(lower,
		"run on this", "run here", "work on this", "work here", "fit on this", "fit here",
		"usable on this", "compatible with this", "install ", "download ", "switch to ")
}

// SemanticModelControl uses the active local inference engine only as an intent
// classifier. It is never trusted for model facts: a positive result is handed
// to ForcedModelGuidance, which uses only Model Manager runtime-fit evidence.
func SemanticModelControl(ctx context.Context, baseURL, model string, msgs []Msg) (bool, error) {
	transcript := recentTranscript(msgs, 8)
	if transcript == "" {
		return false, nil
	}
	payload, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []Msg{
			{Role: "system", Content: modelControlClassifierPrompt},
			{Role: "user", Content: transcript},
		},
		"stream":      false,
		"temperature": 0,
		"max_tokens":  8,
	})
	c, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, errors.New("model intent classifier unavailable")
	}
	var out struct {
		Choices []struct {
			Message Msg `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Choices) == 0 {
		return false, errors.New("invalid model intent classifier response")
	}
	answer := strings.TrimSpace(strings.ToUpper(out.Choices[0].Message.Content))
	switch answer {
	case "MODEL_CONTROL":
		return true, nil
	case "OTHER":
		return false, nil
	default:
		return false, errors.New("ambiguous model intent classifier response")
	}
}

// RecentUserText carries concrete model identifiers across short follow-ups
// without trusting or re-parsing factual claims made by an assistant response.
func RecentUserText(msgs []Msg, max int) string {
	if max <= 0 {
		return ""
	}
	users := make([]string, 0, max)
	for i := len(msgs) - 1; i >= 0 && len(users) < max; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Content) != "" {
			users = append(users, strings.TrimSpace(msgs[i].Content))
		}
	}
	if len(users) == 0 {
		return ""
	}
	if len(users) == 1 || !isShortFollowup(users[0]) {
		return users[0]
	}
	// The latest user message is first in users. Put the concrete earlier
	// request before its follow-up so repository and size parsing stay natural.
	return users[1] + "\n" + users[0]
}

func isShortFollowup(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if len(strings.Fields(lower)) > 8 {
		return false
	}
	return containsAny(lower, "are you sure", "really", "what about", "how about", "can it", "will it", "does it", "would it", "install it", "download it", "switch to it", "do it", "and this", "and that")
}

func recentTranscript(msgs []Msg, max int) string {
	if len(msgs) > max {
		msgs = msgs[len(msgs)-max:]
	}
	var b strings.Builder
	for _, m := range msgs {
		role := strings.ToUpper(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if content == "" || (role != "USER" && role != "ASSISTANT") {
			continue
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(content)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}
