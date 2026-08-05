package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelstorage"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
	"github.com/cloudless/orchestrator/internal/state"
)

func TestModelStorageNFSAndLocalLifecycle(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	var hostApplied, hostLocal, peersApplied, peersLocal, refreshed bool
	server := &Server{
		state: store, jobs: jobs.NewManager(), modelStorageRoot: filepath.Join(root, "models"),
		modelStorageHostApply: func(_ context.Context, config modelstorage.Config) error {
			hostApplied = config.Mode == modelstorage.ModeNFS
			return writeSharedModelStorageMarker(filepath.Join(root, "models"), config)
		},
		modelStorageHostLocal:  func(context.Context) error { hostLocal = true; return nil },
		modelStorageHostVerify: func(context.Context, modelstorage.Config) error { return nil },
		modelStoragePeersApply: func(_ context.Context, _ modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
			peersApplied = true
			return []sparkcluster.ModelStorageNodeStatus{{Name: "peer", Ready: true}}, nil
		},
		modelStoragePeersLocal: func(context.Context) ([]sparkcluster.ModelStorageNodeStatus, error) {
			peersLocal = true
			return []sparkcluster.ModelStorageNodeStatus{{Name: "peer", Ready: true}}, nil
		},
		modelStoragePeersVerify: func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
			return []sparkcluster.ModelStorageNodeStatus{{Name: "peer", Ready: true}}, nil
		},
		modelStorageRefresh: func(context.Context) error { refreshed = true; return nil },
	}

	request := httptest.NewRequest(http.MethodPost, "/api/settings/model-storage/nfs",
		bytes.NewBufferString(`{"server":"nas.example.com","export":"/cloudless/models","version":"4.2"}`))
	response := httptest.NewRecorder()
	server.modelStorageNFSSet(response, request)
	if response.Code != http.StatusOK || !hostApplied || !peersApplied || !refreshed {
		t.Fatalf("enable NFS = %d %s; host=%v peers=%v", response.Code, response.Body.String(), hostApplied, peersApplied)
	}
	config := store.ModelStorageConfig()
	if config.Mode != modelstorage.ModeNFS || config.MarkerID == "" {
		t.Fatalf("persisted NFS config = %#v", config)
	}
	marker, err := os.ReadFile(filepath.Join(server.modelStorageRoot, modelstorage.MarkerName))
	if err != nil || string(marker) != modelstorage.MarkerContents(config) {
		t.Fatalf("shared marker = %q, %v", marker, err)
	}

	response = httptest.NewRecorder()
	server.modelStorageLocalSet(response, httptest.NewRequest(http.MethodPost, "/api/settings/model-storage/local", nil))
	if response.Code != http.StatusOK || !hostLocal || !peersLocal {
		t.Fatalf("restore local = %d %s; host=%v peers=%v", response.Code, response.Body.String(), hostLocal, peersLocal)
	}
	if got := store.ModelStorageConfig(); got.Mode != modelstorage.ModeLocal {
		t.Fatalf("storage remained %#v", got)
	}
}

func TestModelStorageNFSRollsBackWhenPeerCannotMount(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	rolledBack := false
	server := &Server{
		state: store, jobs: jobs.NewManager(), modelStorageRoot: filepath.Join(root, "models"),
		modelStorageHostApply:  func(context.Context, modelstorage.Config) error { return nil },
		modelStorageHostLocal:  func(context.Context) error { rolledBack = true; return nil },
		modelStorageHostVerify: func(context.Context, modelstorage.Config) error { return nil },
		modelStoragePeersApply: func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
			return nil, errors.New("peer mount failed")
		},
		modelStoragePeersLocal: func(context.Context) ([]sparkcluster.ModelStorageNodeStatus, error) { return nil, nil },
	}
	response := httptest.NewRecorder()
	server.modelStorageNFSSet(response, httptest.NewRequest(http.MethodPost, "/api/settings/model-storage/nfs",
		strings.NewReader(`{"server":"10.0.0.8","export":"/cloudless/models","version":"4.1"}`)))
	if response.Code != http.StatusBadGateway || !rolledBack {
		t.Fatalf("failed peer response = %d %s; rollback=%v", response.Code, response.Body.String(), rolledBack)
	}
	if got := store.ModelStorageConfig(); got.Mode != modelstorage.ModeLocal {
		t.Fatalf("failed NFS configuration was persisted: %#v", got)
	}
}

