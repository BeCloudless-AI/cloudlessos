package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/jobs"
)

type packView struct {
	catalog.Pack
	Status      string        `json:"status"`
	Installed   int           `json:"installed"`
	Total       int           `json:"total"`
	NeedsEngine bool          `json:"needsEngine"`
	MemoryGB    int           `json:"memoryGB"`
	DiskGB      int           `json:"diskGB"`
	VRAMGB      int           `json:"vramGB"`
	Components  []catalog.App `json:"components"`
}

func (s *Server) packsList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	result := make([]packView, 0)
	for _, pack := range catalog.Packs() {
		view := packView{Pack: pack}
		order, err := catalog.PackInstallOrder(pack.ID)
		if err != nil {
			continue
		}
		for _, app := range order {
			view.Components = append(view.Components, app)
			view.MemoryGB += app.Resources.MemoryGB
			view.DiskGB += app.Resources.DiskGB
			view.VRAMGB += app.Resources.VRAMGB
			view.NeedsEngine = view.NeedsEngine || app.NeedsEngine
			if container, _ := s.eng.Find(ctx, app.ContainerName()); container != nil && container.State == "running" {
				view.Installed++
			}
		}
		view.Total = len(view.Components)
		switch {
		case view.Installed == 0:
			view.Status = "available"
		case view.Installed == view.Total:
			view.Status = "installed"
		default:
			view.Status = "partial"
		}
		result = append(result, view)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) packInstall(w http.ResponseWriter, r *http.Request) {
	pack, ok := catalog.GetPack(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown or unsupported service pack"})
		return
	}
	order, err := catalog.PackInstallOrder(pack.ID)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	neededDiskGB := 0
	needsEngine := false
	preflightCtx, preflightCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer preflightCancel()
	for _, app := range order {
		needsEngine = needsEngine || app.NeedsEngine
		if container, _ := s.eng.Find(preflightCtx, app.ContainerName()); container != nil && container.State == "running" {
			continue
		}
		neededDiskGB += app.Resources.DiskGB
	}
	if needsEngine && !engineReady(preflightCtx) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "load a model before installing this pack"})
		return
	}
	if available := hardware.Sys().StorageAvailableBytes; available > 0 && uint64(neededDiskGB)*(1<<30) > available {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": fmt.Sprintf("%s needs about %d GB but this machine does not have enough free storage", pack.Name, neededDiskGB)})
		return
	}
	job, created := s.jobs.CreateUnique("pack:"+pack.ID, "pack:"+pack.ID)
	if !created {
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "pack": pack.ID})
		return
	}
	go s.runPackInstall(job, pack, order)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "pack": pack.ID})
}

func (s *Server) runPackInstall(job *jobs.Job, pack catalog.Pack, order []catalog.App) {
	ids := make([]string, 0, len(order))
	for _, app := range order {
		ids = append(ids, app.ID)
	}
	job.ProgressOperation("queued", "Installation queued in the background", pack.Name, 0, 0, len(order))
	unlock := s.appOperations.lock(ids...)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()
	newContainers := make([]string, 0, len(order))
	rollback := func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		for i := len(newContainers) - 1; i >= 0; i-- {
			_ = s.eng.Remove(cleanup, newContainers[i])
		}
	}
	for index, app := range order {
		if existing, _ := s.eng.Find(ctx, app.ContainerName()); existing == nil || existing.State != "running" {
			job.ProgressOperation("component", fmt.Sprintf("Installing %s — component %d of %d", app.Name, index+1, len(order)), app.Name, operationPercent(index, len(order), 4), index, len(order))
			id, err := s.installOne(ctx, job, app, index, len(order))
			if err != nil {
				rollback()
				job.Fail(fmt.Errorf("%s could not install %s: %w; newly installed components were removed", pack.Name, app.Name, err))
				return
			}
			if id != "" {
				newContainers = append(newContainers, app.ContainerName())
			}
		}
		if err := s.configurePackApp(ctx, job, app); err != nil {
			rollback()
			job.Fail(fmt.Errorf("%s installed but %s could not be configured: %w", pack.Name, app.Name, err))
			return
		}
	}
	if err := s.state.SetPackInstalled(pack.ID, true); err != nil {
		rollback()
		job.Fail(fmt.Errorf("%s installed but its state could not be saved: %w", pack.Name, err))
		return
	}
	job.ProgressOperation("verifying", "All components passed their health contracts", pack.Name, 99, len(order), len(order))
	job.Succeed("")
}

