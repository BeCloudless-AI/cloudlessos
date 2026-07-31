package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/nvidiaupdate"
	"github.com/cloudless/orchestrator/internal/osupdate"
	"github.com/cloudless/orchestrator/internal/privileged"
)

// shortDigest trims "sha256:" and shortens for display.
func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

type appUpdateStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category,omitempty"`
	Installed  bool   `json:"installed"`
	Updatable  bool   `json:"updatable"`
	Build      bool   `json:"build,omitempty"`
	Source     string `json:"source,omitempty"`
	Channel    string `json:"channel,omitempty"`
	Verified   bool   `json:"verified,omitempty"`
	Notes      string `json:"notes,omitempty"`
	HasUpdate  bool   `json:"hasUpdate"`
	Current    string `json:"current,omitempty"`
	Latest     string `json:"latest,omitempty"`
	CheckError string `json:"checkError,omitempty"`
}

type engineUpdateStatus struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Installed       bool   `json:"installed"`
	Active          bool   `json:"active"`
	Selected        bool   `json:"selected"`
	RestartOnUpdate bool   `json:"restartOnUpdate"`
	Source          string `json:"source,omitempty"`
	Channel         string `json:"channel,omitempty"`
	Verified        bool   `json:"verified,omitempty"`
	Notes           string `json:"notes,omitempty"`
	HasUpdate       bool   `json:"hasUpdate"`
	Current         string `json:"current,omitempty"`
	Latest          string `json:"latest,omitempty"`
	CheckError      string `json:"checkError,omitempty"`
}

type updateCenterStatus struct {
	System      osupdate.Status      `json:"system"`
	Apps        []appUpdateStatus    `json:"apps"`
	Engines     []engineUpdateStatus `json:"engines"`
	Driver      *nvidiaupdate.Status `json:"driver,omitempty"`
	DriverOwner string               `json:"driverOwner"`
	CheckedAt   string               `json:"checkedAt"`
	UpdateCount int                  `json:"updateCount"`
}

type managedEngineTarget struct {
	Image    string
	Digest   string
	Source   string
	Channel  string
	Verified bool
	Notes    string
}

func (s *Server) managedEngineTarget(ctx context.Context, app catalog.App) (managedEngineTarget, error) {
	if s.manifest != nil {
		if pin, ok := s.manifest.PinFor(ctx, app.ID, app.Image); ok {
			return managedEngineTarget{
				Image: pin.Ref(), Digest: pin.CurrentDigest(),
				Source: "cloudless", Channel: s.manifest.Channel(ctx),
				Verified: pin.Verified, Notes: pin.Notes,
			}, nil
		}
	}
	remote, err := s.eng.RemoteDigest(ctx, app.Image)
	if err != nil || remote == "" {
		return managedEngineTarget{}, fmt.Errorf("could not reach the engine image registry")
	}
	return managedEngineTarget{
		Image: imageRepository(app.Image) + "@" + remote, Digest: remote,
		Source: "upstream",
	}, nil
}

func imageRepository(ref string) string {
	ref = strings.TrimSpace(ref)
	if at := strings.Index(ref, "@"); at >= 0 {
		ref = ref[:at]
	}
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		ref = ref[:colon]
	}
	return ref
}

func sameImageRepository(left, right string) bool {
	return imageRepository(left) != "" && imageRepository(left) == imageRepository(right)
}

// repositoryDigest finds a previously pulled tag from the same managed image
// repository. This matters when a Cloudless catalog update changes a fixed tag:
// an inactive prefetched engine is still installed even though its old tag is no
// longer present in the new catalog entry.
func (s *Server) repositoryDigest(ctx context.Context, image string) string {
	images, err := s.eng.ListImageDigests(ctx)
	if err != nil {
		return ""
	}
	repository := imageRepository(image)
	for _, candidate := range images {
		if candidate.Repository != repository || candidate.Digest == "" || candidate.Digest == "<none>" {
			continue
		}
		return candidate.Digest
	}
	return ""
}

