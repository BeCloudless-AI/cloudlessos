package api

import (
	"context"
	"encoding/json"
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

type updateCenterStatus struct {
	System      osupdate.Status      `json:"system"`
	Apps        []appUpdateStatus    `json:"apps"`
	Driver      *nvidiaupdate.Status `json:"driver,omitempty"`
	DriverOwner string               `json:"driverOwner"`
	CheckedAt   string               `json:"checkedAt"`
	UpdateCount int                  `json:"updateCount"`
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
	wg.Wait()
	installed := make([]appUpdateStatus, 0, len(results))
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
	sort.Slice(installed, func(i, j int) bool {
		if installed[i].HasUpdate != installed[j].HasUpdate {
			return installed[i].HasUpdate
		}
		return installed[i].Name < installed[j].Name
	})
	if system.State == "available" || system.RebootRequired {
		count++
	}
	response := updateCenterStatus{System: system, Apps: installed, DriverOwner: "cloudless", CheckedAt: time.Now().UTC().Format(time.RFC3339), UpdateCount: count}
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
// registry checks are performed by the following GET /api/updates request.
func (s *Server) updateCenterCheck(w http.ResponseWriter, _ *http.Request) {
	failures := []string{}
	if err := startUpdateUnit("cloudless-update-check.service"); err != nil {
		failures = append(failures, err.Error())
	}
	snapshot := capabilities.Current()
	if feature, ok := snapshot.Features[capabilities.GenericDriverUpdates]; ok && feature.Available {
		if err := startUpdateUnit("cloudless-nvidia-check.service"); err != nil {
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
		go s.runInstall(job, app)
	}
	return job, created
}
