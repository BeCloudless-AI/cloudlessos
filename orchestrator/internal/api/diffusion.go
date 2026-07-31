package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/diffusion"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
)

const comfyVolume = "cloudless-comfyui"

// diffusionFit classifies explicit checkpoint requirements. Diffusion entries
// are file-specific rather than parameter-label estimates; language models use
// the stricter runtime-profile matcher in internal/modelfit.
func diffusionFit(requiredGB, availableGB int) string {
	if availableGB <= 0 || requiredGB <= 0 {
		return "unknown"
	}
	switch {
	case float64(requiredGB) <= float64(availableGB)*0.85:
		return "fits"
	case requiredGB <= availableGB:
		return "tight"
	default:
		return "over"
	}
}

// downloadedDiffusion lists model filenames already present in the ComfyUI volume.
// Best-effort (busybox is auto-pulled). Returns the set of base filenames.
func (s *Server) downloadedDiffusion(ctx context.Context) map[string]bool {
	c, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	out, err := s.eng.RunTransient(c, engine.RunSpec{
		Image:   "busybox",
		Volumes: map[string]string{comfyVolume: "/c"},
		Args:    []string{"sh", "-c", "find /c/ComfyUI/models -type f \\( -name '*.safetensors' -o -name '*.ckpt' -o -name '*.gguf' \\) 2>/dev/null"},
	})
	have := map[string]bool{}
	if err != nil {
		return have
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			have[path.Base(line)] = true
		}
	}
	return have
}

type diffusionView struct {
	diffusion.Model
	Fit        string `json:"fit"`
	Downloaded bool   `json:"downloaded"`
}

func (s *Server) activeDiffusionDownloads() []modelDownloadView {
	out := []modelDownloadView{}
	for _, snapshot := range s.jobs.List("diffusion:") {
		if snapshot.Done {
			continue
		}
		out = append(out, modelDownloadView{
			JobID: snapshot.ID, ModelID: strings.TrimPrefix(snapshot.AppID, "diffusion:"),
			Phase: snapshot.Phase, Message: snapshot.Message,
			BytesDone: snapshot.BytesDone, BytesTotal: snapshot.BytesTotal,
			Done: snapshot.Done, Error: snapshot.Error,
		})
	}
	return out
}

func (s *Server) diffusionDownloads(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"downloads": s.activeDiffusionDownloads()})
}

// diffusionList returns "Your image models" (downloaded files) + "Cloudless highlights"
// (curated picks not yet downloaded), each with a VRAM-fit verdict.
func (s *Server) diffusionList(w http.ResponseWriter, r *http.Request) {
	gpuGB, memoryType := acceleratorMemory(r.Context())

	highlights := s.mfDiff.Highlights(r.Context())
	if len(highlights) == 0 {
		highlights = diffusion.All()
	}
	have := s.downloadedDiffusion(r.Context())
	byFile := map[string]diffusion.Model{}
	for _, m := range highlights {
		byFile[m.File] = m
	}

	view := func(m diffusion.Model) diffusionView {
		return diffusionView{Model: m, Fit: diffusionFit(m.MinVRAMGB, gpuGB), Downloaded: have[m.File]}
	}

	yours := []diffusionView{}
	for file := range have {
		if m, ok := byFile[file]; ok {
			yours = append(yours, view(m))
		} else {
			yours = append(yours, diffusionView{Model: diffusion.Model{ID: file, Name: file, Type: "checkpoint",
				File: file, Description: "Custom model file."}, Fit: "unknown", Downloaded: true})
		}
	}
	sort.Slice(yours, func(i, j int) bool { return yours[i].Name < yours[j].Name })

	picks := []diffusionView{}
	for _, m := range highlights {
		if !have[m.File] {
			picks = append(picks, view(m))
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"gpuVRAMGB": gpuGB, "memoryType": memoryType, "yours": yours, "highlights": picks,
		"downloads": s.activeDiffusionDownloads(),
	})
}

func (s *Server) diffusionModel(ctx context.Context, id string) (diffusion.Model, bool) {
	for _, h := range s.mfDiff.Highlights(ctx) {
		if h.ID == id {
			return h, true
		}
	}
	return diffusion.Get(id)
}