func (s *Server) managedEngineUpdate(ctx context.Context, app catalog.App, selected string) engineUpdateStatus {
	result := engineUpdateStatus{ID: app.ID, Name: app.Name, Selected: app.ID == selected}
	container, _ := s.eng.Find(ctx, app.ContainerName())
	usesManagedImage := container != nil && sameImageRepository(container.Image, app.Image)
	result.Active = container != nil && container.State == "running"
	result.RestartOnUpdate = result.Active && usesManagedImage && s.managedEngineUsesBaseImage(app)

	installed := ""
	if usesManagedImage {
		installed, _ = s.eng.ContainerImageDigest(ctx, app.ContainerName())
	}
	if installed == "" {
		installed, _ = s.eng.ImageDigest(ctx, app.Image)
	}
	if installed == "" {
		installed = s.repositoryDigest(ctx, app.Image)
	}
	if installed == "" {
		return result
	}
	result.Installed = true

	target, err := s.managedEngineTarget(ctx, app)
	if err != nil {
		result.CheckError = "Could not reach the engine image registry."
		return result
	}
	result.Source, result.Channel = target.Source, target.Channel
	result.Verified, result.Notes = target.Verified, target.Notes
	// Digest-pinned pulls intentionally do not retag the catalog reference. For
	// an inactive or model-specific runtime, the presence of the exact reviewed
	// reference means the base engine update is already installed. An active
	// base runtime still compares its container digest so it remains actionable
	// until the safe restart has actually cut over.
	if target.Image != app.Image && !result.RestartOnUpdate {
		if reviewed, _ := s.eng.ImageDigest(ctx, target.Image); reviewed == target.Digest {
			installed = reviewed
		}
	}
	if s.state != nil {
		if recorded, ok := s.state.ManagedEngineArtifact(app.ID); ok &&
			recorded.Image == target.Image && recorded.DownloadedDigest == target.Digest {
			if present, _ := s.eng.ImageDigest(ctx, target.Image); present == target.Digest && !result.RestartOnUpdate {
				installed = present
			}
			if result.RestartOnUpdate && recorded.ActiveDigest == target.Digest {
				if current, _ := s.eng.ContainerImageDigest(ctx, app.ContainerName()); current == target.Digest {
					installed = current
				}
			}
		}
	}
	result.Current = shortDigest(installed)
	result.HasUpdate, result.Latest = installed != target.Digest, shortDigest(target.Digest)
	return result
}

// updateGet reports whether a newer image is available for one app.
func (s *Server) updateGet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || app.Service {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.appUpdate(ctx, app))
}

func (s *Server) appUpdate(ctx context.Context, app catalog.App) appUpdateStatus {
	result := appUpdateStatus{ID: app.ID, Name: app.Name, Category: app.Category}
	if app.Build != "" {
		container, _ := s.eng.Find(ctx, app.ContainerName())
		result.Installed, result.Build = container != nil, true
		return result
	}
	installed, _ := s.eng.ContainerImageDigest(ctx, app.ContainerName())
	if installed == "" {
		installed, _ = s.eng.ImageDigest(ctx, app.Image)
	}
	if installed == "" {
		return result
	}
	result.Installed, result.Updatable, result.Current = true, true, shortDigest(installed)
	if s.manifest != nil {
		if pin, ok := s.manifest.PinFor(ctx, app.ID, app.Image); ok {
			digest := pin.CurrentDigest()
			result.Source, result.Channel = "cloudless", s.manifest.Channel(ctx)
			result.Verified, result.Notes = pin.Verified, pin.Notes
			result.HasUpdate, result.Latest = installed != digest, shortDigest(digest)
			return result
		}
	}
	remote, err := s.eng.RemoteDigest(ctx, app.Image)
	result.Source = "upstream"
	if err != nil || remote == "" {
		result.CheckError = "Could not reach the image registry."
		return result
	}
	result.HasUpdate, result.Latest = installed != remote, shortDigest(remote)
	return result
}

