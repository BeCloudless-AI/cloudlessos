package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelstorage"
	"github.com/cloudless/orchestrator/internal/privileged"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

type modelStorageView struct {
	Config  modelstorage.Config                   `json:"config"`
	Ready   bool                                  `json:"ready"`
	Mounted bool                                  `json:"mounted"`
	Shared  bool                                  `json:"shared"`
	Nodes   []sparkcluster.ModelStorageNodeStatus `json:"nodes,omitempty"`
	Error   string                                `json:"error,omitempty"`
}

func (s *Server) modelStorageCacheRoot() string {
	if root := strings.TrimSpace(s.modelStorageRoot); root != "" {
		return filepath.Clean(root)
	}
	return modelstorage.MountPoint
}

func (s *Server) managedModelStorageRoot() string {
	if root := strings.TrimSpace(s.modelStorageManagedRoot); root != "" {
		return filepath.Clean(root)
	}
	return modelstorage.CacheHub
}

func (s *Server) managedModelStorageReadyPath() string {
	if path := strings.TrimSpace(s.modelStorageReadyPath); path != "" {
		return filepath.Clean(path)
	}
	return modelstorage.ReadyPath
}

func (s *Server) modelStorageGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.modelStorageView(ctx))
}

func (s *Server) modelStorageView(ctx context.Context) modelStorageView {
	config := s.state.ModelStorageConfig()
	view := modelStorageView{Config: config, Ready: true}
	if config.Mode != modelstorage.ModeNFS {
		return view
	}
	view.Ready, view.Mounted, view.Shared = false, false, false
	if err := s.verifyModelStorageHost(ctx, config); err != nil {
		view.Error = err.Error()
		return view
	}
	view.Mounted = true
	nodes, err := s.verifyModelStoragePeers(ctx, config)
	view.Nodes = nodes
	if err != nil {
		view.Error = err.Error()
		return view
	}
	view.Ready, view.Shared = true, true
	return view
}

