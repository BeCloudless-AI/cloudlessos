package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/places"
)

// acceleratorMemory reports the capacity available to GPU workloads. On DGX
// Spark this is system-wide unified memory rather than nvidia-smi VRAM.
func acceleratorMemory(ctx context.Context) (int, string) {
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	return hardware.AcceleratorMemoryGB(c)
}

// totalVRAMGB is retained internally while API clients migrate to memoryType.
func totalVRAMGB(ctx context.Context) int {
	total, _ := acceleratorMemory(ctx)
	return total
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

// modelVolumePath resolves Docker's local Hugging Face cache mount. Installed
// systems run cloudlessd as root, so reading it directly avoids launching a
// throwaway container every time Model Manager opens.
func (s *Server) modelVolumePath(ctx context.Context) string {
	out, err := s.eng.Output(ctx, "volume", "inspect", "cloudless-hf", "--format", "{{.Mountpoint}}")
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(out)
	if stat, err := os.Stat(filepath.Join(path, "hub")); err == nil && stat.IsDir() {
		return path
	}
	return ""
}

func scanModelHub(root string) map[string]bool {
	have := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(root, "hub"))
	if err != nil {
		return have
	}
	for _, entry := range entries {
		rest, ok := strings.CutPrefix(entry.Name(), "models--")
		if !ok || !entry.IsDir() || !modelCacheComplete(filepath.Join(root, "hub", entry.Name())) {
			continue
		}
		if org, name, found := strings.Cut(rest, "--"); found {
			have[org+"/"+name] = true
		}
	}
	return have
}

func modelCacheComplete(repoDir string) bool {
	revision, err := os.ReadFile(filepath.Join(repoDir, "refs", "main"))
	if err != nil || strings.TrimSpace(string(revision)) == "" {
		return false
	}
	snapshot := filepath.Join(repoDir, "snapshots", strings.TrimSpace(string(revision)))
	if stat, err := os.Stat(snapshot); err != nil || !stat.IsDir() {
		return false
	}
	incomplete := false
	_ = filepath.WalkDir(filepath.Join(repoDir, "blobs"), func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".incomplete") {
			incomplete = true
		}
		return nil
	})
	return !incomplete
}

// exposeModelCache gives each completed cache snapshot a normal folder under
// Models. Files are hard-linked to Docker's cache, so there is still only one
// physical copy and the view remains visible outside cloudlessd's systemd mount
// namespace.
func exposeModelCache(cacheRoot string, have map[string]bool) error {
	p, ok := places.Get("models")
	if !ok || cacheRoot == "" {
		return nil
	}
	if err := os.MkdirAll(p.Path, 0o755); err != nil {
		return err
	}
	for repo := range have {
		cacheName := "models--" + strings.ReplaceAll(repo, "/", "--")
		repoRoot := filepath.Join(cacheRoot, "hub", cacheName)
		revisionBytes, err := os.ReadFile(filepath.Join(repoRoot, "refs", "main"))
		if err != nil {
			continue
		}
		revision := strings.TrimSpace(string(revisionBytes))
		source := filepath.Join(repoRoot, "snapshots", revision)
		name := strings.ReplaceAll(repo, "/", "--")
		destination := filepath.Join(p.Path, name)
		if marker, err := os.ReadFile(filepath.Join(destination, ".cloudless-revision")); err == nil && strings.TrimSpace(string(marker)) == revision {
			continue
		}
		if info, err := os.Lstat(destination); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(destination); err != nil {
					return err
				}
			} else if _, markerErr := os.Stat(filepath.Join(destination, ".cloudless-revision")); markerErr == nil {
				if err := os.RemoveAll(destination); err != nil {
					return err
				}
			} else {
				destination += "-Cloudless"
				if _, collisionErr := os.Lstat(destination); collisionErr == nil {
					return fmt.Errorf("Models folder entry already exists: %s", destination)
				}
			}
		}
		temp := filepath.Join(p.Path, ".materializing-"+name)
		_ = os.RemoveAll(temp)
		if err := hardlinkTree(source, temp); err != nil {
			_ = os.RemoveAll(temp)
			return fmt.Errorf("expose %s in Models: %w", repo, err)
		}
		if err := os.WriteFile(filepath.Join(temp, ".cloudless-revision"), []byte(revision+"\n"), 0o644); err != nil {
			_ = os.RemoveAll(temp)
			return err
		}
		if info, err := os.Lstat(destination); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				_ = os.Remove(destination)
			} else if _, markerErr := os.Stat(filepath.Join(destination, ".cloudless-revision")); markerErr == nil {
				_ = os.RemoveAll(destination)
			} else {
				_ = os.RemoveAll(temp)
				return fmt.Errorf("Models folder entry already exists: %s", destination)
			}
		}
		if err := os.Rename(temp, destination); err != nil {
			_ = os.RemoveAll(temp)
			return err
		}
	}
	return nil
}

func hardlinkTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if err := os.Link(resolved, target); err == nil {
			return nil
		}
		// ProtectSystem/ProtectHome place cloudlessd in a private mount
		// namespace. Link through PID 1's namespace so the source cache and
		// desktop Models folder share their real host mount.
		if output, err := exec.Command("nsenter", "-t", "1", "-m", "--", "ln", "--", resolved, target).CombinedOutput(); err != nil {
			return fmt.Errorf("hard-link %s: %w: %s", entry.Name(), err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

// downloadedModels lists model ids present in the shared cache. Results are
// briefly cached so installed models paint immediately in the default view.
func (s *Server) downloadedModels(ctx context.Context) map[string]bool {
	s.modelsMu.Lock()
	if s.modelsHave != nil && time.Since(s.modelsHaveAt) < 3*time.Second {
		out := make(map[string]bool, len(s.modelsHave))
		for id, present := range s.modelsHave {
			out[id] = present
		}
		s.modelsMu.Unlock()
		return out
	}
	s.modelsMu.Unlock()

	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	root := s.modelVolumePath(c)
	have := scanModelHub(root)
	if root == "" {
		out, err := s.eng.Output(c, "run", "--rm", "-v", "cloudless-hf:/c", "busybox",
			"sh", "-c", `for f in /c/hub/models--*/refs/main; do [ -s "$f" ] && basename "$(dirname "$(dirname "$f")")"; done 2>/dev/null`)
		if err == nil {
			for _, line := range strings.Split(out, "\n") {
				rest, ok := strings.CutPrefix(strings.TrimSpace(line), "models--")
				if !ok {
					continue
				}
				if org, name, found := strings.Cut(rest, "--"); found {
					have[org+"/"+name] = true
				}
			}
		}
	} else {
		if err := exposeModelCache(root, have); err != nil {
			log.Printf("models: expose cache in Models folder: %v", err)
		}
	}
	cached := make(map[string]bool, len(have))
	for id, present := range have {
		cached[id] = present
	}
	s.modelsMu.Lock()
	s.modelsHave, s.modelsHaveAt = cached, time.Now()
	s.modelsMu.Unlock()
	return have
}

func (s *Server) invalidateDownloadedModels() {
	s.modelsMu.Lock()
	s.modelsHaveAt = time.Time{}
	s.modelsMu.Unlock()
}

type modelDownloadView struct {
	JobID      string `json:"jobId"`
	ModelID    string `json:"modelId"`
	Phase      string `json:"phase"`
	Message    string `json:"message"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
	Done       bool   `json:"done"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) activeModelDownloads() []modelDownloadView {
	out := []modelDownloadView{}
	for _, snapshot := range s.jobs.List("model-dl:") {
		if snapshot.Done {
			continue
		}
		out = append(out, modelDownloadView{
			JobID: snapshot.ID, ModelID: strings.TrimPrefix(snapshot.AppID, "model-dl:"),
			Phase: snapshot.Phase, Message: snapshot.Message,
			BytesDone: snapshot.BytesDone, BytesTotal: snapshot.BytesTotal,
			Done: snapshot.Done, Error: snapshot.Error,
		})
	}
	return out
}

func (s *Server) modelDownloads(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"downloads": s.activeModelDownloads()})
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
	gpuGB, memoryType := acceleratorMemory(r.Context())
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
		"memoryType": memoryType,
		"current":    current,
		"custom":     !inCatalog,
		"yours":      yours,
		"highlights": picks,
		"downloads":  s.activeModelDownloads(),
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
	body.ID = strings.TrimSpace(body.ID)
	for _, active := range s.activeModelDownloads() {
		if active.ModelID == body.ID {
			writeJSON(w, http.StatusAccepted, map[string]string{"jobId": active.JobID})
			return
		}
	}
	job := s.jobs.Create("model-dl:" + body.ID)
	go s.runModelDownload(job, body.ID)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func huggingFaceModelBytes(ctx context.Context, repo string) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://huggingface.co/api/models/"+repo+"?blobs=true", nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var info struct {
		Siblings []struct {
			Size int64 `json:"size"`
		} `json:"siblings"`
	}
	if json.NewDecoder(resp.Body).Decode(&info) != nil {
		return 0
	}
	var total int64
	for _, file := range info.Siblings {
		total += file.Size
	}
	return total
}