func (s *Server) appIsInstalled(ctx context.Context, app catalog.App) bool {
	if app.Build != "" {
		container, _ := s.eng.Find(ctx, app.ContainerName())
		return container != nil
	}
	digest, _ := s.eng.ContainerImageDigest(ctx, app.ContainerName())
	if digest == "" {
		digest, _ = s.eng.ImageDigest(ctx, app.Image)
	}
	return digest != ""
}

// updateCenterGet aggregates the update authorities into one stable contract.
// Registry checks use bounded parallelism so a slow image does not serialize
// every installed application behind it.
func (s *Server) updateCenterGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 22*time.Second)
	defer cancel()
	system, err := osupdate.Read()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	all := catalog.All()
	results := make([]appUpdateStatus, len(all))
	managedEngines := catalog.Engines()
	engineResults := make([]engineUpdateStatus, len(managedEngines))
	selectedEngine := catalog.DefaultEngine()
	if s.state != nil {
		if configured := s.state.Get().Engine; configured != "" {
			selectedEngine = configured
		}
	}
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for index, app := range all {
		if app.Service || (app.Image == "" && app.Build == "") {
			continue
		}
		wg.Add(1)
		go func(i int, candidate catalog.App) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			results[i] = s.appUpdate(ctx, candidate)
		}(index, app)
	}
	for index, candidate := range managedEngines {
		wg.Add(1)
		go func(i int, app catalog.App) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			engineResults[i] = s.managedEngineUpdate(ctx, app, selectedEngine)
		}(index, candidate)
	}
	wg.Wait()
	installed := make([]appUpdateStatus, 0, len(results))
	installedEngines := make([]engineUpdateStatus, 0, len(engineResults))
	count := 0
	for _, result := range results {
		if !result.Installed {
			continue
		}
		installed = append(installed, result)
		if result.HasUpdate {
			count++
		}
	}
	for _, result := range engineResults {
		if !result.Installed {
			continue
		}
		installedEngines = append(installedEngines, result)
		if result.HasUpdate {
			count++
		}
	}
	sort.Slice(installed, func(i, j int) bool {
		if installed[i].HasUpdate != installed[j].HasUpdate {
			return installed[i].HasUpdate
		}
		return installed[i].Name < installed[j].Name
	})
	sort.Slice(installedEngines, func(i, j int) bool {
		if installedEngines[i].HasUpdate != installedEngines[j].HasUpdate {
			return installedEngines[i].HasUpdate
		}
		if installedEngines[i].Active != installedEngines[j].Active {
			return installedEngines[i].Active
		}
		return installedEngines[i].Name < installedEngines[j].Name
	})
	if system.State == "available" || system.RebootRequired {
		count++
	}
	response := updateCenterStatus{System: system, Apps: installed, Engines: installedEngines, DriverOwner: "cloudless", CheckedAt: time.Now().UTC().Format(time.RFC3339), UpdateCount: count}
	snapshot := capabilities.Current()
	if feature, ok := snapshot.Features[capabilities.GenericDriverUpdates]; ok && feature.Available {
		driver, readErr := nvidiaupdate.Read()
		if readErr == nil {
			response.Driver = &driver
			if driver.UpdateAvailable || driver.RebootRequired {
				response.UpdateCount++
			}
		}
	} else {
		response.DriverOwner = "nvidia-dgx"
	}
	writeJSON(w, http.StatusOK, response)
}

