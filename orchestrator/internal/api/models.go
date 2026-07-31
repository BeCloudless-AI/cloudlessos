package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/modelfit"
	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/places"
	"github.com/cloudless/orchestrator/internal/platform"
	"github.com/cloudless/orchestrator/internal/state"
)

// acceleratorMemory reports the capacity available to GPU workloads. On DGX
// Spark this is system-wide unified memory rather than nvidia-smi VRAM.
func acceleratorMemory(ctx context.Context) (int, string) {
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	return hardware.AcceleratorMemoryGB(c)
}

func acceleratorFitMemory(ctx context.Context) (totalGB int, memoryType string, availableGB, reservedGB float64) {
	totalGB, memoryType = acceleratorMemory(ctx)
	if memoryType != "unified" {
		return totalGB, memoryType, float64(totalGB), 0
	}
	budget := hardware.AcceleratorMemoryBudget(ctx)
	return totalGB, memoryType,
		float64(budget.AvailableMB) / 1024,
		float64(budget.ReservedMB) / 1024
}

// totalVRAMGB is retained internally while API clients migrate to memoryType.
func totalVRAMGB(ctx context.Context) int {
	total, _ := acceleratorMemory(ctx)
	return total
}

// modelVolumePath resolves Cloudless's host-owned Hugging Face cache. It no
// longer exposes Docker's root-private volume mountpoint to the API daemon.
func (s *Server) modelVolumePath(ctx context.Context) string {
	_ = ctx
	path := modelcache.Root()
	if stat, err := os.Stat(filepath.Join(path, "hub")); err == nil && stat.IsDir() {
		return path
	}
	// An empty, prepared cache is still valid and writable.
	if stat, err := os.Stat(path); err == nil && stat.IsDir() {
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
		if err := os.Chmod(filepath.Join(temp, ".cloudless-revision"), 0o644); err != nil {
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
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			// cloudlessd runs with UMask=0077. The Models view is deliberately
			// read-only to the desktop account, so make that visibility explicit.
			return os.Chmod(target, 0o755)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if err := os.Link(resolved, target); err == nil {
			return nil
		}
		return fmt.Errorf("hard-link %s into the Models view: model cache and desktop storage must share a filesystem", entry.Name())
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
	if root != "" {
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

func modelCacheName(repo string) (string, bool) {
	repo = strings.TrimSpace(repo)
	if repo == "" || strings.Contains(repo, `\`) || strings.ContainsAny(repo, "\x00\r\n") {
		return "", false
	}
	for _, part := range strings.Split(repo, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return "models--" + strings.ReplaceAll(repo, "/", "--"), true
}

func (s *Server) registerModelJob(jobID string, cancel context.CancelFunc) {
	s.modelJobsMu.Lock()
	defer s.modelJobsMu.Unlock()
	if s.modelJobs == nil {
		s.modelJobs = make(map[string]context.CancelFunc)
	}
	s.modelJobs[jobID] = cancel
}

func (s *Server) unregisterModelJob(jobID string) {
	s.modelJobsMu.Lock()
	delete(s.modelJobs, jobID)
	s.modelJobsMu.Unlock()
}

func (s *Server) cancelModelJob(jobID string) bool {
	s.modelJobsMu.Lock()
	cancel, ok := s.modelJobs[jobID]
	s.modelJobsMu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

func modelDownloadContainerName(repo string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(repo)))
	return fmt.Sprintf("cloudless-model-download-%x", sum[:8])
}

func (s *Server) observeModelDownload(job *jobs.Job, repo string) {
	job.Observe(func(update jobs.Update) {
		if update.Done && update.Phase != "error" {
			if err := s.state.RemoveModelDownload(repo); err != nil {
				log.Printf("models: remove durable download %s: %v", repo, err)
			}
			return
		}
		download := state.ModelDownload{
			ModelID: repo, Phase: update.Phase, Message: update.Message,
			BytesDone: update.BytesDone, BytesTotal: update.BytesTotal,
			Started: update.StartedAt, Updated: update.UpdatedAt, Error: update.Error,
		}
		if err := s.state.SetModelDownload(download); err != nil {
			log.Printf("models: persist download %s: %v", repo, err)
		}
	})
}

// startModelDownloadRecovery resumes interrupted cache operations after a
// cloudlessd restart. Hugging Face's incomplete blob chunks remain in the
// shared volume; snapshot_download verifies and continues them.
func (s *Server) startModelDownloadRecovery() {
	for _, download := range s.state.ModelDownloads() {
		if download.ModelID == "" || download.Phase == "error" || download.Phase == "canceled" {
			continue
		}
		job := s.jobs.Create("model-dl:" + download.ModelID)
		s.observeModelDownload(job, download.ModelID)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
		s.registerModelJob(job.ID, cancel)
		go s.runModelDownload(ctx, cancel, job, download.ModelID, s.huggingFaceToken())
	}
}

type modelDownloadView struct {
	JobID      string `json:"jobId"`
	ModelID    string `json:"modelId"`
	Phase      string `json:"phase"`
	Message    string `json:"message"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
	Percent    int    `json:"percent"`
	ETASecs    int64  `json:"etaSeconds,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
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
			Percent: snapshot.Percent, ETASecs: snapshot.ETASecs,
			StartedAt: snapshot.StartedAt, UpdatedAt: snapshot.UpdatedAt,
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
	Fit             string             `json:"fit"` // fits | tight | over | unknown
	FitEstimate     modelfit.Estimate  `json:"fitEstimate"`
	ClusterFit      string             `json:"clusterFit,omitempty"`
	ClusterEstimate *modelfit.Estimate `json:"clusterEstimate,omitempty"`
	Active          bool               `json:"active"`                // currently the served model
	Downloaded      bool               `json:"downloaded"`            // present in the HF cache
	Recommended     bool               `json:"recommended,omitempty"` // recommended for the machine's region
	RecommendBy     string             `json:"recommendBy,omitempty"` // why, e.g. "Recommended in France"
}

// modelsList returns two views: "yours" (models present on disk) and the curated
// "Cloudless highlights" (from the hosted models manifest, else the built-in list).
// Each carries a VRAM-fit verdict, active and downloaded flags.
func (s *Server) modelsList(w http.ResponseWriter, r *http.Request) {
	gpuGB, memoryType, availableGB, reservedGB := acceleratorFitMemory(r.Context())
	cluster := clusterCompute(r.Context(), gpuGB)
	currentState := s.state.Get()
	engineUnloaded := currentState.EngineUnloaded
	current := currentState.Model
	if current == "" {
		current = catalog.DefaultModel()
	}
	activeEngine := currentState.Engine
	if activeEngine == "" {
		activeEngine = catalog.DefaultEngine()
	}
	machinePlatform := platform.Detect()

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
	// User-imported Hugging Face repositories remain in Model Manager across
	// restarts and after uninstall, so they can be downloaded again without
	// repeating the Hub lookup.
	for _, m := range s.importedModels() {
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
		estimate := modelfit.EstimateModel(m, modelfit.Envelope{
			MemoryGB: float64(gpuGB), AvailableGB: availableGB, ReservedGB: reservedGB,
			MemoryType: memoryType, Nodes: 1,
			Engine: activeEngine, Architecture: platform.Architecture(), Platform: machinePlatform,
		})
		mv := modelView{Model: m, Fit: estimate.Status, FitEstimate: estimate,
			Active: !engineUnloaded && m.ID == current, Downloaded: have[m.ID]}
		if cluster.DistributedReady && !m.SingleNodeOnly {
			clusterMemoryGB := float64(cluster.LocalMemoryGB)
			clusterAvailableGB := availableGB
			if cluster.PerNodeCapacityGB > 0 {
				clusterMemoryGB = cluster.PerNodeCapacityGB + reservedGB
				clusterAvailableGB = cluster.PerNodeAvailableGB + reservedGB
			}
			clusterEstimate := modelfit.EstimateModel(m, modelfit.Envelope{
				MemoryGB: clusterMemoryGB, AvailableGB: clusterAvailableGB, ReservedGB: reservedGB,
				MemoryType: memoryType,
				Nodes:      cluster.Nodes, Sharded: true, Engine: activeEngine,
				Architecture: platform.Architecture(), Platform: machinePlatform,
			})
			mv.ClusterFit = clusterEstimate.Status
			mv.ClusterEstimate = &clusterEstimate
		}
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
		"unloaded":   engineUnloaded,
		"executionMode": func() string {
			if s.state.Get().ExecutionMode == "cluster" {
				return "cluster"
			}
			return "local"
		}(),
		"custom":     !inCatalog,
		"yours":      yours,
		"highlights": picks,
		"downloads":  s.activeModelDownloads(),
		"cluster":    cluster,
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
		ID    string `json:"id"`
		Token string `json:"token,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	if _, ok := modelCacheName(body.ID); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model id"})
		return
	}
	for _, active := range s.activeModelDownloads() {
		if active.ModelID == body.ID {
			writeJSON(w, http.StatusAccepted, map[string]string{"jobId": active.JobID})
			return
		}
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		token = s.huggingFaceToken()
	}
	job := s.jobs.Create("model-dl:" + body.ID)
	s.observeModelDownload(job, body.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	s.registerModelJob(job.ID, cancel)
	go s.runModelDownload(ctx, cancel, job, body.ID, token)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func (s *Server) modelDownloadCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JobID string `json:"jobId"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.JobID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jobId is required"})
		return
	}
	job, ok := s.jobs.Get(strings.TrimSpace(body.JobID))
	if !ok || (!strings.HasPrefix(job.AppID, "model-dl:") && !strings.HasPrefix(job.AppID, "diffusion:")) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "model download not found"})
		return
	}
	if job.Snapshot().Done || !s.cancelModelJob(job.ID) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "model download is no longer active"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "canceling"})
}

func (s *Server) removeExposedModel(repo string) error {
	p, ok := places.Get("models")
	if !ok {
		return nil
	}
	name := strings.ReplaceAll(repo, "/", "--")
	for _, candidate := range []string{
		filepath.Join(p.Path, name),
		filepath.Join(p.Path, name+"-Cloudless"),
		filepath.Join(p.Path, ".materializing-"+name),
	} {
		if _, err := os.Stat(filepath.Join(candidate, ".cloudless-revision")); err == nil {
			if err := os.RemoveAll(candidate); err != nil {
				return err
			}
		} else if strings.HasPrefix(filepath.Base(candidate), ".materializing-") {
			_ = os.RemoveAll(candidate)
		}
	}
	return nil
}

func (s *Server) removeModelCache(ctx context.Context, repo string) error {
	cacheName, ok := modelCacheName(repo)
	if !ok {
		return fmt.Errorf("invalid model id")
	}
	root := s.modelVolumePath(ctx)
	if root == "" {
		return errors.New("Cloudless model cache is unavailable")
	}
	if err := os.RemoveAll(filepath.Join(root, "hub", cacheName)); err != nil {
		return err
	}
	if err := s.removeExposedModel(repo); err != nil {
		return err
	}
	s.invalidateDownloadedModels()
	return nil
}

func (s *Server) modelUninstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	body.ID = strings.TrimSpace(body.ID)
	if _, ok := modelCacheName(body.ID); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model id"})
		return
	}
	current := s.state.Get().Model
	if current == "" {
		current = catalog.DefaultModel()
	}
	if body.ID == current {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "switch to another model before uninstalling the model currently in use"})
		return
	}
	for _, active := range s.activeModelDownloads() {
		if active.ModelID == body.ID {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cancel this model download before uninstalling it"})
			return
		}
	}
	if !s.downloadedModels(r.Context())[body.ID] {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "model is not installed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.removeModelCache(ctx, body.ID); err != nil {
		log.Printf("models: uninstall %s: %v", body.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not uninstall model"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "uninstalled"})
}

func huggingFaceModelBytes(ctx context.Context, repo, token string) int64 {
	return huggingFaceModelRevisionBytes(ctx, repo, "", token)
}

func huggingFaceModelRevisionBytes(ctx context.Context, repo, revision, token string) int64 {
	total, _ := huggingFaceModelRevisionSize(ctx, repo, revision, token)
	return total
}

func huggingFaceModelRevisionSize(ctx context.Context, repo, revision, token string) (int64, error) {
	endpoint := "https://huggingface.co/api/models/" + repo
	if revision = strings.TrimSpace(revision); revision != "" {
		endpoint += "/revision/" + url.PathEscape(revision)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?blobs=true", nil)
	if err != nil {
		return 0, err
	}
	if token = strings.TrimSpace(token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Hugging Face model metadata returned HTTP %d", resp.StatusCode)
	}
	var info struct {
		Siblings []struct {
			Size int64 `json:"size"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, err
	}
	var total int64
	for _, file := range info.Siblings {
		total += file.Size
	}
	if total <= 0 {
		return 0, errors.New("Hugging Face model metadata did not include file sizes")
	}
	return total, nil
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
	_ = ctx
	cacheName := "models--" + strings.ReplaceAll(repo, "/", "--")
	if root != "" {
		return directoryBytes(filepath.Join(root, "hub", cacheName))
	}
	return 0
}

func (s *Server) modelRepoIncomplete(ctx context.Context, repo, root string) int {
	_ = ctx
	cacheName := "models--" + strings.ReplaceAll(repo, "/", "--")
	if root != "" {
		count := 0
		_ = filepath.WalkDir(filepath.Join(root, "hub", cacheName, "blobs"), func(_ string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && strings.HasSuffix(entry.Name(), ".incomplete") {
				count++
			}
			return nil
		})
		return count
	}
	return 0
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
func (s *Server) runModelDownload(ctx context.Context, cancel context.CancelFunc, job *jobs.Job, repo, token string) {
	defer cancel()
	defer s.unregisterModelJob(job.ID)
	vllm, _ := catalog.Get("vllm")
	img := s.imageFor(ctx, vllm)
	revision := ""
	if model, ok := models.Get(repo); ok && model.RuntimeImage != "" {
		if model.RuntimeBuild == "" {
			img = model.RuntimeImage
		}
		revision = model.Revision
	}
	metadataCtx, metadataCancel := context.WithTimeout(ctx, 12*time.Second)
	total := huggingFaceModelRevisionBytes(metadataCtx, repo, revision, token)
	metadataCancel()
	root := s.modelVolumePath(ctx)
	job.ProgressBytes("downloading", "Preparing "+repo+"…", s.modelRepoBytes(ctx, repo, root), total)
	py := "import os; from huggingface_hub import snapshot_download; kw={}; revision=os.environ.get('CLOUDLESS_MODEL_REVISION',''); kw.update(revision=revision) if revision else None; snapshot_download(os.environ['CLOUDLESS_MODEL_ID'], **kw)"
	containerName := modelDownloadContainerName(repo)
	// A daemon crash may leave the previous helper running. Stop that stable
	// helper before resuming so two writers never mutate the same cache blobs.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	_ = s.eng.Remove(cleanupCtx, containerName)
	cleanupCancel()
	result := make(chan error, 1)
	go func() {
		env := map[string]string{"CLOUDLESS_MODEL_ID": repo}
		if revision != "" {
			env["CLOUDLESS_MODEL_REVISION"] = revision
		}
		if token != "" {
			env["HF_TOKEN"] = token
		}
		_, err := s.eng.RunTransient(ctx, engine.RunSpec{
			Name:       containerName,
			Image:      img,
			Env:        env,
			Volumes:    map[string]string{modelcache.Root(): "/root/.cache/huggingface"},
			EntryPoint: "python3",
			Args:       []string{"-c", py},
		})
		result <- err
	}()
	stopCanceled := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = s.eng.Remove(cleanupCtx, containerName)
		// Keep .incomplete Hugging Face blobs. A later Download resumes verified
		// chunks instead of forcing the user to transfer the model again.
		job.Cancel()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			if err != nil {
				if errors.Is(ctx.Err(), context.Canceled) {
					stopCanceled()
					return
				}
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
			if errors.Is(ctx.Err(), context.Canceled) {
				stopCanceled()
				return
			}
			job.Fail(ctx.Err())
			return
		}
	}
}
