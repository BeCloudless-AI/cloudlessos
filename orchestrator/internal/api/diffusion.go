package api

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/diffusion"
	"github.com/cloudless/orchestrator/internal/jobs"
)

const comfyVolume = "cloudless-comfyui"

// downloadedDiffusion lists model filenames already present in the ComfyUI volume.
// Best-effort (busybox is auto-pulled). Returns the set of base filenames.
func (s *Server) downloadedDiffusion(ctx context.Context) map[string]bool {
	c, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	out, err := s.eng.Output(c, "run", "--rm", "-v", comfyVolume+":/c", "busybox",
		"sh", "-c", "find /c/ComfyUI/models -type f \\( -name '*.safetensors' -o -name '*.ckpt' -o -name '*.gguf' \\) 2>/dev/null")
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
		return diffusionView{Model: m, Fit: fitFor(m.MinVRAMGB, gpuGB), Downloaded: have[m.File]}
	}

	yours := []diffusionView{}
	for file := range have {
		if m, ok := byFile[file]; ok {
			yours = append(yours, view(m))
		} else {
			yours = append(yours, diffusionView{Model: diffusion.Model{ID: file, Name: file, Type: "checkpoint",
				Description: "Custom model file."}, Fit: "unknown", Downloaded: true})
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
	})
}

// diffusionDownload fetches a curated model file into the ComfyUI volume (async job).
func (s *Server) diffusionDownload(w http.ResponseWriter, r *http.Request) {
	// Prefer the hosted highlight (so the URL/file can be re-curated remotely), else the built-in.
	id := r.PathValue("id")
	var m diffusion.Model
	found := false
	for _, h := range s.mfDiff.Highlights(r.Context()) {
		if h.ID == id {
			m, found = h, true
			break
		}
	}
	if !found {
		m, found = diffusion.Get(id)
	}
	if !found || m.URL == "" || m.File == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown model"})
		return
	}
	job := s.jobs.Create("diffusion:" + m.ID)
	go s.runDiffusionDownload(job, m)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

// runDiffusionDownload curls the model file into the ComfyUI volume's models dir. The
// volume's root is ComfyUI's working dir, so models live under ComfyUI/models/<dir>.
func (s *Server) runDiffusionDownload(job *jobs.Job, m diffusion.Model) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	dir := "/c/ComfyUI/models/" + m.Dir
	size := "a large file"
	if m.SizeGB > 0 {
		size = fmt.Sprintf("~%.1f GB", m.SizeGB)
	}
	job.Progress("downloading", "Downloading "+m.Name+" ("+size+") — this can take a while…", -1, -1)

	cmd := "mkdir -p '" + dir + "' && curl -fL --retry 3 -o '" + dir + "/" + m.File + "' '" + m.URL + "'"
	// --user 0: the volume is root-owned (ComfyUI runs as root), and curlimages/curl
	// defaults to a non-root user that can't write there. curl validates TLS.
	if _, err := s.eng.Output(ctx, "run", "--rm", "--user", "0", "--entrypoint", "sh",
		"-v", comfyVolume+":/c", "curlimages/curl:latest", "-c", cmd); err != nil {
		job.Fail(err)
		return
	}
	job.Succeed("")
}