// updateCenterCheck starts the privileged system/driver checkers. Application
// and managed-engine registry checks are performed by the following
// GET /api/updates request.
func (s *Server) updateCenterCheck(w http.ResponseWriter, r *http.Request) {
	failures := []string{}
	if err := s.runPrivileged(r.Context(), privileged.ActionSystemUpdateCheck); err != nil {
		failures = append(failures, err.Error())
	}
	snapshot := capabilities.Current()
	if feature, ok := snapshot.Features[capabilities.GenericDriverUpdates]; ok && feature.Available {
		if err := s.runPrivileged(r.Context(), privileged.ActionNVIDIAUpdateCheck); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": strings.Join(failures, "; ")})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

// updateCenterAppsApply starts each selected application independently. The
// per-app lock manager allows unrelated updates to run in parallel while still
// rejecting a conflicting install/reset/uninstall for the same application.
func (s *Server) updateCenterAppsApply(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "update-apps" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "app update confirmation required"})
		return
	}
	var request struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid app update request"})
		return
	}
	started := map[string]string{}
	for _, id := range request.IDs {
		app, ok := catalog.Get(id)
		if !ok || app.Service || (app.Image == "" && app.Build == "") {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		installed := s.appIsInstalled(ctx, app)
		cancel()
		if !installed {
			continue
		}
		job, created := s.createAppUpdate(app)
		if created || job.AppID == "app:"+app.ID+":update" {
			started[id] = job.ID
		}
	}
	if len(started) == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no eligible application updates could be started"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"jobs": started})
}

// updateApply keeps the legacy per-app route while using the same daemon-owned
// update job contract as the Update Center.
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || app.Service {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	if app.Image == "" && app.Build == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": app.Name + " is not installable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	installed := s.appIsInstalled(ctx, app)
	cancel()
	if !installed {
		writeJSON(w, http.StatusConflict, map[string]string{"error": app.Name + " is not installed"})
		return
	}
	job, created := s.createAppUpdate(app)
	if !created && job.AppID != "app:"+app.ID+":update" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": app.Name + " already has another background operation"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func (s *Server) createAppUpdate(app catalog.App) (*jobs.Job, bool) {
	identity := "app:" + app.ID + ":update"
	job, created := s.jobs.CreateUnique(identity, "app:"+app.ID+":")
	if created {
		s.auditJob(job, "update", "application", app.ID)
		go s.runInstall(job, app)
	}
	return job, created
}

func (s *Server) updateCenterEngineApply(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "update-engine" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "managed engine update confirmation required"})
		return
	}
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || !app.Engine || strings.HasPrefix(app.ID, "custom-") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown managed inference engine"})
		return
	}
	selected := catalog.DefaultEngine()
	if s.state != nil && s.state.Get().Engine != "" {
		selected = s.state.Get().Engine
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	status := s.managedEngineUpdate(ctx, app, selected)
	cancel()
	if !status.Installed {
		writeJSON(w, http.StatusConflict, map[string]string{"error": app.Name + " is not installed or prefetched"})
		return
	}
	if !status.HasUpdate {
		writeJSON(w, http.StatusConflict, map[string]string{"error": app.Name + " is already current"})
		return
	}
	targetCtx, targetCancel := context.WithTimeout(r.Context(), 20*time.Second)
	target, targetErr := s.managedEngineTarget(targetCtx, app)
	targetCancel()
	if targetErr != nil || target.Digest == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the reviewed engine target could not be resolved"})
		return
	}
	identity := "engine-update:" + app.ID
	job, created := s.jobs.CreateUnique(identity, "engine-update:")
	if !created {
		if job.AppID != identity {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another inference engine update is already running"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "engine": app.ID})
		return
	}
	s.auditJob(job, "update", "engine", app.ID)
	go s.runManagedEngineUpdate(job, app, target)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "engine": app.ID})
}

func (s *Server) managedEngineUsesBaseImage(app catalog.App) bool {
	model := ""
	if s.state != nil {
		model = s.state.Get().Model
	}
	return catalog.EngineSpec(app, model).Image == app.Image
}

