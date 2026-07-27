package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const virtualKeyboardAction = "virtual-keyboard"

// systemInputKey emits a real X11 key into the currently focused control. It is
// used only for cross-origin embedded apps: browser security intentionally
// prevents the Cloudless page from editing an input inside their iframe.
func (s *Server) systemInputKey(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != virtualKeyboardAction {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "virtual keyboard confirmation header required"})
		return
	}
	var body struct {
		Action string `json:"action"`
		Value  string `json:"value"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid virtual key"})
		return
	}
	body.Action = strings.TrimSpace(body.Action)
	if err := validateVirtualKey(body.Action, body.Value); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	emitter := s.virtualKey
	if emitter == nil {
		emitter = emitSystemVirtualKey
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	s.virtualKeyMu.Lock()
	defer s.virtualKeyMu.Unlock()
	if err := emitter(ctx, body.Action, body.Value); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not type into the embedded app"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func validateVirtualKey(action, value string) error {
	switch action {
	case "text":
		if len(value) != 1 || value[0] < 0x20 || value[0] > 0x7e {
			return errors.New("virtual keyboard text must be one printable character")
		}
	case "backspace", "left", "right", "enter", "shift-enter":
		if value != "" {
			return errors.New("virtual keyboard action does not accept text")
		}
	default:
		return errors.New("unsupported virtual keyboard action")
	}
	return nil
}

func emitSystemVirtualKey(ctx context.Context, action, value string) error {
	args := []string{"key", "--clearmodifiers"}
	if action == "text" {
		args = []string{"type", "--clearmodifiers", "--delay", "0", value}
	} else {
		key := map[string]string{
			"backspace":   "BackSpace",
			"left":        "Left",
			"right":       "Right",
			"enter":       "Return",
			"shift-enter": "shift+Return",
		}[action]
		args = append(args, key)
	}
	cmd := exec.CommandContext(ctx, "xdotool", args...)
	display := strings.TrimSpace(os.Getenv("CLOUDLESS_DISPLAY"))
	if display == "" {
		display = ":0"
	}
	xauthority := strings.TrimSpace(os.Getenv("CLOUDLESS_XAUTHORITY"))
	if xauthority == "" {
		xauthority = "/home/cloudless/.Xauthority"
	}
	cmd.Env = append(os.Environ(), "DISPLAY="+display, "XAUTHORITY="+xauthority)
	return cmd.Run()
}
