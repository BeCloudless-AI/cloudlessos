package api

import (
	"context"
	"fmt"
	"net/http"
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
	for _, snapshot := range s.jobs.List("pack:" + pack.ID) {
		if !snapshot.Done {
			writeJSON(w, http.StatusAccepted, map[string]string{"jobId": snapshot.ID, "pack": pack.ID})
			return
		}
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
	job := s.jobs.Create("pack:" + pack.ID)
	go s.runPackInstall(job, pack, order)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "pack": pack.ID})
}

func (s *Server) runPackInstall(job *jobs.Job, pack catalog.Pack, order []catalog.App) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
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
		if existing, _ := s.eng.Find(ctx, app.ContainerName()); existing != nil && existing.State == "running" {
			continue
		}
		job.Progress("component", fmt.Sprintf("Installing %s — component %d of %d", app.Name, index+1, len(order)), index, len(order))
		id, err := s.installOne(ctx, job, app)
		if err != nil {
			rollback()
			job.Fail(fmt.Errorf("%s could not install %s: %w; newly installed components were removed", pack.Name, app.Name, err))
			return
		}
		if id != "" {
			newContainers = append(newContainers, app.ContainerName())
		}
	}
	if err := s.state.SetPackInstalled(pack.ID, true); err != nil {
		rollback()
		job.Fail(fmt.Errorf("%s installed but its state could not be saved: %w", pack.Name, err))
		return
	}
	job.Progress("verifying", "All components passed their health contracts.", len(order), len(order))
	job.Succeed("")
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
	job := s.jobs.Create("pack-remove:" + pack.ID)
	go s.runPackUninstall(job, pack)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "pack": pack.ID})
}

func (s *Server) runPackUninstall(job *jobs.Job, pack catalog.Pack) {
	s.installMu.Lock()
	defer s.installMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	usedElsewhere := map[string]bool{}
	for _, installedID := range s.state.Packs() {
		if installedID == pack.ID {
			continue
		}
		if other, ok := catalog.GetPack(installedID); ok {
			order, _ := catalog.PackInstallOrder(other.ID)
			for _, app := range order {
				usedElsewhere[app.ID] = true
			}
		}
	}
	order, err := catalog.PackInstallOrder(pack.ID)
	if err != nil {
		job.Fail(err)
		return
	}
	for index := len(order) - 1; index >= 0; index-- {
		app := order[index]
		if app.Preinstall || app.Engine || usedElsewhere[app.ID] {
			continue
		}
		if container, _ := s.eng.Find(ctx, app.ContainerName()); container == nil {
			continue
		}
		job.Progress("removing", "Removing "+app.Name+"…", len(order)-index-1, len(order))
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