func (s *Server) pullManagedEngine(ctx context.Context, job *jobs.Job, app catalog.App, image string) error {
	layers := map[string]*dockerPullLayer{}
	job.ProgressOperation("pulling", "Checking the managed "+app.Name+" image", app.Name, 5, 0, 1)
	return s.eng.PullStream(ctx, image, func(line string) {
		id, status, ok := splitStatus(line)
		switch {
		case ok && strings.HasPrefix(status, "Pulling fs layer"):
			if layers[id] == nil {
				layers[id] = &dockerPullLayer{}
			}
		case ok && (status == "Pull complete" || status == "Already exists"):
			if layers[id] == nil {
				layers[id] = &dockerPullLayer{}
			}
			layers[id].complete = true
			if layers[id].total > 0 {
				layers[id].done = layers[id].total
			}
		case ok && strings.HasPrefix(status, "Downloading"):
			if doneBytes, totalBytes, parsed := parseDockerLayerProgress(status); parsed {
				if layers[id] == nil {
					layers[id] = &dockerPullLayer{}
				}
				layers[id].done, layers[id].total = doneBytes, totalBytes
			}
		case strings.HasPrefix(line, "Status:"):
			job.ProgressOperation("pulling", line, app.Name, 78, 0, 1)
			return
		default:
			return
		}
		done, total, bytesDone, bytesTotal := dockerPullTotals(layers)
		percent := 10
		if total > 0 {
			percent += done * 65 / total
		}
		message := fmt.Sprintf("Downloading %s runtime layers — %d of %d complete", app.Name, done, total)
		job.ProgressOperation("pulling", message, app.Name, percent, 0, 1)
		job.ProgressDetail("pulling", message, done, total)
		if bytesTotal > 0 {
			job.ProgressBytesDetail("pulling", message, bytesDone, bytesTotal)
		}
	})
}

func (s *Server) runManagedEngineUpdate(job *jobs.Job, app catalog.App, target managedEngineTarget) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if err := s.pullManagedEngine(ctx, job, app, target.Image); err != nil {
		job.Fail(fmt.Errorf("download %s update: %w", app.Name, err))
		return
	}
	downloaded, _ := s.eng.ImageDigest(ctx, target.Image)
	if downloaded != target.Digest {
		job.Fail(fmt.Errorf("download %s update: pulled digest %s does not match target %s", app.Name, shortDigest(downloaded), shortDigest(target.Digest)))
		return
	}
	if s.state != nil {
		previous, _ := s.state.ManagedEngineArtifact(app.ID)
		previous.EngineID, previous.Image = app.ID, target.Image
		previous.DownloadedDigest = target.Digest
		previous.Source, previous.Channel = target.Source, target.Channel
		previous.Verified = target.Verified
		if err := s.state.SetManagedEngineArtifact(previous); err != nil {
			job.Fail(fmt.Errorf("record %s downloaded runtime: %w", app.Name, err))
			return
		}
	}

	container, _ := s.eng.Find(ctx, app.ContainerName())
	runningManagedImage := container != nil && container.State == "running" &&
		sameImageRepository(container.Image, app.Image) && s.managedEngineUsesBaseImage(app)
	if !runningManagedImage {
		job.ProgressOperation("ready", app.Name+" is updated and ready for its next launch", app.Name, 95, 1, 1)
		job.Succeed("")
		return
	}

	latest := target.Digest
	current, _ := s.eng.ContainerImageDigest(ctx, app.ContainerName())
	if latest != "" && current == latest {
		job.Succeed("")
		return
	}
	job.ProgressOperation("restarting", "Restarting the active "+app.Name+" with the updated runtime", app.Name, 82, 0, 1)
	// applyEngine preserves the selected model, execution mode and stable API
	// identity. On a Spark cluster it also pulls the same image on every worker.
	s.applyEngineVerified(job, app, target.Image, func(verifyCtx context.Context) error {
		active, _ := s.eng.ContainerImageDigest(verifyCtx, app.ContainerName())
		if active != target.Digest {
			return fmt.Errorf("%s restarted but active digest %s does not match target %s", app.Name, shortDigest(active), shortDigest(target.Digest))
		}
		if s.state != nil {
			artifact, _ := s.state.ManagedEngineArtifact(app.ID)
			artifact.ActiveDigest = active
			if err := s.state.SetManagedEngineArtifact(artifact); err != nil {
				return fmt.Errorf("record active %s runtime: %w", app.Name, err)
			}
		}
		return nil
	})
}