func (s *Server) configurePackApp(ctx context.Context, job *jobs.Job, app catalog.App) error {
	if app.ID != "perplexica" {
		return nil
	}
	job.ProgressOperation("configuring", "Connecting Cloudless Research to your Cloudless model", app.Name, 96, 0, 1)
	for port := range app.Ports {
		return configureCloudlessResearch(ctx, fmt.Sprintf("http://127.0.0.1:%d", port))
	}
	return fmt.Errorf("research endpoint is unavailable")
}

type researchProvider struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ChatModels []struct {
		Key string `json:"key"`
	} `json:"chatModels"`
}

// configureCloudlessResearch is idempotent, so retrying an interrupted install
// never duplicates the Cloudless provider or model.
func configureCloudlessResearch(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	requestJSON := func(method, path string, payload any, result any) error {
		var raw []byte
		var err error
		if payload != nil {
			raw, err = json.Marshal(payload)
			if err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("%s returned %s", path, resp.Status)
		}
		if result != nil {
			return json.NewDecoder(resp.Body).Decode(result)
		}
		return nil
	}

	var listing struct {
		Providers []researchProvider `json:"providers"`
	}
	if err := requestJSON(http.MethodGet, "/api/providers", nil, &listing); err != nil {
		return err
	}
	var cloudless *researchProvider
	for i := range listing.Providers {
		if listing.Providers[i].Name == "Cloudless" {
			cloudless = &listing.Providers[i]
			break
		}
	}
	if cloudless == nil {
		var created struct {
			Provider researchProvider `json:"provider"`
		}
		payload := map[string]any{
			"type": "openai", "name": "Cloudless",
			"config": map[string]string{"apiKey": "cloudless", "baseURL": "http://cloudless-ai:8000/v1"},
		}
		if err := requestJSON(http.MethodPost, "/api/providers", payload, &created); err != nil {
			return err
		}
		cloudless = &created.Provider
	}
	hasModel := false
	for _, model := range cloudless.ChatModels {
		hasModel = hasModel || model.Key == "cloudless"
	}
	if !hasModel {
		payload := map[string]string{"type": "chat", "key": "cloudless", "name": "Cloudless"}
		if err := requestJSON(http.MethodPost, "/api/providers/"+url.PathEscape(cloudless.ID)+"/models", payload, nil); err != nil {
			return err
		}
	}
	return requestJSON(http.MethodPost, "/api/config/setup-complete", nil, nil)
}

func (s *Server) packUninstall(w http.ResponseWriter, r *http.Request) {
	pack, ok := catalog.GetPack(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown or unsupported service pack"})
		return
	}
	if r.Header.Get("X-Cloudless-Action") != "pack-uninstall" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "pack uninstall confirmation header required"})
		return
	}
	job, created := s.jobs.CreateUnique("pack-remove:"+pack.ID, "pack-remove:"+pack.ID)
	if !created {
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "pack": pack.ID})
		return
	}
	go s.runPackUninstall(job, pack)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "pack": pack.ID})
}

func (s *Server) runPackUninstall(job *jobs.Job, pack catalog.Pack) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	order, err := catalog.PackInstallOrder(pack.ID)
	if err != nil {
		job.Fail(err)
		return
	}
	ids := make([]string, 0, len(order))
	for _, app := range order {
		ids = append(ids, app.ID)
	}
	job.ProgressOperation("queued", "Removal queued in the background", pack.Name, 0, 0, len(order))
	unlock := s.appOperations.lock(ids...)
	defer unlock()
	usedElsewhere := map[string]bool{}
	for _, installedID := range s.state.Packs() {
		if installedID == pack.ID {
			continue
		}
		if other, ok := catalog.GetPack(installedID); ok {
			otherOrder, _ := catalog.PackInstallOrder(other.ID)
			for _, app := range otherOrder {
				usedElsewhere[app.ID] = true
			}
		}
	}
	for index := len(order) - 1; index >= 0; index-- {
		app := order[index]
		if app.Preinstall || app.Engine || usedElsewhere[app.ID] {
			continue
		}
		if container, _ := s.eng.Find(ctx, app.ContainerName()); container == nil {
			continue
		}
		done := len(order) - index - 1
		job.ProgressOperation("removing", "Removing "+app.Name, app.Name, operationPercent(done, len(order), 20), done, len(order))
		if err := s.eng.Remove(ctx, app.ContainerName()); err != nil {
			job.Fail(err)
			return
		}
	}
	if err := s.state.SetPackInstalled(pack.ID, false); err != nil {
		job.Fail(err)
		return
	}
	job.Succeed("")
}