func safeDiffusionFile(m diffusion.Model) bool {
	if !safeDiffusionFilename(m.File) {
		return false
	}
	cleanDir := path.Clean(m.Dir)
	return cleanDir != "." && cleanDir != ".." && !strings.HasPrefix(cleanDir, "../") && !path.IsAbs(cleanDir)
}

func safeDiffusionFilename(file string) bool {
	if file == "" || path.Base(file) != file || strings.ContainsAny(file, "\x00\r\n") {
		return false
	}
	switch strings.ToLower(path.Ext(file)) {
	case ".safetensors", ".ckpt", ".gguf":
		return true
	default:
		return false
	}
}

// diffusionDownload fetches a curated model file into the ComfyUI volume (async job).
func (s *Server) diffusionDownload(w http.ResponseWriter, r *http.Request) {
	// Prefer the hosted highlight (so the URL/file can be re-curated remotely), else the built-in.
	id := r.PathValue("id")
	m, found := s.diffusionModel(r.Context(), id)
	if !found || m.URL == "" || !safeDiffusionFile(m) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown model"})
		return
	}
	for _, active := range s.activeDiffusionDownloads() {
		if active.ModelID == m.ID {
			writeJSON(w, http.StatusAccepted, map[string]string{"jobId": active.JobID})
			return
		}
	}
	job := s.jobs.Create("diffusion:" + m.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	s.registerModelJob(job.ID, cancel)
	go s.runDiffusionDownload(ctx, cancel, job, m)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

// runDiffusionDownload curls the model file into the ComfyUI volume's models dir. The
// volume's root is ComfyUI's working dir, so models live under ComfyUI/models/<dir>.
func (s *Server) runDiffusionDownload(ctx context.Context, cancel context.CancelFunc, job *jobs.Job, m diffusion.Model) {
	defer cancel()
	defer s.unregisterModelJob(job.ID)

	dir := "/c/ComfyUI/models/" + m.Dir
	final := dir + "/" + m.File
	partial := final + ".cloudless-part"
	size := "a large file"
	if m.SizeGB > 0 {
		size = fmt.Sprintf("~%.1f GB", m.SizeGB)
	}
	job.Progress("downloading", "Downloading "+m.Name+" ("+size+") — this can take a while…", -1, -1)

	cmd := `mkdir -p "$1" && curl -fL --retry 3 -o "$2" "$3" && mv "$2" "$4"`
	containerName := "cloudless-diffusion-download-" + job.ID
	// --user 0: the volume is root-owned (ComfyUI runs as root), and curlimages/curl
	// defaults to a non-root user that can't write there. curl validates TLS.
	_, err := s.eng.RunTransient(ctx, engine.RunSpec{
		Name:       containerName,
		Image:      "curlimages/curl:latest",
		User:       "0",
		EntryPoint: "sh",
		Volumes:    map[string]string{comfyVolume: "/c"},
		Args:       []string{"-c", cmd, "cloudless-download", dir, partial, m.URL, final},
	})
	if err != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = s.eng.Remove(cleanupCtx, containerName)
		_, _ = s.eng.RunTransient(cleanupCtx, engine.RunSpec{
			Image:   "busybox",
			User:    "0",
			Volumes: map[string]string{comfyVolume: "/c"},
			Args:    []string{"sh", "-c", `rm -f "$1"`, "cloudless-cancel-diffusion", partial},
		})
		if errors.Is(ctx.Err(), context.Canceled) {
			job.Cancel()
			return
		}
		job.Fail(err)
		return
	}
	job.Succeed("")
}

func (s *Server) diffusionUninstall(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	m, found := s.diffusionModel(r.Context(), id)
	if !found && safeDiffusionFilename(id) {
		m, found = diffusion.Model{ID: id, Name: id, File: id}, true
	}
	if !found || !safeDiffusionFilename(m.File) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown model"})
		return
	}
	for _, active := range s.activeDiffusionDownloads() {
		if active.ModelID == id {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cancel this model download before uninstalling it"})
			return
		}
	}
	if !s.downloadedDiffusion(r.Context())[m.File] {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "model is not installed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := s.eng.RunTransient(ctx, engine.RunSpec{
		Image:   "busybox",
		User:    "0",
		Volumes: map[string]string{comfyVolume: "/c"},
		Args:    []string{"sh", "-c", `find /c/ComfyUI/models -type f -name "$1" -delete`, "cloudless-remove-diffusion", m.File},
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not uninstall model"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "uninstalled"})
}