func directoryBytes(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, statErr := d.Info(); statErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func (s *Server) modelRepoBytes(ctx context.Context, repo, root string) int64 {
	cacheName := "models--" + strings.ReplaceAll(repo, "/", "--")
	if root != "" {
		return directoryBytes(filepath.Join(root, "hub", cacheName))
	}
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := s.eng.Output(c, "run", "--rm", "-v", "cloudless-hf:/c", "busybox",
		"du", "-sb", filepath.Join("/c/hub", cacheName))
	if err != nil {
		return 0
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0
	}
	value, _ := strconv.ParseInt(fields[0], 10, 64)
	return value
}

func formatDownloadProgress(done, total int64) string {
	const gib = 1024 * 1024 * 1024
	if total > 0 {
		pct := float64(done) / float64(total) * 100
		if pct > 99 {
			pct = 99
		}
		return fmt.Sprintf("%.1f / %.1f GB · %.0f%%", float64(done)/gib, float64(total)/gib, pct)
	}
	return fmt.Sprintf("%.1f GB downloaded", float64(done)/gib)
}

// runModelDownload fetches a repo into the shared cache while polling its
// on-disk byte count for real progress.
func (s *Server) runModelDownload(job *jobs.Job, repo string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	vllm, _ := catalog.Get("vllm")
	img := s.imageFor(ctx, vllm)
	metadataCtx, metadataCancel := context.WithTimeout(ctx, 12*time.Second)
	total := huggingFaceModelBytes(metadataCtx, repo)
	metadataCancel()
	root := s.modelVolumePath(ctx)
	job.ProgressBytes("downloading", "Preparing "+repo+"…", s.modelRepoBytes(ctx, repo, root), total)
	py := "import os; from huggingface_hub import snapshot_download; snapshot_download(os.environ['CLOUDLESS_MODEL_ID'])"
	result := make(chan error, 1)
	go func() {
		_, err := s.eng.Output(ctx, "run", "--rm", "--entrypoint", "python3",
			"-e", "CLOUDLESS_MODEL_ID="+repo,
			"-v", "cloudless-hf:/root/.cache/huggingface", img, "-c", py)
		result <- err
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			if err != nil {
				job.Fail(err)
				return
			}
			s.invalidateDownloadedModels()
			have := s.downloadedModels(context.Background())
			if root != "" {
				if err := exposeModelCache(root, have); err != nil {
					job.Fail(err)
					return
				}
			}
			done := s.modelRepoBytes(ctx, repo, root)
			if total > 0 {
				done = total
			}
			job.ProgressBytes("finalizing", "Saved in Models", done, total)
			job.Succeed("")
			return
		case <-ticker.C:
			done := s.modelRepoBytes(ctx, repo, root)
			job.ProgressBytes("downloading", "Downloading "+repo+" · "+formatDownloadProgress(done, total), done, total)
		case <-ctx.Done():
			job.Fail(ctx.Err())
			return
		}
	}
}
