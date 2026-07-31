package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/customengine"
	"github.com/cloudless/orchestrator/internal/state"
)

var (
	customEngineImagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)
	customEngineSlugPattern  = regexp.MustCompile(`[^a-z0-9]+`)
)

func customEngineID(name string) string {
	slug := strings.Trim(customEngineSlugPattern.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" {
		slug = "engine"
	}
	if len(slug) > 36 {
		slug = strings.Trim(slug[:36], "-")
	}
	var suffix [3]byte
	_, _ = rand.Read(suffix[:])
	return customengine.Prefix + slug + "-" + hex.EncodeToString(suffix[:])
}

func (s *Server) customEngineCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Image string `json:"image"`
		Base  string `json:"base"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	body.Name, body.Image, body.Base = strings.TrimSpace(body.Name), strings.TrimSpace(body.Image), strings.TrimSpace(body.Base)
	if len(body.Name) < 2 || len(body.Name) > 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be between 2 and 64 characters"})
		return
	}
	if !customEngineImagePattern.MatchString(body.Image) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enter a valid local Docker image tag"})
		return
	}
	if body.Base != "vllm" && body.Base != "sglang" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "custom builds must use the vLLM or SGLang runtime contract"})
		return
	}
	ctx, cancel := contextWithShortTimeout(r)
	defer cancel()
	inspection, err := s.eng.InspectImage(ctx, body.Image)
	if err != nil || !strings.HasPrefix(inspection.ID, "sha256:") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Docker cannot find that image locally. Build or tag it first, then try again."})
		return
	}
	imageID, architecture := inspection.ID, inspection.Architecture
	if architecture != runtime.GOARCH {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": fmt.Sprintf("that image is built for %s, but this CloudlessOS host is %s", architecture, runtime.GOARCH)})
		return
	}
	commandMode := ""
	if body.Base == "vllm" {
		lower := strings.ToLower(strings.Join(inspection.EntryPoint, " "))
		if strings.Contains(lower, "vllm") && strings.Contains(lower, "serve") {
			commandMode = "vllm-entrypoint"
		}
	}
	def := state.CustomEngine{
		ID: customEngineID(body.Name), Name: body.Name, Image: body.Image,
		ResolvedImage: imageID, ImageDigest: imageID, Architecture: architecture,
		Base: body.Base, CommandMode: commandMode,
		ProfileVersion: 1, ContractVersion: "cloudless-openai-v1",
		ValidationStatus: "registered", Created: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.state.UpsertCustomEngine(def); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"engine": def, "inspection": strings.TrimSpace(inspection.ID + " " + inspection.Architecture),
	})
}

func contextWithShortTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 8*time.Second)
}

func (s *Server) customEngineDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !customengine.IsCustom(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown custom engine"})
		return
	}
	current := s.state.Get()
	if current.Engine == id {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "switch back to a managed engine before removing this registration"})
		return
	}
	if _, ok := customengine.Get(s.state, id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown custom engine"})
		return
	}
	ctx, cancel := contextWithShortTimeout(r)
	_ = s.eng.Remove(ctx, "cloudless-"+id)
	cancel()
	if err := s.state.DeleteCustomEngine(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("remove registration: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}