func TestManagedModelStorageChoosesCoordinatorAndCloudlessExport(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	var applied modelstorage.Config
	server := &Server{
		state: store, jobs: jobs.NewManager(),
		modelStorageManagedNFS: func() (string, error) { return "10.100.0.1", nil },
		modelStorageHostApply:  func(_ context.Context, config modelstorage.Config) error { applied = config; return nil },
		modelStorageHostVerify: func(context.Context, modelstorage.Config) error { return nil },
		modelStoragePeersApply: func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
			return nil, nil
		},
		modelStoragePeersVerify: func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
			return nil, nil
		},
		modelStorageRefresh: func(context.Context) error { return nil },
	}
	response := httptest.NewRecorder()
	server.modelStorageNFSSet(response, httptest.NewRequest(http.MethodPost, "/api/settings/model-storage/nfs", strings.NewReader(`{"managed":true}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("managed NFS = %d %s", response.Code, response.Body.String())
	}
	if !applied.Managed || applied.Server != "10.100.0.1" || applied.Export != modelstorage.ManagedExport || applied.Version != modelstorage.DefaultVersion {
		t.Fatalf("managed defaults = %#v", applied)
	}
}

func TestManagedModelStorageRejectsUnavailableCoordinator(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	server := &Server{
		state: store, jobs: jobs.NewManager(),
		modelStorageManagedNFS: func() (string, error) { return "", errors.New("cluster is not configured") },
		modelStorageHostApply:  func(context.Context, modelstorage.Config) error { called = true; return nil },
	}
	response := httptest.NewRecorder()
	server.modelStorageNFSSet(response, httptest.NewRequest(http.MethodPost, "/api/settings/model-storage/nfs", strings.NewReader(`{"managed":true}`)))
	if response.Code != http.StatusConflict || called || !strings.Contains(response.Body.String(), "cluster is not configured") {
		t.Fatalf("unavailable coordinator = %d %s; apply=%v", response.Code, response.Body.String(), called)
	}
}

func TestModelStorageRejectsChangesWhileInferenceOrJobsAreActive(t *testing.T) {
	for _, scenario := range []string{"inference", "job"} {
		t.Run(scenario, func(t *testing.T) {
			store, err := state.Open(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			manager := jobs.NewManager()
			if scenario == "inference" {
				if err := store.SetEngine("vllm"); err != nil {
					t.Fatal(err)
				}
				if err := store.SetEngineUnloaded(false); err != nil {
					t.Fatal(err)
				}
			} else {
				manager.Create("model:test")
			}
			called := false
			server := &Server{state: store, jobs: manager, modelStorageHostApply: func(context.Context, modelstorage.Config) error { called = true; return nil }}
			response := httptest.NewRecorder()
			server.modelStorageNFSSet(response, httptest.NewRequest(http.MethodPost, "/api/settings/model-storage/nfs", strings.NewReader(`{"managed":true}`)))
			if response.Code != http.StatusConflict || called {
				t.Fatalf("active %s storage change = %d %s; apply=%v", scenario, response.Code, response.Body.String(), called)
			}
		})
	}
}

func TestManagedModelStorageVerifierBindsReadinessToExactIdentity(t *testing.T) {
	root := t.TempDir()
	readyPath := filepath.Join(root, "model-storage.ready")
	config := modelstorage.Config{Mode: modelstorage.ModeNFS, Managed: true, Server: "10.100.0.1", Export: modelstorage.ManagedExport, Version: "4.2", MarkerID: "0123456789abcdef0123456789abcdef"}
	if err := os.WriteFile(filepath.Join(root, modelstorage.MarkerName), []byte(modelstorage.MarkerContents(config)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readyPath, []byte(modelstorage.MarkerContents(config)), 0o644); err != nil {
		t.Fatal(err)
	}
	server := &Server{modelStorageManagedRoot: root, modelStorageReadyPath: readyPath}
	if err := server.verifyModelStorageHost(context.Background(), config); err != nil {
		t.Fatalf("matching managed readiness was rejected: %v", err)
	}
	if err := os.WriteFile(readyPath, []byte("cloudless-nfs-v1 stale identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := server.verifyModelStorageHost(context.Background(), config); err == nil {
		t.Fatal("stale managed readiness was accepted")
	}
}

func TestModelStorageRefreshFailureRollsBackEveryLayer(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	var hostLocal, peersLocal bool
	server := &Server{
		state: store, jobs: jobs.NewManager(),
		modelStorageManagedNFS: func() (string, error) { return "10.100.0.1", nil },
		modelStorageHostApply:  func(context.Context, modelstorage.Config) error { return nil },
		modelStorageHostLocal:  func(context.Context) error { hostLocal = true; return nil },
		modelStorageHostVerify: func(context.Context, modelstorage.Config) error { return nil },
		modelStoragePeersApply: func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
			return nil, nil
		},
		modelStoragePeersVerify: func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error) {
			return nil, nil
		},
		modelStoragePeersLocal: func(context.Context) ([]sparkcluster.ModelStorageNodeStatus, error) {
			peersLocal = true
			return nil, nil
		},
		modelStorageRefresh: func(context.Context) error { return errors.New("restart failed") },
	}
	response := httptest.NewRecorder()
	server.modelStorageNFSSet(response, httptest.NewRequest(http.MethodPost, "/api/settings/model-storage/nfs", strings.NewReader(`{"managed":true}`)))
	if response.Code != http.StatusInternalServerError || !hostLocal || !peersLocal {
		t.Fatalf("refresh rollback = %d %s; host=%v peers=%v", response.Code, response.Body.String(), hostLocal, peersLocal)
	}
	if got := store.ModelStorageConfig(); got.Mode != modelstorage.ModeLocal {
		t.Fatalf("failed refresh persisted shared storage: %#v", got)
	}
}

func TestSharedNFSModelDistributionSkipsTransferPreparation(t *testing.T) {
	recipe := localrecipes.Recipe{Model: localrecipes.Model{ID: "example/model", Revision: "revision"}}
	manifest := recipeArtifactManifest{ModelID: recipe.Model.ID, Revision: recipe.Model.Revision, Bytes: 1, Digest: strings.Repeat("a", 64)}
	job := jobs.NewManager().Create("recipe:test")
	if _, err := distributeRecipeModel(context.Background(), nil, job, recipe, "", nil, nil, manifest, true); err != nil {
		t.Fatalf("shared storage entered the transfer path: %v", err)
	}
	snapshot := job.Snapshot()
	if snapshot.Phase != "verifying-peer-model" || !strings.Contains(snapshot.Message, "without copying") {
		t.Fatalf("shared model distribution progress = %#v", snapshot)
	}
}