func (s *Server) modelStorageNFSSet(w http.ResponseWriter, r *http.Request) {
	if err := s.requireIdleModelStorage(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if current := s.state.ModelStorageConfig(); current.Mode == modelstorage.ModeNFS {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "switch back to local storage before changing the NFS export"})
		return
	}
	var requested struct {
		Managed bool   `json:"managed"`
		Server  string `json:"server"`
		Export  string `json:"export"`
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&requested); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid NFS settings"})
		return
	}
	marker := make([]byte, 16)
	if _, err := rand.Read(marker); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create the shared-storage identity"})
		return
	}
	config := modelstorage.Config{Mode: modelstorage.ModeNFS, Managed: requested.Managed, Server: requested.Server, Export: requested.Export,
		Version: requested.Version, MarkerID: hex.EncodeToString(marker), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if requested.Managed {
		server, err := s.managedNFSCoordinator()
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		config.Server, config.Export, config.Version = server, modelstorage.ManagedExport, modelstorage.DefaultVersion
	}
	config = config.Normalized()
	if err := config.Validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.applyModelStorageHost(ctx, config); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "mount NFS model storage on this Spark: " + err.Error()})
		return
	}
	rollback := func() {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer rollbackCancel()
		_, _ = s.useLocalModelStoragePeers(rollbackCtx)
		_ = s.useLocalModelStorageHost(rollbackCtx)
	}
	if _, err := s.applyModelStoragePeers(ctx, config); err != nil {
		rollback()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	nodes, err := s.verifyModelStoragePeers(ctx, config)
	if err != nil {
		rollback()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if err := s.state.SetModelStorageConfig(config); err != nil {
		rollback()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.refreshModelStorageServiceNamespaces(ctx); err != nil {
		_ = s.state.SetModelStorageConfig(modelstorage.Config{Mode: modelstorage.ModeLocal})
		rollback()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refresh Cloudless model-storage view: " + err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "storage", Event: "nfs-enabled", Outcome: "success", Actor: "local-ui", Target: config.Server + config.Export})
	writeJSON(w, http.StatusOK, modelStorageView{Config: config, Ready: true, Mounted: true, Shared: true, Nodes: nodes})
}

func (s *Server) modelStorageLocalSet(w http.ResponseWriter, r *http.Request) {
	if err := s.requireIdleModelStorage(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	current := s.state.ModelStorageConfig()
	if current.Mode != modelstorage.ModeNFS {
		writeJSON(w, http.StatusOK, modelStorageView{Config: modelstorage.Config{Mode: modelstorage.ModeLocal}, Ready: true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if _, err := s.useLocalModelStoragePeers(ctx); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if err := s.useLocalModelStorageHost(ctx); err != nil {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer rollbackCancel()
		_, _ = s.applyModelStoragePeers(rollbackCtx, current)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "restore local model storage on this Spark: " + err.Error()})
		return
	}
	local := modelstorage.Config{Mode: modelstorage.ModeLocal}
	if err := s.state.SetModelStorageConfig(local); err != nil {
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer rollbackCancel()
		_ = s.applyModelStorageHost(rollbackCtx, current)
		_, _ = s.applyModelStoragePeers(rollbackCtx, current)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.refreshModelStorageServiceNamespaces(ctx); err != nil {
		_ = s.applyModelStorageHost(ctx, current)
		_, _ = s.applyModelStoragePeers(ctx, current)
		_ = s.state.SetModelStorageConfig(current)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refresh Cloudless local-storage view: " + err.Error()})
		return
	}
	s.auditSecurity(gatewayAuditEvent{Category: "storage", Event: "local-enabled", Outcome: "success", Actor: "local-ui", Target: "model-cache"})
	writeJSON(w, http.StatusOK, modelStorageView{Config: local, Ready: true})
}

func (s *Server) refreshModelStorageServiceNamespaces(ctx context.Context) error {
	if s.modelStorageRefresh != nil {
		return s.modelStorageRefresh(ctx)
	}
	return s.privilegedAction(ctx, privileged.ActionModelStorageRefresh)
}

func (s *Server) requireIdleModelStorage() error {
	state := s.state.Get()
	if !state.EngineUnloaded && (state.Engine != "" || state.Model != "" || state.LocalRecipeID != "") {
		return errors.New("unload Cloudless AI before changing model storage")
	}
	if state.InferenceOperation.ID != "" {
		return errors.New("wait for the current AI operation to finish before changing model storage")
	}
	if s.jobs != nil {
		for _, job := range s.jobs.List("") {
			if !job.Done {
				return errors.New("wait for active downloads and recipe operations before changing model storage")
			}
		}
	}
	return nil
}

func (s *Server) applyModelStorageHost(ctx context.Context, config modelstorage.Config) error {
	if s.modelStorageHostApply != nil {
		return s.modelStorageHostApply(ctx, config)
	}
	payload, _ := json.Marshal(config)
	return s.privilegedValue(ctx, privileged.ActionModelStorageNFSApply, string(payload))
}

func (s *Server) useLocalModelStorageHost(ctx context.Context) error {
	if s.modelStorageHostLocal != nil {
		return s.modelStorageHostLocal(ctx)
	}
	return s.privilegedAction(ctx, privileged.ActionModelStorageLocal)
}

func (s *Server) verifyModelStorageHost(ctx context.Context, config modelstorage.Config) error {
	if s.modelStorageHostVerify != nil {
		return s.modelStorageHostVerify(ctx, config)
	}
	if config.Managed {
		marker, err := os.ReadFile(filepath.Join(s.managedModelStorageRoot(), modelstorage.MarkerName))
		if err != nil || string(marker) != modelstorage.MarkerContents(config) {
			return errors.New("the coordinator cannot verify its managed shared-storage marker")
		}
		// cloudless-model-storage is the privileged owner of exportfs. It writes
		// this identity-bound readiness proof only after exportfs -ra succeeds;
		// the unprivileged HTTP daemon never invokes host service controls.
		ready, err := os.ReadFile(s.managedModelStorageReadyPath())
		if err != nil || string(ready) != modelstorage.MarkerContents(config) {
			return errors.New("the coordinator's private NFS export is not active")
		}
		return nil
	}
	output, err := exec.CommandContext(ctx, "/usr/bin/findmnt", "-n", "--first-only", "-o", "SOURCE,FSTYPE", "--mountpoint", s.modelStorageCacheRoot()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("shared model cache is not mounted: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) != 2 || fields[0] != config.Source() || fields[1] != "nfs4" && fields[1] != "nfs" {
		return fmt.Errorf("shared model storage mount is %q, expected %q over NFSv4", strings.TrimSpace(string(output)), config.Source())
	}
	hubOutput, err := exec.CommandContext(ctx, "/usr/bin/findmnt", "-n", "--first-only", "-o", "FSTYPE", "--mountpoint", modelstorage.CacheHub).CombinedOutput()
	if err != nil || strings.TrimSpace(string(hubOutput)) != "nfs4" && strings.TrimSpace(string(hubOutput)) != "nfs" {
		return errors.New("shared model-weight hub is not mounted over NFS")
	}
	marker, err := os.ReadFile(filepath.Join(s.modelStorageCacheRoot(), modelstorage.MarkerName))
	if err != nil || string(marker) != modelstorage.MarkerContents(config) {
		return errors.New("this Spark cannot see the expected writable shared-storage marker")
	}
	return nil
}

func (s *Server) managedNFSCoordinator() (string, error) {
	if s.modelStorageManagedNFS != nil {
		return s.modelStorageManagedNFS()
	}
	return sparkcluster.ManagedNFSServer()
}

func (s *Server) applyModelStoragePeers(ctx context.Context, config modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
	if s.modelStoragePeersApply != nil {
		return s.modelStoragePeersApply(ctx, config)
	}
	return sparkcluster.ApplyNFSModelStorage(ctx, config)
}

func (s *Server) useLocalModelStoragePeers(ctx context.Context) ([]sparkcluster.ModelStorageNodeStatus, error) {
	if s.modelStoragePeersLocal != nil {
		return s.modelStoragePeersLocal(ctx)
	}
	return sparkcluster.RemoveNFSModelStorage(ctx)
}

func (s *Server) verifyModelStoragePeers(ctx context.Context, config modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
	if s.modelStoragePeersVerify != nil {
		return s.modelStoragePeersVerify(ctx, config)
	}
	return sparkcluster.VerifyNFSModelStorage(ctx, config)
}

func writeSharedModelStorageMarker(root string, config modelstorage.Config) error {
	if err := os.MkdirAll(root, 0o770); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(root, ".cloudless-shared-storage-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.WriteString(modelstorage.MarkerContents(config)); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(root, modelstorage.MarkerName))
}

func (s *Server) sharedModelStorageForRecipe(ctx context.Context, recipe localrecipes.Recipe) (bool, error) {
	if s.state == nil || recipe.Distributed.Nodes <= 1 {
		return false, nil
	}
	config := s.state.ModelStorageConfig()
	if config.Mode != modelstorage.ModeNFS {
		return false, nil
	}
	if err := s.verifyModelStorageHost(ctx, config); err != nil {
		return false, fmt.Errorf("shared NFS model storage is unavailable on this Spark: %w", err)
	}
	if _, err := s.verifyModelStoragePeers(ctx, config); err != nil {
		return false, fmt.Errorf("shared NFS model storage is not ready on every enrolled Spark: %w", err)
	}
	return true, nil
}
