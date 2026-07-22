package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/models"
)

// totalVRAMGB sums detected GPU memory (vLLM can shard across GPUs with TP).
func totalVRAMGB(ctx context.Context) int {
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	gpus, _ := hardware.GPUs(c)
	total := 0
	for _, g := range gpus {
		total += g.MemTotalMB
	}
	return total / 1024
}

// fitFor classifies a model's VRAM need against available GPU memory.
func fitFor(minGB, gpuGB int) string {
	if gpuGB <= 0 {
		return "unknown"
	}
	switch {
	case float64(minGB) <= float64(gpuGB)*0.85:
		return "fits"
	case float64(minGB) <= float64(gpuGB)*1.05:
		return "tight"
	default:
		return "over"
	}
}

// downloadedModels lists the Hugging Face model ids present in the engine's HF cache
// volume. Best-effort (returns nil on any error); busybox is auto-pulled by `docker run`.
func (s *Server) downloadedModels(ctx context.Context) map[string]bool {
	c, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	out, err := s.eng.Output(c, "run", "--rm", "-v", "cloudless-hf:/c", "busybox",
		"sh", "-c", "ls -1 /c/hub 2>/dev/null")
	if err != nil {
		return map[string]bool{}
	}
	have := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "models--")
		if !ok {
			continue
		}
		if org, name, found := strings.Cut(rest, "--"); found {
			have[org+"/"+name] = true
		}
	}
	return have
}

type modelView struct {
	models.Model
	Fit         string `json:"fit"`                   // fits | tight | over | unknown
	Active      bool   `json:"active"`                // currently the served model
	Downloaded  bool   `json:"downloaded"`            // present in the HF cache
	Recommended bool   `json:"recommended,omitempty"` // recommended for the machine's region
	RecommendBy string `json:"recommendBy,omitempty"` // why, e.g. "Recommended in France"
}

// modelsList returns two views: "yours" (models present on disk) and the curated
// "Cloudless highlights" (from the hosted models manifest, else the built-in list).
// Each carries a VRAM-fit verdict, active and downloaded flags.
func (s *Server) modelsList(w http.ResponseWriter, r *http.Request) {
	gpuGB := totalVRAMGB(r.Context())
	current := s.state.Get().Model
	if current == "" {
		current = catalog.DefaultModel()
	}

	// Region-awareness: a machine in France is recommended French-built (Mistral) models.
	country, countryName, _, _ := s.effectiveCountry()

	// "Cloudless highlights": hosted manifest, falling back to the built-in catalog.
	highlights := s.mfModels.Highlights(r.Context())
	highlights = models.Merge(highlights)
	// Ensure the region's recommended models are present even if the hosted manifest
	// omits them, so e.g. Mistral always appears for a machine in France.
	seen := map[string]bool{}
	for _, m := range highlights {
		seen[m.ID] = true
	}
	for _, m := range models.RegionModels(country) {
		if !seen[m.ID] {
			highlights = append(highlights, m)
			seen[m.ID] = true
		}
	}
	byID := map[string]models.Model{}
	for _, m := range highlights {
		byID[m.ID] = m
	}

	have := s.downloadedModels(r.Context())
	have[current] = true // the served model is, by definition, present

	view := func(m models.Model) modelView {
		mv := modelView{Model: m, Fit: fitFor(m.MinVRAMGB, gpuGB), Active: m.ID == current, Downloaded: have[m.ID]}
		if m.Region != "" && m.Region == country {
			mv.Recommended = true
			mv.RecommendBy = "Recommended in " + countryName
		}
		return mv
	}

	// Your models = everything downloaded, enriched with highlight metadata when known.
	yours := []modelView{}
	for id := range have {
		if m, ok := byID[id]; ok {
			yours = append(yours, view(m))
		} else {
			yours = append(yours, view(models.Model{ID: id, Name: id, Params: "?", Use: "general", Description: "Custom model."}))
		}
	}
	sort.Slice(yours, func(i, j int) bool {
		if yours[i].Active != yours[j].Active {
			return yours[i].Active // active first
		}
		if yours[i].MinVRAMGB != yours[j].MinVRAMGB {
			return yours[i].MinVRAMGB < yours[j].MinVRAMGB
		}
		return yours[i].ID < yours[j].ID
	})
	// Highlights = curated picks the user hasn't downloaded yet (discover more),
	// with region-recommended models (e.g. Mistral in France) floated to the top.
	picks := []modelView{}
	inCatalog := false
	for _, m := range highlights {
		if m.ID == current {
			inCatalog = true
		}
		if !have[m.ID] {
			picks = append(picks, view(m))
		}
	}
	sort.SliceStable(picks, func(i, j int) bool {
		if picks[i].Recommended != picks[j].Recommended {
			return picks[i].Recommended // recommended-for-region first
		}
		return false
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"gpuVRAMGB":  gpuGB,
		"current":    current,
		"custom":     !inCatalog,
		"yours":      yours,
		"highlights": picks,
		"region": map[string]any{
			"country":     country,
			"countryName": countryName,
			"inFrance":    country == "FR",
		},
	})
}

// modelDownload pre-fetches an LLM's weights into the HF cache (without launching),
// so a later launch is instant. Async job.
func (s *Server) modelDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	job := s.jobs.Create("model-dl:" + body.ID)
	go s.runModelDownload(job, strings.TrimSpace(body.ID))
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

// runModelDownload snapshot_downloads a HF repo into the shared cache via the engine image.
func (s *Server) runModelDownload(job *jobs.Job, repo string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	vllm, _ := catalog.Get("vllm")
	img := s.imageFor(ctx, vllm)
	job.Progress("downloading", "Downloading "+repo+" weights — this can take a while…", -1, -1)
	py := "from huggingface_hub import snapshot_download; snapshot_download('" + repo + "')"
	if _, err := s.eng.Output(ctx, "run", "--rm", "--entrypoint", "python3",
		"-v", "cloudless-hf:/root/.cache/huggingface", img, "-c", py); err != nil {
		job.Fail(err)
		return
	}
	job.Succeed("")
}
