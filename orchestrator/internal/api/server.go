// Package api exposes the orchestrator's local HTTP API and serves the web UI.
package api

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/capabilities"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/customengine"
	"github.com/cloudless/orchestrator/internal/distributedprofiles"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/modelstorage"
	"github.com/cloudless/orchestrator/internal/places"
	"github.com/cloudless/orchestrator/internal/power"
	"github.com/cloudless/orchestrator/internal/privileged"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/remoteaccess"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
	"github.com/cloudless/orchestrator/internal/state"
	"github.com/cloudless/orchestrator/internal/usage"
)

//go:embed all:web
var webFS embed.FS

// Server wires the container engine, job manager, and state store to HTTP handlers.
type Server struct {
	eng                  engine.Engine
	jobs                 *jobs.Manager
	state                *state.Store
	manifest             *manifest.Store
	mfModels             *manifest.ModelsStore
	mfDiff               *manifest.DiffusionStore
	usage                *usage.Store
	power                *power.Store
	shutdown             func() error
	reboot               func() error
	privilegedAction     func(context.Context, privileged.Action) error
	privilegedValue      func(context.Context, privileged.Action, string) error
	gatewayRebind        func(int) error
	gatewaySecurityMu    sync.Mutex
	gatewayLimiter       *gatewayRateLimiter
	gatewaySourceLimiter *gatewayRateLimiter
	gatewayAudit         *gatewayAuditLog
	virtualKey           func(context.Context, string, string) error
	virtualKeyMu         sync.Mutex
	displayQuery         func(context.Context) (displaySnapshot, error)
	displayApply         func(context.Context, string, string, int, int) error
	displayMu            sync.Mutex
	displayChange        *pendingDisplayChange
	displayDelay         time.Duration
	displayRecoveryDelay time.Duration

	shutdownMu     sync.Mutex
	shutdownQueued bool
	shutdownDelay  time.Duration

	modelsMu     sync.Mutex
	modelsHave   map[string]bool
	modelsHaveAt time.Time

	clusterViewMu         sync.Mutex
	clusterView           clusterComputeView
	clusterViewAt         time.Time
	clusterViewReady      bool
	clusterViewRefreshing bool
	clusterComputeProbe   func(context.Context, int) clusterComputeView

	modelJobsMu sync.Mutex
	modelJobs   map[string]context.CancelFunc
	// modelJobRevisions preserves the exact immutable Hub revision associated
	// with each process-local download job. The durable copy lives in state.
	modelJobRevisions map[string]string

	engineJobsMu sync.Mutex
	engineJobs   map[string]context.CancelFunc
	recipeJobsMu sync.Mutex
	recipeJobs   map[string]recipeJobControl
	recipeGCMu   sync.Mutex
	recipeGCLast time.Time

	appOperations  appOperationLocks // serialize only operations that touch the same app
	recipes        *localrecipes.Store
	recipeOps      *recipeops.Store
	recipeOpsErr   error
	recipeFailures recipeFailureInjector
	// recipeRunPreflight is an internal-only seam for deterministic admission
	// tests. Production leaves it nil and recomputes the current machine and
	// cluster identity before any recipe preparation is allowed to begin.
	recipeRunPreflight      func(context.Context, localrecipes.Recipe, recipeops.Operation) error
	modelStorageRoot        string
	modelStorageManagedRoot string
	modelStorageReadyPath   string
	modelStorageHostApply   func(context.Context, modelstorage.Config) error
	modelStorageHostLocal   func(context.Context) error
	modelStorageHostVerify  func(context.Context, modelstorage.Config) error
	modelStoragePeersApply  func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error)
	modelStoragePeersLocal  func(context.Context) ([]sparkcluster.ModelStorageNodeStatus, error)
	modelStoragePeersVerify func(context.Context, modelstorage.Config) ([]sparkcluster.ModelStorageNodeStatus, error)
	modelStorageRefresh     func(context.Context) error
	modelStorageManagedNFS  func() (string, error)
	// recipeRevalidate is an internal-only seam for the lifecycle failure
	// matrix. Production servers leave it nil and always execute the complete
	// hardware/topology/content revalidation.
	recipeRevalidate func(context.Context, localrecipes.Recipe, recipeops.Operation, string, map[string]string, recipeModelEvidence) error
	// appHealthCheck is an internal-only seam for deterministic lifecycle tests.
	// Production leaves it nil and performs the real container/HTTP checks.
	appHealthCheck func(context.Context, catalog.App) error
	// appConfigure is the matching post-install seam. Production leaves it nil
	// and executes each app's real integration hook.
	appConfigure      func(context.Context, *jobs.Job, catalog.App) error
	remoteAccess      remoteaccess.Service
	accountBaseURL    string
	accountHTTPClient *http.Client
	runtimeInstanceID string
}

// NewServer constructs a Server backed by the given engine, state store and manifests.
func NewServer(eng engine.Engine, st *state.Store, mf *manifest.Store, mfModels *manifest.ModelsStore, mfDiff *manifest.DiffusionStore, us *usage.Store, pw *power.Store) *Server {
	recipeOps, recipeOpsErr := recipeops.Open(st.Dir())
	privilegedClient := privileged.Client{SocketPath: os.Getenv("CLOUDLESS_PRIVILEGED_SOCKET")}
	runtimeInstanceID := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	server := &Server{
		eng: eng, jobs: jobs.NewManager(), state: st, manifest: mf, mfModels: mfModels,
		mfDiff: mfDiff, usage: us, power: pw,
		shutdown:         func() error { return privilegedClient.Do(context.Background(), privileged.ActionPowerOff) },
		reboot:           func() error { return privilegedClient.Do(context.Background(), privileged.ActionReboot) },
		privilegedAction: privilegedClient.Do,
		privilegedValue:  privilegedClient.DoValue,
		virtualKey:       emitSystemVirtualKey,
		shutdownDelay:    time.Second, displayDelay: 20 * time.Second, displayRecoveryDelay: time.Second,
		recipes: localrecipes.New(st.Dir()), remoteAccess: remoteaccess.New(),
		recipeOps: recipeOps, recipeOpsErr: recipeOpsErr,
		gatewayLimiter: newGatewayRateLimiter(), gatewaySourceLimiter: newGatewayRateLimiterWith(120, 30),
		gatewayAudit: newGatewayAuditLog(st.Dir()), runtimeInstanceID: runtimeInstanceID,
	}
	if err := prepareRuntimeRestartOffer(st, runtimeInstanceID); err != nil {
		log.Printf("runtime restart offer: %v", err)
	}
	if recipeOps != nil && recipeOpsErr == nil {
		if err := recipeOps.Prune(200, 90*24*time.Hour); err != nil {
			log.Printf("[recipe-operations] prune history: %v", err)
		}
	}
	server.startRecipeRecovery()
	server.startModelDownloadRecovery()
	server.recoverPendingDisplayChange()
	return server
}

// prepareRuntimeRestartOffer distinguishes a daemon-only restart from a device
// restart. Package upgrades restart cloudlessd while leaving the managed
// inference endpoint running; presenting a restart offer in that case would
// incorrectly mark a healthy runtime as unloaded. A cold device restart has no
// reachable stable endpoint, so it continues through the normal manual or
// automatic recovery flow.
func prepareRuntimeRestartOffer(st *state.Store, instanceID string) error {
	current := st.Get()
	runtime := current.InferenceRuntime()
	hasRuntimeIdentity := strings.TrimSpace(runtime.Engine) != "" || strings.TrimSpace(runtime.Model) != "" || strings.TrimSpace(runtime.LocalRecipeID) != ""
	if !current.EngineUnloaded && current.InferenceOperation.ID == "" && hasRuntimeIdentity {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := engineEndpointError(ctx)
		cancel()
		if err == nil {
			log.Printf("runtime restart reconciliation: stable inference endpoint is already healthy; preserving active runtime")
			return nil
		}
	}
	return st.PrepareRuntimeRestartOffer(instanceID)
}

// SetGatewayRebind wires the daemon's listener manager into the settings API.
// Tests and embedded callers may omit it when they never change the API port.
func (s *Server) SetGatewayRebind(rebind func(int) error) { s.gatewayRebind = rebind }

// imageFor returns the image reference to pull/run for an app: the manifest's
// validated digest pin ("image@sha256:…") when present, else the catalog's tag.
func (s *Server) imageFor(ctx context.Context, app catalog.App) string {
	if s.manifest != nil {
		if p, ok := s.manifest.PinFor(ctx, app.ID, app.Image); ok {
			return p.Ref()
		}
	}
	return app.Image
}

// managedEngineSpec applies the signed image pin to a built-in inference
// engine without redirecting a model-specific or user-built runtime. Launches
// and Update Center replacements therefore use the same reviewed artifact.
func (s *Server) managedEngineSpec(ctx context.Context, app catalog.App, model string, override []string) engine.RunSpec {
	spec := catalog.EngineSpecOverride(app, model, override)
	// Inference recovery is a Cloudless policy decision. Docker must not
	// independently resurrect a model after the user chooses to keep it off.
	spec.RestartPolicy = "no"
	if !customengine.IsCustom(app.ID) && spec.Image == app.Image {
		spec.Image = s.imageFor(ctx, app)
	}
	return spec
}

// infraImage resolves an infra image (cloudflared/socat) to the manifest pin, else fallback.
func (s *Server) infraImage(ctx context.Context, key, fallback string) string {
	if s.manifest != nil {
		if p, ok := s.manifest.PinFor(ctx, key, fallback); ok {
			return p.Ref()
		}
	}
	return fallback
}

// Routes returns the configured HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/account/signup", s.accountSignup)
	mux.HandleFunc("POST /api/account/login", s.accountLogin)
	mux.HandleFunc("POST /api/account/refresh", s.accountRefresh)
	mux.HandleFunc("GET /api/account/me", s.accountCurrent)
	mux.HandleFunc("POST /api/account/logout", s.accountLogout)
	mux.HandleFunc("GET /api/account/oauth/{provider}", s.accountOAuth)
	mux.HandleFunc("POST /api/account/picture", s.accountPicture)
	mux.HandleFunc("GET /api/account/publisher-keys", s.accountPublisherKeys)
	mux.HandleFunc("POST /api/account/publisher-keys", s.accountPublisherKeys)
	mux.HandleFunc("DELETE /api/account/publisher-keys/{id}", s.accountPublisherKeys)
	mux.Handle("GET /terminal/", cloudlessTerminalProxy())
	mux.HandleFunc("GET /api/gpu", s.gpu)
	mux.HandleFunc("GET /api/system", s.system)
	mux.HandleFunc("GET /api/system/boot-health", s.bootHealth)
	mux.HandleFunc("GET /api/system/input", s.systemInput)
	mux.HandleFunc("POST /api/system/input/key", s.systemInputKey)
	mux.HandleFunc("GET /api/capabilities", s.capabilities)
	mux.HandleFunc("POST /api/system/shutdown", s.systemShutdown)
	mux.HandleFunc("POST /api/system/reboot", s.systemReboot)
	mux.HandleFunc("GET /api/system/browser", s.systemBrowserStatus)
	mux.HandleFunc("POST /api/system/browser", s.systemBrowserOpen)
	mux.HandleFunc("GET /api/system/tailscale", s.tailscaleStatus)
	mux.HandleFunc("POST /api/system/tailscale/install", s.tailscaleInstall)
	mux.HandleFunc("POST /api/system/tailscale/connect", s.tailscaleConnect)
	mux.HandleFunc("POST /api/system/tailscale/logout", s.tailscaleLogout)
	mux.HandleFunc("POST /api/system/tailscale/ssh", s.tailscaleSSH)
	mux.HandleFunc("POST /api/system/tailscale/serve", s.tailscaleServe)
	mux.HandleFunc("GET /api/system/display", s.displayGet)
	mux.HandleFunc("POST /api/system/display", s.displaySet)
	mux.HandleFunc("POST /api/system/display/normalize", s.displayNormalize)
	mux.HandleFunc("POST /api/system/display/confirm", s.displayConfirm)
	mux.HandleFunc("POST /api/system/display/revert", s.displayRevert)
	mux.HandleFunc("GET /api/system/update", s.systemUpdateGet)
	mux.HandleFunc("POST /api/system/update/check", s.systemUpdateCheck)
	mux.HandleFunc("POST /api/system/update/apply", s.systemUpdateApply)
	mux.HandleFunc("POST /api/system/update/channel", s.systemUpdateChannel)
	mux.HandleFunc("GET /api/updates", s.updateCenterGet)
	mux.HandleFunc("POST /api/updates/check", s.updateCenterCheck)
	mux.HandleFunc("POST /api/updates/apps/apply", s.updateCenterAppsApply)
	mux.HandleFunc("POST /api/updates/engines/{id}/apply", s.updateCenterEngineApply)
	mux.HandleFunc("GET /api/system/nvidia-driver", s.nvidiaDriverGet)
	mux.HandleFunc("POST /api/system/nvidia-driver/check", s.nvidiaDriverCheck)
	mux.HandleFunc("POST /api/system/nvidia-driver/apply", s.nvidiaDriverApply)
	mux.HandleFunc("GET /api/system/doctor", s.systemDoctor)
	mux.HandleFunc("POST /api/system/doctor/run", s.systemDoctor)
	mux.HandleFunc("POST /api/system/doctor/bundle", s.systemDoctorBundle)
	mux.HandleFunc("GET /api/system/spark-cluster", s.sparkClusterStatus)
	mux.HandleFunc("POST /api/system/spark-cluster/discover", s.sparkClusterDiscover)
	mux.HandleFunc("POST /api/system/spark-cluster/preflight", s.sparkClusterPreflight)
	mux.HandleFunc("POST /api/system/spark-cluster/create", s.sparkClusterCreate)
	mux.HandleFunc("POST /api/system/spark-cluster/selection", s.sparkClusterSelection)
	mux.HandleFunc("POST /api/system/spark-cluster/rebind", s.sparkClusterRebind)
	mux.HandleFunc("POST /api/system/spark-cluster/disconnect", s.sparkClusterDisconnect)
	mux.HandleFunc("GET /api/sysload", s.sysload)
	mux.HandleFunc("GET /api/profile", s.profileGet)
	mux.HandleFunc("POST /api/profile", s.profileSet)
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("GET /api/packs", s.packsList)
	mux.HandleFunc("POST /api/packs/{id}/install", s.packInstall)
	mux.HandleFunc("POST /api/packs/{id}/uninstall", s.packUninstall)
	mux.HandleFunc("GET /api/apps", s.apps)
	mux.HandleFunc("POST /api/apps/{id}/start", s.start)
	mux.HandleFunc("POST /api/apps/{id}/stop", s.stop)
	mux.HandleFunc("POST /api/apps/{id}/remove", s.remove)
	mux.HandleFunc("POST /api/apps/{id}/reset", s.appReset)
	mux.HandleFunc("POST /api/apps/{id}/uninstall", s.appUninstall)
	mux.HandleFunc("GET /api/apps/{id}/config", s.appConfigGet)
	mux.HandleFunc("POST /api/apps/{id}/config", s.appConfigSet)
	mux.HandleFunc("POST /api/apps/{id}/config/reset", s.appConfigReset)
	mux.HandleFunc("GET /api/apps/{id}/settings", s.appSettingsGet)
	mux.HandleFunc("POST /api/apps/{id}/settings", s.appSettingsSet)
	mux.HandleFunc("GET /api/jobs", s.jobList)
	mux.HandleFunc("GET /api/jobs/{id}", s.jobState)
	mux.HandleFunc("GET /api/jobs/{id}/events", s.jobEvents)
	mux.HandleFunc("GET /api/settings", s.settingsGet)
	mux.HandleFunc("POST /api/settings/model", s.settingsModel)
	mux.HandleFunc("POST /api/settings/inference-contract", s.inferenceContractSet)
	mux.HandleFunc("GET /api/settings/model-storage", s.modelStorageGet)
	mux.HandleFunc("POST /api/settings/model-storage/nfs", s.modelStorageNFSSet)
	mux.HandleFunc("POST /api/settings/model-storage/local", s.modelStorageLocalSet)
	mux.HandleFunc("GET /api/models", s.modelsList)
	mux.HandleFunc("GET /api/models/huggingface/search", s.huggingFaceSearch)
	mux.HandleFunc("POST /api/models/huggingface/import", s.huggingFaceImport)
	mux.HandleFunc("GET /api/models/huggingface/account", s.huggingFaceAccount)
	mux.HandleFunc("POST /api/models/huggingface/account", s.huggingFaceConnect)
	mux.HandleFunc("DELETE /api/models/huggingface/account", s.huggingFaceDisconnect)
	mux.HandleFunc("GET /api/models/downloads", s.modelDownloads)
	mux.HandleFunc("POST /api/models/download", s.modelDownload)
	mux.HandleFunc("POST /api/models/download/cancel", s.modelDownloadCancel)
	mux.HandleFunc("POST /api/models/uninstall", s.modelUninstall)
	mux.HandleFunc("GET /api/recipes", s.localRecipesList)
	mux.HandleFunc("GET /api/recipes/cache", s.localRecipeCacheStatus)
	mux.HandleFunc("POST /api/recipes/cache/cleanup", s.localRecipeCacheCleanup)
	mux.HandleFunc("POST /api/recipes", s.localRecipeCreate)
	mux.HandleFunc("POST /api/recipes/import/preview", s.localRecipeImportPreview)
	mux.HandleFunc("POST /api/recipes/import", s.localRecipeImport)
	mux.HandleFunc("DELETE /api/recipes/{id}", s.localRecipeDelete)
	mux.HandleFunc("PUT /api/recipes/{id}", s.localRecipeUpdate)
	mux.HandleFunc("GET /api/recipes/{id}/source", s.localRecipeSource)
	mux.HandleFunc("GET /api/recipes/{id}/inventory", s.localRecipeInventory)
	mux.HandleFunc("GET /api/recipes/{id}/diagnostics/{operation}", s.localRecipeDiagnostics)
	mux.HandleFunc("POST /api/recipes/{id}/cleanup/{operation}", s.localRecipeCleanupRetry)
	mux.HandleFunc("POST /api/recipes/{id}/check", s.localRecipeCheck)
	mux.HandleFunc("POST /api/recipes/{id}/run", s.localRecipeRun)
	mux.HandleFunc("POST /api/recipes/{id}/abort", s.localRecipeAbort)
	mux.HandleFunc("POST /api/recipes/{id}/stop", s.localRecipeStop)
	mux.HandleFunc("POST /api/community/install", s.communityRecipeInstall)
	mux.HandleFunc("POST /api/community/rollback", s.communityRecipeRollback)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		mux.HandleFunc(method+" /api/community/recipes", s.communityRecipes)
		mux.HandleFunc(method+" /api/community/recipes/{rest...}", s.communityRecipes)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		mux.HandleFunc(method+" /api/community/moderation", s.communityModeration)
		mux.HandleFunc(method+" /api/community/moderation/{rest...}", s.communityModeration)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		mux.HandleFunc(method+" /api/community/social", s.communitySocial)
		mux.HandleFunc(method+" /api/community/social/{rest...}", s.communitySocial)
	}
	mux.HandleFunc("GET /api/diffusion", s.diffusionList)
	mux.HandleFunc("GET /api/diffusion/downloads", s.diffusionDownloads)
	mux.HandleFunc("POST /api/diffusion/{id}/download", s.diffusionDownload)
	mux.HandleFunc("POST /api/diffusion/{id}/uninstall", s.diffusionUninstall)
	mux.HandleFunc("POST /api/onboarding/reset", s.onboardingReset)
	mux.HandleFunc("GET /api/onboarding", s.onboardingGet)
	mux.HandleFunc("POST /api/onboarding/setup", s.onboardingSetup)
	mux.HandleFunc("POST /api/onboarding/complete", s.onboardingComplete)
	mux.HandleFunc("GET /api/guidance/recipe-launch", s.recipeLaunchGuidanceGet)
	mux.HandleFunc("POST /api/guidance/recipe-launch", s.recipeLaunchGuidanceAcknowledge)
	mux.HandleFunc("GET /api/folders", s.folders)
	mux.HandleFunc("POST /api/folders/{id}/open", s.openFolder)
	mux.HandleFunc("GET /api/engine", s.engineState)
	mux.HandleFunc("POST /api/engines/custom", s.customEngineCreate)
	mux.HandleFunc("DELETE /api/engines/custom/{id}", s.customEngineDelete)
	mux.HandleFunc("POST /api/engine/load", s.engineLoad)
	mux.HandleFunc("POST /api/engine/unload", s.engineUnload)
	mux.HandleFunc("POST /api/engine/abort", s.engineAbort)
	mux.HandleFunc("GET /api/engine/metrics", s.engineMetricsHandler)
	mux.HandleFunc("GET /api/engine/usage", s.engineUsage)
	mux.HandleFunc("GET /api/engine/power", s.enginePower)
	mux.HandleFunc("DELETE /api/engine/power", s.enginePowerClear)
	mux.HandleFunc("GET /api/engine/launch", s.engineLaunchGet)
	mux.HandleFunc("POST /api/engine/launch", s.engineLaunchSet)
	mux.HandleFunc("DELETE /api/engine/launch", s.engineLaunchClear)
	mux.HandleFunc("POST /api/engine/restart", s.engineRestart)
	mux.HandleFunc("GET /api/runtime/restart-offer", s.runtimeRestartOfferGet)
	mux.HandleFunc("POST /api/runtime/restart-offer/dismiss", s.runtimeRestartOfferDismiss)
	mux.HandleFunc("GET /api/settings/runtime-restart", s.runtimeRestartSettingGet)
	mux.HandleFunc("POST /api/settings/runtime-restart", s.runtimeRestartSettingSet)
	mux.HandleFunc("POST /api/engine/{id}", s.engineSwitch)
	mux.HandleFunc("GET /api/pins", s.pinsGet)
	mux.HandleFunc("POST /api/apps/{id}/pin", s.pinToggle)
	mux.HandleFunc("GET /api/apps/{id}/tunnel", s.tunnelGet)
	mux.HandleFunc("POST /api/apps/{id}/tunnel", s.tunnelSet)
	mux.HandleFunc("GET /api/apps/{id}/lan", s.lanGet)
	mux.HandleFunc("POST /api/apps/{id}/lan", s.lanSet)
	mux.HandleFunc("GET /api/network/local", s.localNetGet)
	mux.HandleFunc("POST /api/network/local", s.localNetSet)
	mux.HandleFunc("GET /api/network/status", s.networkStatus)
	mux.HandleFunc("GET /api/gateway", s.gatewayGet)
	mux.HandleFunc("POST /api/keys", s.keyCreate)
	mux.HandleFunc("DELETE /api/keys/{id}", s.keyDelete)
	mux.HandleFunc("POST /api/gateway/lan", s.gatewayLanSet)
	mux.HandleFunc("POST /api/gateway/tailnet", s.gatewayTailnetSet)
	mux.HandleFunc("POST /api/gateway/tunnel", s.gatewayTunnelSet)
	mux.HandleFunc("POST /api/gateway/metrics", s.gatewayMetricsSet)
	mux.HandleFunc("GET /api/gateway/audit", s.gatewayAuditGet)
	mux.HandleFunc("GET /api/security/audit", s.securityAuditGet)
	mux.HandleFunc("GET /api/apps/{id}/update", s.updateGet)
	mux.HandleFunc("POST /api/apps/{id}/update", s.updateApply)
	mux.HandleFunc("POST /api/assistant/chat", s.assistantChat)
	mux.HandleFunc("GET /api/hermes", s.hermesStatus)
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		mux.HandleFunc(method+" /apps/{id}/", s.appViewProxy)
	}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed web assets: %v", err)
	}
	ui := http.FileServer(http.FS(sub))
	mux.Handle("GET /auth/callback", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		clone := r.Clone(r.Context())
		clone.URL.Path = "/"
		ui.ServeHTTP(w, clone)
	}))
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CloudlessOS updates replace the embedded interface in cloudlessd.
		// Never let the long-running kiosk reuse HTML or JavaScript from the
		// previous package after the daemon restarts.
		w.Header().Set("Cache-Control", "no-store")
		ui.ServeHTTP(w, r)
	}))

	return logging(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	dockerErr := s.eng.Available(ctx)
	resp := map[string]any{"status": "ok", "dockerOK": dockerErr == nil}
	if dockerErr != nil {
		resp["dockerError"] = dockerErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) capabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, capabilities.Current())
}

// requireCapability is the server-side enforcement boundary for feature-gated
// operations. Frontend visibility is convenience only; every privileged
// feature endpoint must call this helper.
func (s *Server) requireCapability(w http.ResponseWriter, id string) bool {
	snapshot := capabilities.Current()
	status, ok := snapshot.Features[id]
	if ok && status.Available {
		return true
	}
	reason := status.Reason
	if reason == "" {
		reason = "This feature is not available on this CloudlessOS platform"
	}
	writeJSON(w, http.StatusConflict, map[string]string{
		"error": reason, "capability": id,
	})
	return false
}

func (s *Server) gpu(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	local, err := hardware.GPUs(ctx)
	gpus := append([]hardware.GPU(nil), local...)
	hostname, _ := os.Hostname()
	for i := range gpus {
		gpus[i].Node = hostname
	}
	peers, _ := sparkcluster.ClusterGPUs(ctx)
	for _, peer := range peers {
		if peer.Reachable {
			gpus = append(gpus, peer.GPUs...)
		}
	}
	firstPeer := sparkcluster.PeerTelemetry{}
	if len(peers) > 0 {
		firstPeer = peers[0]
	}
	resp := map[string]any{"available": len(gpus) > 0, "gpus": gpus, "peers": peers, "peer": firstPeer}
	if err != nil && len(gpus) == 0 {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) folders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, places.List())
}

// activeEngine returns the id of the currently-running inference engine, or "".
// It lists containers ONCE (one `docker ps`) and scans, rather than calling Find
// per engine — this runs on every /api/engine poll, so the extra forks add up.
func (s *Server) activeEngine(ctx context.Context) string {
	all, err := s.eng.List(ctx)
	if err != nil {
		return ""
	}
	running := make(map[string]bool, len(all))
	for _, c := range all {
		if c.State == "running" {
			running[c.Name] = true
		}
	}
	for _, e := range customengine.All(s.state) {
		if running[e.ContainerName()] {
			return e.ID
		}
	}
	if current := s.state.Get(); current.LocalRecipeID != "" && !current.EngineUnloaded && running["cloudless-cluster-engine-proxy"] {
		return current.Engine
	}
	return ""
}

func engineModelsResponseError(body io.Reader) error {
	var modelsResponse struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(body).Decode(&modelsResponse); err != nil {
		return fmt.Errorf("invalid OpenAI models response: %w", err)
	}
	for _, model := range modelsResponse.Data {
		if model.ID == localrecipes.CloudlessModelAlias {
			return nil
		}
	}
	return fmt.Errorf("does not serve the required model name %q", localrecipes.CloudlessModelAlias)
}

// engineEndpointError validates the entire stable inference contract: the
// permanent port must be reachable, OpenAI-compatible, and serve Cloudless's
// internal model identity. This is shared by recipes, bundled engines and
// locally-built custom engines.
func engineEndpointError(ctx context.Context) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/models", recipeStablePort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode)
	}

	if err := engineModelsResponseError(resp.Body); err != nil {
		return fmt.Errorf("%s %w", url, err)
	}
	return nil
}

// engineToolContractError verifies the request shape used by Hermes. A plain
// /v1/models probe cannot detect a broken structured-output dependency because
// vLLM imports its tool parser only when a request contains tools.
func engineToolContractError(ctx context.Context) error {
	return engineToolContractErrorAt(ctx, fmt.Sprintf("http://127.0.0.1:%d", recipeStablePort))
}

func engineToolContractErrorAt(ctx context.Context, endpoint string) error {
	payload, err := json.Marshal(map[string]any{
		"model":       localrecipes.CloudlessModelAlias,
		"messages":    []map[string]string{{"role": "user", "content": "Reply with READY."}},
		"max_tokens":  1,
		"temperature": 0,
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "cloudless_runtime_probe",
				"description": "Validates the local tool-calling runtime.",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		}},
		"tool_choice": "auto",
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%s returned HTTP %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func engineReady(ctx context.Context) bool {
	return engineEndpointError(ctx) == nil
}

func (s *Server) engineState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	active := s.activeEngine(ctx)
	currentState := s.state.Get()
	type eng struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		Active           bool   `json:"active"`
		Selected         bool   `json:"selected"`
		Custom           bool   `json:"custom,omitempty"`
		SupportLevel     string `json:"supportLevel"`
		Image            string `json:"image,omitempty"`
		Base             string `json:"base,omitempty"`
		Architecture     string `json:"architecture,omitempty"`
		ContractVersion  string `json:"contractVersion,omitempty"`
		ValidationStatus string `json:"validationStatus,omitempty"`
		LastValidated    string `json:"lastValidated,omitempty"`
		LastError        string `json:"lastError,omitempty"`
	}
	selected := currentState.Engine
	if selected == "" {
		selected = catalog.DefaultEngine()
	}
	list := []eng{}
	for _, e := range customengine.All(s.state) {
		item := eng{ID: e.ID, Name: e.Name, Active: e.ID == active, Selected: e.ID == selected, Custom: customengine.IsCustom(e.ID), SupportLevel: e.SupportLevel}
		if item.Custom {
			for _, def := range currentState.CustomEngines {
				if def.ID == e.ID {
					item.Image = def.Image
					item.Base = def.Base
					item.Architecture = def.Architecture
					item.ContractVersion = def.ContractVersion
					item.ValidationStatus = def.ValidationStatus
					item.LastValidated = def.LastValidated
					item.LastError = def.LastError
					break
				}
			}
		}
		list = append(list, item)
	}
	var startup *jobs.Snapshot
	startupSequence := -1
	for _, prefix := range []string{"engine:", "model:", "recipe:"} {
		for _, snapshot := range s.jobs.List(prefix) {
			if !engineStartupCandidate(snapshot) {
				continue
			}
			sequence, _ := strconv.Atoi(strings.TrimPrefix(snapshot.ID, "job-"))
			if !snapshot.Done && sequence > startupSequence {
				copy := snapshot
				startup = &copy
				startupSequence = sequence
			}
		}
	}
	clusterView := s.cachedClusterCompute(ctx, totalVRAMGB(ctx))
	endpointReady := active != "" && engineReady(ctx)
	ready, clusterDegraded := clusterRuntimeReady(currentState.ExecutionMode, endpointReady, clusterView)
	durableOperation := currentState.InferenceOperation
	if startup == nil && durableOperation.ID != "" && durableOperation.Phase != "error" {
		showRecovered := durableOperation.Action == "unload" || durableOperation.Action == "abort" || !ready
		if showRecovered {
			nodes := make([]jobs.NodeProgress, 0, len(durableOperation.Nodes))
			for _, node := range durableOperation.Nodes {
				nodes = append(nodes, jobs.NodeProgress{
					Node: node.Node, Phase: node.Phase, Message: node.Message,
					BytesDone: node.BytesDone, BytesTotal: node.BytesTotal,
					Percent: node.Percent, ETASecs: node.ETASecs,
				})
			}
			appID := "engine:" + durableOperation.TargetEngine
			switch durableOperation.Action {
			case "unload":
				appID = "engine:unload"
			case "abort":
				appID = "engine:abort"
			case "model":
				appID = "model:" + durableOperation.TargetEngine
			}
			startup = &jobs.Snapshot{
				ID: durableOperation.ID, AppID: appID,
				Update: jobs.Update{
					Phase: durableOperation.Phase, Message: durableOperation.Message,
					Percent:   durableOperation.Percent,
					ItemsDone: durableOperation.ItemsDone, ItemsTotal: durableOperation.ItemsTotal,
					BytesDone: durableOperation.BytesDone, BytesTotal: durableOperation.BytesTotal,
					Error:     durableOperation.Error,
					StartedAt: durableOperation.Started, UpdatedAt: durableOperation.Updated,
					Nodes: nodes,
				},
			}
		}
	}
	if startup == nil && active != "" && !ready {
		if app, ok := customengine.Get(s.state, active); ok {
			if logs, err := s.eng.Logs(ctx, app.ContainerName()); err == nil {
				done, total := modelCheckpointProgress(logs)
				message := "Loading the model across the available hardware …"
				phase := "loading"
				var bytesDone, bytesTotal int64
				if currentState.ExecutionMode == "cluster" {
					modelID := resolveModel(currentState.Model)
					probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
					peers, peerErr := sparkcluster.ClusterModelProgress(probeCtx, modelID)
					probeCancel()
					var peerBytes int64
					var downloading, loading []string
					allLoaded := len(peers) > 0
					for _, peer := range peers {
						peerBytes += peer.Bytes
						if peer.Incomplete > 0 {
							downloading = append(downloading, peer.Node)
						}
						if !peer.WeightsLoaded {
							allLoaded = false
							if peer.Incomplete == 0 {
								loading = append(loading, peer.Node)
							}
						}
					}
					if peerErr == nil && len(downloading) > 0 {
						phase = "peer-downloading"
						bytesDone = peerBytes
						bytesTotal = s.modelRepoBytes(ctx, modelID, modelcache.Root()) * int64(len(peers))
						message = peerDownloadStatus(strings.Join(downloading, ", "), modelID, bytesDone, bytesTotal, 0)
					} else if peerErr == nil && !allLoaded {
						phase = "peer-loading"
						message = strings.Join(loading, ", ") + " are loading model weights …"
					} else if peerErr == nil && allLoaded && total > 0 && done >= total {
						phase = "optimizing"
						message = fmt.Sprintf("All %d Sparks loaded the model. Optimizing distributed inference …", len(peers)+1)
					}
				}
				if phase == "loading" && total > 0 && done < total {
					message = fmt.Sprintf("Loading model weights — %d of %d checkpoint shards", done, total)
				} else if phase == "loading" && total > 0 && currentState.ExecutionMode != "cluster" {
					message = "Model weights loaded. Optimizing the inference engine …"
					phase = "optimizing"
				}
				startup = &jobs.Snapshot{AppID: "engine:" + active, Update: jobs.Update{Phase: phase, Message: message, LayersDone: done, LayersTotal: total, BytesDone: bytesDone, BytesTotal: bytesTotal}}
			}
		}
	}
	operation, operationJobID, canAbort := describeEngineOperation(startup, ready, currentState.EngineUnloaded)
	if clusterDegraded {
		operation, operationJobID, canAbort = describeClusterFailureOperation(durableOperation)
		errorMessage := "The model endpoint may still answer, but Cloudless cannot prove that every selected node and fabric path is healthy. Open DGX Spark Cluster, recover the failed node, then reload the model."
		if canAbort {
			errorMessage = "The model operation is still active, but Cloudless cannot prove that every selected node and fabric path is healthy. Abort loading or recover the failed Spark before continuing."
		}
		startup = &jobs.Snapshot{
			ID:    operationJobID,
			AppID: "engine:" + active,
			Update: jobs.Update{
				Phase: "error", Done: true,
				Message: "Distributed inference lost a selected Spark.",
				Error:   errorMessage,
			},
		}
	}
	if durableOperation.ID != "" && durableOperation.Phase == "error" && !ready {
		operation, operationJobID, canAbort = "error", durableOperation.ID, false
		if startup == nil {
			startup = &jobs.Snapshot{
				ID: durableOperation.ID, AppID: "engine:" + durableOperation.TargetEngine,
				Update: jobs.Update{
					Phase: "error", Message: durableOperation.Message,
					Error: durableOperation.Error, Done: true,
					StartedAt: durableOperation.Started, UpdatedAt: durableOperation.Updated,
				},
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active":           active,
		"ready":            ready,
		"unloaded":         currentState.EngineUnloaded,
		"engines":          list,
		"startup":          startup,
		"promotion":        currentState.ModelPromotion,
		"operationJournal": durableOperation,
		"operation":        operation,
		"operationJobId":   operationJobID,
		"canAbort":         canAbort,
		"cluster":          clusterView,
		"degraded":         clusterDegraded,
		"executionMode": func() string {
			if currentState.ExecutionMode == "cluster" {
				return "cluster"
			}
			return "local"
		}(),
	})
}

// describeEngineOperation turns the job manager's implementation details into a
// stable UI contract. Readiness wins over a launch job that is about to publish
// its final success update, while unload/abort jobs remain explicit operations.
func describeEngineOperation(startup *jobs.Snapshot, ready, _ bool) (operation, jobID string, canAbort bool) {
	if startup != nil && !startup.Done {
		switch startup.AppID {
		case "engine:unload":
			return "unloading", startup.ID, false
		case "engine:abort":
			return "aborting", startup.ID, false
		}
		if strings.HasPrefix(startup.AppID, "recipe:") {
			if strings.HasSuffix(startup.AppID, ":stop") || startup.Phase == "stopping" {
				return "unloading", startup.ID, false
			}
			if !ready && !strings.HasSuffix(startup.AppID, ":check") {
				return "loading", startup.ID, false
			}
		}
		if !ready && (strings.HasPrefix(startup.AppID, "engine:") || strings.HasPrefix(startup.AppID, "model:")) {
			return "loading", startup.ID, startup.ID != ""
		}
	}
	return "idle", "", false
}

func engineStartupCandidate(snapshot jobs.Snapshot) bool {
	if snapshot.Done {
		return false
	}
	if strings.HasPrefix(snapshot.AppID, "recipe:") {
		return !strings.HasSuffix(snapshot.AppID, ":check")
	}
	return strings.HasPrefix(snapshot.AppID, "engine:") || strings.HasPrefix(snapshot.AppID, "model:")
}

// describeClusterFailureOperation preserves the user's recovery action when a
// selected Spark disappears during a durable lifecycle operation. The cluster
// is still reported as degraded, but an interrupted load/switch/restart remains
// abortable even when the original in-memory job disappeared after a restart.
func describeClusterFailureOperation(operation state.InferenceOperation) (uiOperation, jobID string, canAbort bool) {
	if !inferenceOperationBlocksClusterMutation(operation) {
		return "error", operation.ID, false
	}
	switch operation.Action {
	case "abort":
		return "aborting", operation.ID, false
	case "unload":
		return "unloading", operation.ID, false
	default:
		return "loading", operation.ID, true
	}
}

// engineUnload releases accelerator memory without deleting the selected model
// or its downloaded weights. The persisted unloaded flag prevents provisioning
// from silently loading it again after a daemon restart.
func (s *Server) engineUnload(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "model-unload" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "model unload confirmation header required"})
		return
	}
	job := s.jobs.Create("engine:unload")
	s.observeInferenceJob(job, "unload", "", s.state.Get().InferenceRuntime())
	go s.runEngineUnload(job)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func (s *Server) engineLoad(w http.ResponseWriter, _ *http.Request) {
	id := s.state.Get().Engine
	if id == "" {
		id = catalog.DefaultEngine()
	}
	target, ok := customengine.Get(s.state, id)
	if !ok || !target.Engine {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the selected inference engine is unavailable"})
		return
	}
	job := s.jobs.Create("engine:" + target.ID)
	s.observeInferenceJob(job, "load", target.ID, s.state.Get().InferenceRuntime())
	go s.applyEngine(job, target)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "engine": target.ID})
}

// engineAbort cancels every queued/running engine launch, then uses the same
// cleanup path as an explicit unload. Cancelling all registered launches is
// intentional: a second queued launch must not start after the user presses
// Abort and unexpectedly consume accelerator memory again.
func (s *Server) engineAbort(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Cloudless-Action") != "model-abort" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "model abort confirmation header required"})
		return
	}
	previous := s.state.Get()
	canceled := s.cancelEngineJobs()
	if canceled == 0 && (previous.InferenceOperation.ID == "" || previous.InferenceOperation.Phase == "error") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no model launch is currently active"})
		return
	}
	job := s.jobs.Create("engine:abort")
	s.observeInferenceJob(job, "abort", "", previous.InferenceRuntime())
	go s.runEngineUnload(job)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func (s *Server) registerEngineJob(jobID string, cancel context.CancelFunc) {
	s.engineJobsMu.Lock()
	defer s.engineJobsMu.Unlock()
	if s.engineJobs == nil {
		s.engineJobs = make(map[string]context.CancelFunc)
	}
	s.engineJobs[jobID] = cancel
}

func (s *Server) unregisterEngineJob(jobID string) {
	s.engineJobsMu.Lock()
	delete(s.engineJobs, jobID)
	s.engineJobsMu.Unlock()
}

func (s *Server) cancelEngineJobs() int {
	s.engineJobsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.engineJobs))
	for _, cancel := range s.engineJobs {
		cancels = append(cancels, cancel)
	}
	s.engineJobsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

func (s *Server) observeInferenceJob(job *jobs.Job, action, targetEngine string, previous state.InferenceRuntime) {
	current := s.state.Get()
	operation := state.InferenceOperation{
		ID: job.ID, Action: action, TargetEngine: targetEngine,
		TargetModel: resolveModel(current.Model), TargetMode: current.ExecutionMode,
		Previous: previous, Phase: "pending", Message: "Queued",
	}
	if err := s.state.BeginInferenceOperation(operation); err != nil {
		log.Printf("engine: persist %s operation: %v", action, err)
	}
	s.auditSecurity(gatewayAuditEvent{
		Category: "engine", Event: action, Outcome: "started", Actor: "local-ui",
		Target: targetEngine, OperationID: job.ID,
	})
	var auditOnce sync.Once
	job.Observe(func(update jobs.Update) {
		if update.Done {
			auditOnce.Do(func() {
				outcome := "succeeded"
				if update.Error != "" || update.Phase == "error" {
					outcome = "failed"
				}
				s.auditSecurity(gatewayAuditEvent{
					Category: "engine", Event: action, Outcome: outcome, Actor: "cloudlessd",
					Target: targetEngine, OperationID: job.ID, Detail: update.Error,
				})
			})
		}
		if update.Done && update.Phase != "error" {
			if _, err := s.state.ClearInferenceOperation(job.ID); err != nil {
				log.Printf("engine: clear %s operation: %v", action, err)
			}
			return
		}
		nodes := make([]state.InferenceNodeProgress, 0, len(update.Nodes))
		for _, node := range update.Nodes {
			nodes = append(nodes, state.InferenceNodeProgress{
				Node: node.Node, Phase: node.Phase, Message: node.Message,
				BytesDone: node.BytesDone, BytesTotal: node.BytesTotal,
				Percent: node.Percent, ETASecs: node.ETASecs,
			})
		}
		next := state.InferenceOperation{
			ID: job.ID, Phase: update.Phase, Message: update.Message,
			Percent: update.Percent, ItemsDone: update.ItemsDone, ItemsTotal: update.ItemsTotal,
			BytesDone: update.BytesDone, BytesTotal: update.BytesTotal,
			Error: update.Error, Nodes: nodes,
		}
		if _, err := s.state.UpdateInferenceOperation(next); err != nil {
			log.Printf("engine: update %s operation: %v", action, err)
		}
	})
}

// rollbackInferenceRuntime restores the last atomically persisted runtime after
// a failed local launch. Distributed and recipe-owned runtimes require their
// own topology/review recovery, so they are restored as selected-but-unloaded
// rather than guessed into a partially running state.
// Caller must hold provision.EngineMu.
func (s *Server) rollbackInferenceRuntime(ctx context.Context, operationID string) {
	operation := s.state.Get().InferenceOperation
	if operation.ID != operationID {
		_ = s.state.SetEngineUnloaded(true)
		return
	}
	terminal := operation
	terminal.Phase = "error"
	if terminal.Error == "" {
		terminal.Error = "the candidate inference runtime failed and Cloudless restored a safe state"
	}
	if _, err := s.state.UpdateInferenceOperation(state.InferenceOperation{
		ID: operationID, Phase: "rollback", Message: "Restoring the previous inference runtime",
	}); err != nil {
		log.Printf("engine: persist rollback phase: %v", err)
	}
	waitQualificationPhaseGate(ctx, "rollback")
	defer func() {
		if _, err := s.state.UpdateInferenceOperation(terminal); err != nil {
			log.Printf("engine: persist terminal rollback result: %v", err)
		}
	}()
	previous := operation.Previous
	safePrevious := previous
	safePrevious.EngineUnloaded = true
	if err := s.state.CommitInferenceRuntime(safePrevious); err != nil {
		log.Printf("engine: persist safe rollback target: %v", err)
		return
	}
	if previous.EngineUnloaded || previous.ExecutionMode == "cluster" || previous.LocalRecipeID != "" {
		return
	}
	engineID := previous.Engine
	if engineID == "" {
		engineID = catalog.DefaultEngine()
	}
	target, ok := customengine.Get(s.state, engineID)
	if !ok || !target.Engine {
		return
	}
	_ = s.eng.Remove(ctx, target.ContainerName())
	spec := s.managedEngineSpec(ctx, target, previous.Model, nil)
	if override, ok := s.state.EngineCmd(engineID, resolveModel(previous.Model)); ok {
		spec = s.managedEngineSpec(ctx, target, previous.Model, override)
	}
	if _, err := s.eng.Run(ctx, spec); err != nil {
		log.Printf("engine: rollback %s failed: %v", engineID, err)
		return
	}
	if err := s.state.CommitInferenceRuntime(previous); err != nil {
		log.Printf("engine: commit restored runtime: %v", err)
	}
}

func (s *Server) runEngineUnload(job *jobs.Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	provision.EngineMu.Lock()
	defer provision.EngineMu.Unlock()
	s.stopActiveLocalRecipeRuntime(context.Background(), job)

	job.Progress("stopping", "Stopping inference and releasing accelerator memory …", -1, -1)
	waitQualificationPhaseGate(ctx, "stopping")
	if s.state.Get().ExecutionMode == "cluster" {
		job.Progress("stopping", "Stopping distributed inference on the other Spark …", -1, -1)
		if err := sparkcluster.StopWorker(ctx); err != nil {
			job.Fail(err)
			return
		}
	}
	if proxy, _ := s.eng.Find(ctx, "cloudless-cluster-engine-proxy"); proxy != nil {
		if err := s.eng.Remove(ctx, proxy.Name); err != nil {
			job.Fail(err)
			return
		}
	}
	for _, candidate := range customengine.All(s.state) {
		container, err := s.eng.Find(ctx, candidate.ContainerName())
		if err != nil {
			job.Fail(err)
			return
		}
		if container != nil {
			job.Progress("stopping", "Unloading the model from "+candidate.Name+" …", -1, -1)
			if err := s.eng.Remove(ctx, container.Name); err != nil {
				job.Fail(err)
				return
			}
		}
	}
	if err := s.state.SetEngineUnloaded(true); err != nil {
		job.Fail(err)
		return
	}
	job.Succeed("")
}

// engineSwitch stops the current engine and starts the chosen one, which inherits
// the stable `cloudless-ai` alias — so every client follows automatically.
func (s *Server) engineSwitch(w http.ResponseWriter, r *http.Request) {
	previous := s.state.Get().InferenceRuntime()
	app, ok := customengine.Get(s.state, r.PathValue("id"))
	if !ok || !app.Engine {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown engine"})
		return
	}
	job := s.jobs.Create("engine:" + app.ID)
	s.observeInferenceJob(job, "switch", app.ID, previous)
	go s.runSwitch(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "engine": app.ID})
}

// engineRestart recreates the active engine container with its current catalog spec
// (e.g. to pick up SGLang's --enable-metrics). Unlike a switch, it always recreates.
func (s *Server) engineRestart(w http.ResponseWriter, r *http.Request) {
	previous := s.state.Get().InferenceRuntime()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	active := s.activeEngine(ctx)
	cancel()
	if active == "" {
		active = catalog.DefaultEngine()
	}
	app, ok := customengine.Get(s.state, active)
	if !ok || !app.Engine {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no engine"})
		return
	}
	job := s.jobs.Create("engine:" + app.ID)
	s.observeInferenceJob(job, "restart", app.ID, previous)
	go s.applyEngine(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "engine": app.ID})
}

func (s *Server) runSwitch(job *jobs.Job, target catalog.App) {
	// Already the active, ready engine? No-op.
	check, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	if s.activeEngine(check) == target.ID && engineReady(check) {
		ccancel()
		job.Succeed("")
		return
	}
	ccancel()
	s.applyEngine(job, target)
}

// applyEngine makes `target` the only running engine, with the current model and
// the stable alias, then waits until it's serving. Used by switch + model change.
func (s *Server) applyEngine(job *jobs.Job, target catalog.App) {
	s.applyEngineVerified(job, target, "", nil)
}

// applyEngineVerified binds a managed update to the exact image reference that
// was checked and downloaded. The optional verifier runs after the stable API
// is healthy but before the job is allowed to succeed.
func (s *Server) applyEngineVerified(job *jobs.Job, target catalog.App, exactImage string, verify func(context.Context) error) {
	customProfile := customengine.IsCustom(target.ID)
	if customProfile {
		_ = s.state.SetCustomEngineValidation(target.ID, "validating", "")
	}
	st := s.state.Get()
	model := st.Model
	modelID := resolveModel(model)
	modelRuntime, hasModelRuntime := models.Get(modelID)
	clusterNodes := 2
	distributed := st.ExecutionMode == "cluster" && target.ID == "vllm"
	timeout := 15 * time.Minute
	if distributed {
		timeout = 60 * time.Minute // the peer may need its first image/model download
	} else if hasModelRuntime && modelRuntime.RuntimeBuild != "" {
		timeout = 45 * time.Minute // first launch may pull and build a CUDA adapter
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	s.registerEngineJob(job.ID, cancel)
	defer s.unregisterEngineJob(job.ID)
	waitQualificationPhaseGate(ctx, "pending")
	job.Progress("preparing", "Validating the selected inference runtime and cluster contract …", -1, -1)
	waitQualificationPhaseGate(ctx, "preparing")
	var distributedProfile distributedprofiles.Profile
	distributedImage := ""
	if distributed {
		cluster := clusterCompute(ctx, totalVRAMGB(ctx))
		clusterNodes = cluster.Nodes
		if !cluster.DistributedReady || clusterNodes < 2 {
			job.Fail(errors.New("the Spark cluster is not healthy enough for distributed inference"))
			return
		}
		var err error
		distributedProfile, err = reviewedDistributedProfile(modelID, target.ID, clusterNodes)
		if err != nil {
			job.Fail(err)
			return
		}
		distributedImage, err = s.reviewedDistributedImage(ctx, target, exactImage)
		if err != nil {
			job.Fail(err)
			return
		}
	}
	// Serialize runtime preparation and engine replacement with startup.
	provision.EngineMu.Lock()
	if hasModelRuntime && modelRuntime.RuntimeBuild != "" {
		job.Progress("building-runtime", "Preparing the reviewed "+modelRuntime.Name+" runtime ...", -1, -1)
		if err := apps.EnsureBuild(ctx, s.eng, modelRuntime.RuntimeBuild, modelRuntime.RuntimeImage, func(line string) {
			log.Printf("[build %s] %s", modelRuntime.RuntimeBuild, line)
			if message := workbenchBuildMessage(modelRuntime.Name+" runtime", line); message != "" {
				job.Progress("building-runtime", message, -1, -1)
			}
		}); err != nil {
			provision.EngineMu.Unlock()
			job.Fail(fmt.Errorf("build %s runtime: %w", modelRuntime.Name, err))
			return
		}
	}
	s.stopActiveLocalRecipeRuntime(context.Background(), job)
	_ = s.state.SetEngine(target.ID) // remember the choice across restarts
	_ = s.state.SetEngineUnloaded(false)
	launchReady := false
	// Any bundled or custom engine that cannot satisfy the stable port/model
	// contract must fail closed. Without this cleanup, a failed image or command
	// leaves the selected engine persisted as loaded and the desktop spins on
	// "starting up" forever after the job itself has ended.
	defer func() {
		if launchReady {
			return
		}
		if customProfile {
			reason := job.Snapshot().Error
			if reason == "" {
				reason = "runtime did not pass the Cloudless API contract check"
			}
			_ = s.state.SetCustomEngineValidation(target.ID, "failed", reason)
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		provision.EngineMu.Lock()
		defer provision.EngineMu.Unlock()
		_ = s.eng.Remove(cleanupCtx, "cloudless-cluster-engine-proxy")
		_ = s.eng.Remove(cleanupCtx, target.ContainerName())
		if distributed {
			_ = sparkcluster.StopWorker(cleanupCtx)
		}
		s.rollbackInferenceRuntime(cleanupCtx, job.ID)
	}()
	if target.ID != "vllm" && st.ExecutionMode == "cluster" {
		distributed = false
		_ = s.state.SetExecutionMode("local")
	}
	for _, e := range customengine.All(s.state) {
		if e.ID != target.ID {
			message := "Stopping " + e.Name + " …"
			job.Progress("switching", message, -1, -1)
			_ = s.eng.Stop(ctx, e.ContainerName())
		}
	}
	_ = s.eng.Remove(ctx, target.ContainerName())
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	if !distributed {
		_ = sparkcluster.StopWorker(ctx)
	}
	startMessage := "Starting " + target.Name + " …"
	job.Progress("switching", startMessage, -1, -1)
	var runErr error
	if distributed {
		managedSpec := s.managedEngineSpec(ctx, target, model, nil)
		managedSpec.Image = distributedImage
		managedSpec, runErr = distributedprofiles.Apply(managedSpec, distributedProfile)
		if runErr != nil {
			provision.EngineMu.Unlock()
			job.Fail(runErr)
			return
		}
		spec := sparkcluster.CoordinatorSpec(managedSpec)
		job.Progress("downloading", "Preparing the reviewed model and engine artifacts on every Spark …", -1, -1)
		waitQualificationPhaseGate(ctx, "downloading")
		job.Progress("starting-workers", "Starting the coordinator on this Spark …", -1, -1)
		_, runErr = s.eng.Run(ctx, spec)
		if runErr != nil {
			provision.EngineMu.Unlock()
			job.Fail(runErr)
			return
		}
		started := time.Now()
		job.Progress("starting-workers", fmt.Sprintf("Preparing %d worker Sparks. First launch downloads may take several minutes …", clusterNodes-1), -1, -1)
		waitQualificationPhaseGate(ctx, "starting-workers")
		workerDone := make(chan error, 1)
		go func() { workerDone <- sparkcluster.StartWorker(ctx, spec.Image, modelID) }()
		ticker := time.NewTicker(10 * time.Second)
		var workerErr error
	workerWait:
		for {
			select {
			case workerErr = <-workerDone:
				break workerWait
			case <-ticker.C:
				elapsed := time.Since(started).Round(time.Second)
				job.Progress("starting-workers", fmt.Sprintf("Preparing %d worker Sparks for distributed inference (%s elapsed) …", clusterNodes-1, elapsed), -1, -1)
			case <-ctx.Done():
				workerErr = ctx.Err()
				break workerWait
			}
		}
		ticker.Stop()
		if workerErr != nil {
			provision.EngineMu.Unlock()
			job.Fail(workerErr)
			return
		}
		job.Progress("starting-workers", fmt.Sprintf("Waiting for all %d Sparks to join the inference cluster …", clusterNodes), -1, -1)
		waitCommand := fmt.Sprintf("until ray status 2>/dev/null | grep -q '/%d.0 GPU'; do sleep 2; done", clusterNodes)
		if err := s.eng.Exec(ctx, target.ContainerName(), "/bin/bash", "-lc", waitCommand); err != nil {
			runErr = err
		}
		job.Progress("starting-workers", fmt.Sprintf("All %d Sparks joined. Starting the distributed model …", clusterNodes), -1, -1)
		if err := s.eng.Exec(ctx, target.ContainerName(), "touch", "/tmp/cloudless-ray-worker"); err != nil {
			runErr = err
		}
		if runErr == nil {
			job.Progress("starting-workers", fmt.Sprintf("Connecting Cloudless apps to the %d-Spark engine …", clusterNodes), -1, -1)
			_ = s.eng.Pull(ctx, "alpine/socat:latest")
			_, runErr = s.eng.Run(ctx, sparkcluster.ProxySpec())
		}
		if runErr != nil {
			_ = sparkcluster.StopWorker(ctx)
		}
	} else {
		override, _ := s.state.EngineCmd(target.ID, modelID)
		spec := s.managedEngineSpec(ctx, target, model, override)
		if exactImage != "" {
			spec.Image = exactImage
		}
		job.Progress("downloading", "Preparing the reviewed model and engine artifacts …", -1, -1)
		waitQualificationPhaseGate(ctx, "downloading")
		_, runErr = s.eng.Run(ctx, spec)
	}
	provision.EngineMu.Unlock()
	if runErr != nil {
		job.Fail(runErr)
		return
	}

	loadingMessage := "Loading model …"
	job.Progress("loading", loadingMessage, -1, -1)
	waitQualificationPhaseGate(ctx, "loading")
	var peerTotal int64
	var nextPeerProbe time.Time
	peerLastBytes := map[string]int64{}
	peerLastAt := map[string]time.Time{}
	peerRates := map[string]float64{}
	modelRoot := ""
	if distributed {
		totalCtx, totalCancel := context.WithTimeout(ctx, 8*time.Second)
		token, _ := s.state.HuggingFaceToken()
		peerTotal = huggingFaceModelBytes(totalCtx, modelID, token)
		totalCancel()
		modelRoot = s.modelVolumePath(ctx)
	}
	for {
		if ctx.Err() != nil {
			job.Fail(fmt.Errorf("%s did not become ready in time", target.Name))
			return
		}
		pctx, pcancel := context.WithTimeout(context.Background(), 3*time.Second)
		ready := engineReady(pctx)
		pcancel()
		if ready {
			if qualificationPhaseRequested("optimizing") {
				job.Progress("optimizing", "Finalizing the distributed inference runtime …", -1, -1)
				waitQualificationPhaseGate(ctx, "optimizing")
			}
			if qualificationRollbackRequested() {
				job.Fail(errors.New("qualification requested the real rollback boundary"))
				return
			}
			job.Progress("verifying", "Verifying the stable Cloudless model and API contract …", -1, -1)
			waitQualificationPhaseGate(ctx, "verifying")
			if target.ID == "vllm" && !customProfile {
				toolCtx, toolCancel := context.WithTimeout(context.Background(), 35*time.Second)
				toolErr := engineToolContractError(toolCtx)
				toolCancel()
				if toolErr != nil {
					job.Fail(fmt.Errorf("%s failed the Cloudless Agent tool-calling contract: %w", target.Name, toolErr))
					return
				}
			}
			if verify != nil {
				verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 15*time.Second)
				verifyErr := verify(verifyCtx)
				verifyCancel()
				if verifyErr != nil {
					job.Fail(verifyErr)
					return
				}
			}
			if customProfile {
				if err := s.state.SetCustomEngineValidation(target.ID, "compatible", ""); err != nil {
					job.Fail(fmt.Errorf("record custom engine contract result: %w", err))
					return
				}
			}
			launchReady = true
			job.Succeed("")
			return
		}
		if distributed && !time.Now().Before(nextPeerProbe) {
			nextPeerProbe = time.Now().Add(5 * time.Second)
			probeCtx, probeCancel := context.WithTimeout(ctx, 4*time.Second)
			peers, peerErr := sparkcluster.ClusterModelProgress(probeCtx, modelID)
			probeCancel()
			if peerErr == nil && len(peers) > 0 {
				var peerBytes int64
				var downloading, loading []string
				allLoaded := true
				nodeProgress := make([]jobs.NodeProgress, 0, len(peers)+1)
				localName, _ := os.Hostname()
				localBytes := s.modelRepoBytes(ctx, modelID, modelRoot)
				localIncomplete := s.modelRepoIncomplete(ctx, modelID, modelRoot)
				localStage := jobs.NodeProgress{
					Node: localName, Phase: "loading", Message: "Coordinator is loading model weights",
					BytesDone: localBytes, BytesTotal: peerTotal,
				}
				now := time.Now()
				if lastAt := peerLastAt[localName]; !lastAt.IsZero() && localBytes > peerLastBytes[localName] {
					instant := float64(localBytes-peerLastBytes[localName]) / now.Sub(lastAt).Seconds()
					if peerRates[localName] == 0 {
						peerRates[localName] = instant
					} else {
						peerRates[localName] = peerRates[localName]*0.7 + instant*0.3
					}
				}
				peerLastBytes[localName], peerLastAt[localName] = localBytes, now
				if localIncomplete > 0 {
					downloading = append(downloading, localName)
					localStage.Phase = "downloading"
					localStage.Message = "Downloading model chunks"
					if peerTotal > 0 {
						localStage.Percent = min(99, int(localBytes*100/peerTotal))
						if rate := peerRates[localName]; rate > 0 && localBytes < peerTotal {
							localStage.ETASecs = int64(math.Ceil(float64(peerTotal-localBytes) / rate))
						}
					}
				}
				if localIncomplete == 0 {
					logs, logErr := s.eng.Logs(ctx, target.ContainerName())
					if logErr == nil {
						done, total := modelCheckpointProgress(logs)
						if total > 0 {
							localStage.BytesDone, localStage.BytesTotal = int64(done), int64(total)
							localStage.Percent = min(100, done*100/total)
							if done >= total {
								localStage.Phase = "optimizing"
								localStage.Message = "Coordinator loaded weights and is optimizing"
							}
						}
					}
				}
				if localStage.Phase == "loading" {
					loading = append(loading, localName)
					allLoaded = false
				} else if localStage.Phase == "downloading" {
					allLoaded = false
				}
				nodeProgress = append(nodeProgress, localStage)
				var peerBytesPerSecond = peerRates[localName]
				for _, peer := range peers {
					peerBytes += peer.Bytes
					if lastAt := peerLastAt[peer.Host]; !lastAt.IsZero() && peer.Bytes > peerLastBytes[peer.Host] {
						instant := float64(peer.Bytes-peerLastBytes[peer.Host]) / now.Sub(lastAt).Seconds()
						if peerRates[peer.Host] == 0 {
							peerRates[peer.Host] = instant
						} else {
							peerRates[peer.Host] = peerRates[peer.Host]*0.7 + instant*0.3
						}
					}
					peerLastBytes[peer.Host], peerLastAt[peer.Host] = peer.Bytes, now
					peerBytesPerSecond += peerRates[peer.Host]
					node := jobs.NodeProgress{Node: peer.Node, BytesDone: peer.Bytes, BytesTotal: peerTotal}
					if peer.Incomplete > 0 {
						downloading = append(downloading, peer.Node)
						node.Phase = "downloading"
						node.Message = "Downloading model chunks"
						if peerTotal > 0 {
							node.Percent = min(99, int(peer.Bytes*100/peerTotal))
							if rate := peerRates[peer.Host]; rate > 0 && peer.Bytes < peerTotal {
								node.ETASecs = int64(math.Ceil(float64(peerTotal-peer.Bytes) / rate))
							}
						}
					}
					if !peer.WeightsLoaded {
						allLoaded = false
						if peer.Incomplete == 0 {
							loading = append(loading, peer.Node)
							node.Phase = "loading"
							node.Message = "Download complete; loading model weights"
						}
					} else {
						node.Phase = "ready"
						node.Message = "Model weights loaded"
						node.Percent = 100
					}
					nodeProgress = append(nodeProgress, node)
				}
				clusterDone := peerBytes
				if peerTotal > 0 {
					clusterDone += min(localBytes, peerTotal)
				} else {
					clusterDone += localBytes
				}
				clusterTotal := peerTotal * int64(len(peers)+1)
				if len(downloading) > 0 {
					message := peerDownloadStatus(strings.Join(downloading, ", "), modelID, clusterDone, clusterTotal, peerBytesPerSecond)
					job.ProgressNodes("peer-downloading", message, nodeProgress, clusterDone, clusterTotal)
					time.Sleep(2 * time.Second)
					continue
				}
				if !allLoaded {
					job.ProgressNodes("peer-loading", strings.Join(loading, ", ")+" finished downloading and are loading model weights …", nodeProgress, clusterDone, clusterTotal)
					time.Sleep(2 * time.Second)
					continue
				}
				job.ProgressNodes("optimizing", fmt.Sprintf("All %d Sparks loaded the model. Optimizing distributed inference …", clusterNodes), nodeProgress, clusterDone, clusterTotal)
				waitQualificationPhaseGate(ctx, "optimizing")
				time.Sleep(2 * time.Second)
				continue
			}
		}
		if logs, err := s.eng.Logs(ctx, target.ContainerName()); err == nil {
			done, total := modelCheckpointProgress(logs)
			if total > 0 && done < total {
				job.Progress("loading", fmt.Sprintf("Loading model weights — %d of %d checkpoint shards", done, total), done, total)
			} else if total > 0 {
				job.Progress("optimizing", fmt.Sprintf("All %d Sparks loaded the model. Optimizing distributed inference …", clusterNodes), done, total)
				waitQualificationPhaseGate(ctx, "optimizing")
			}
		}
		time.Sleep(2 * time.Second)
	}
}

func peerDownloadStatus(peerName, model string, done, total int64, bytesPerSecond float64) string {
	message := peerName + " is downloading " + model + " — " + formatDownloadProgress(done, total)
	if total > done && bytesPerSecond > 0 {
		remaining := time.Duration(float64(time.Second) * float64(total-done) / bytesPerSecond)
		if remaining < time.Minute {
			message += fmt.Sprintf(" · about %d seconds remaining", max(1, int(math.Ceil(remaining.Seconds()))))
		} else {
			message += fmt.Sprintf(" · about %d minutes remaining", max(1, int(math.Ceil(remaining.Minutes()))))
		}
	}
	return message
}

var checkpointProgressPattern = regexp.MustCompile(`(?m)(\d+)% Completed \| (\d+)/(\d+)`)

func modelCheckpointProgress(logs string) (int, int) {
	matches := checkpointProgressPattern.FindAllStringSubmatch(logs, -1)
	if len(matches) == 0 {
		return 0, 0
	}
	last := matches[len(matches)-1]
	done, _ := strconv.Atoi(last[2])
	total, _ := strconv.Atoi(last[3])
	return done, total
}

func (s *Server) openFolder(w http.ResponseWriter, r *http.Request) {
	p, ok := places.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown folder"})
		return
	}
	if err := places.Open(p); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"opened": false, "path": p.Path, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true, "path": p.Path})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, catalog.All())
}

func currentBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// onboardingGet reports whether first-run onboarding has been completed for this
// install/user. `firstLaunch` is true when the daemon found no prior state file.
func (s *Server) onboardingGet(w http.ResponseWriter, r *http.Request) {
	st := s.state.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"completed":     st.Onboarded,
		"firstLaunch":   s.state.FirstRun(),
		"firstSeen":     st.FirstSeen,
		"bootID":        currentBootID(),
		"setupRequired": s.state.FirstLaunchSetupRequired(),
		"setupChoice":   s.state.FirstLaunchSetup(),
	})
}

// onboardingSetup records that the user will choose a recipe or model manually.
// First launch never authorizes automatic provisioning; explicit Model Manager
// actions remain the only way to download or start an inference workload.
func (s *Server) onboardingSetup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Choice string `json:"choice"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid setup choice"})
		return
	}
	if body.Choice != state.FirstLaunchSetupManual {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "first launch does not support automatic installation"})
		return
	}
	if err := s.state.SetFirstLaunchSetup(body.Choice); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"choice":  body.Choice,
		"started": false,
	})
}

// onboardingComplete marks first-run onboarding done and persists it.
func (s *Server) onboardingComplete(w http.ResponseWriter, r *http.Request) {
	if err := s.state.SetOnboarded(true); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"completed": true})
}

func (s *Server) recipeLaunchGuidanceGet(w http.ResponseWriter, r *http.Request) {
	st := s.state.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"acknowledged": st.RecipeLaunchGuidanceAck,
		"show":         st.LocalRecipeID != "" && !st.EngineUnloaded && !st.RecipeLaunchGuidanceAck,
	})
}

func (s *Server) recipeLaunchGuidanceAcknowledge(w http.ResponseWriter, r *http.Request) {
	if err := s.state.AcknowledgeRecipeLaunchGuidance(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true})
}

func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	all, err := s.eng.List(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	managed := []engine.Container{}
	for _, c := range all {
		if strings.HasPrefix(c.Name, "cloudless-") {
			managed = append(managed, c)
		}
	}
	writeJSON(w, http.StatusOK, managed)
}

// start kicks off an async install job and returns its id immediately.
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	if app.Image == "" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": app.Name + " recipe is not available yet",
		})
		return
	}
	identity := "app:" + app.ID + ":install"
	job, created := s.jobs.CreateUnique(identity, "app:"+app.ID+":")
	if !created {
		if job.AppID != identity {
			writeJSON(w, http.StatusConflict, map[string]string{"error": app.Name + " already has another background operation"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID, "operation": job.AppID})
		return
	}
	go s.runInstall(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID})
}

// runInstall pulls the image (streaming progress into the job) and runs it.
func (s *Server) runInstall(job *jobs.Job, app catalog.App) {
	dependencies, err := catalog.Dependencies(app.ID)
	if err != nil {
		job.Fail(err)
		return
	}
	targets := append(append([]catalog.App{}, dependencies...), app)
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.ID)
	}
	job.ProgressOperation("queued", "Queued in the background", app.Name, 0, 0, len(targets))
	unlock := s.appOperations.lock(ids...)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	installedDependencies := make([]catalog.App, 0, len(dependencies))
	rollbackDependencies := func(cause error) {
		if len(installedDependencies) == 0 {
			return
		}
		rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer rollbackCancel()
		for index := len(installedDependencies) - 1; index >= 0; index-- {
			dependency := installedDependencies[index]
			job.ProgressOperation("rolling-back", "Removing "+dependency.Name+" from the failed install", dependency.Name, operationPercent(index, len(targets), 0), index, len(targets))
			if err := s.eng.Remove(rollbackCtx, dependency.ContainerName()); err != nil {
				log.Printf("[install %s] rollback container %s after %v: %v", app.ID, dependency.ID, cause, err)
			}
			if err := s.eng.RemoveImage(rollbackCtx, s.imageFor(rollbackCtx, dependency)); err != nil {
				log.Printf("[install %s] rollback image %s after %v: %v", app.ID, dependency.ID, cause, err)
			}
		}
	}

	for index, dependency := range dependencies {
		current, findErr := s.eng.Find(ctx, dependency.ContainerName())
		if findErr != nil {
			job.Fail(fmt.Errorf("inspect dependency %s: %w", dependency.Name, findErr))
			return
		}
		if current != nil && current.State == "running" {
			job.ProgressOperation("dependency", dependency.Name+" is already ready", dependency.Name, operationPercent(index+1, len(targets), 0), index+1, len(targets))
			continue
		}
		job.ProgressOperation("dependency", "Preparing required service: "+dependency.Name, dependency.Name, operationPercent(index, len(targets), 5), index, len(targets))
		if _, err := s.installOne(ctx, job, dependency, index, len(targets)); err != nil {
			rollbackDependencies(err)
			job.Fail(fmt.Errorf("dependency %s: %w", dependency.Name, err))
			return
		}
		// A stopped/exited dependency existed before this operation. Its
		// replacement now belongs to the user's pre-existing installation and
		// must not be removed if the target app fails later.
		if current == nil {
			installedDependencies = append(installedDependencies, dependency)
		}
	}
	id, err := s.installOne(ctx, job, app, len(targets)-1, len(targets))
	if err != nil {
		rollbackDependencies(err)
		job.Fail(err)
		return
	}
	job.Succeed(id)
}

func operationPercent(item, total, within int) int {
	if total <= 0 {
		return within
	}
	return (item*100 + within) / total
}

func (s *Server) installOne(ctx context.Context, job *jobs.Job, app catalog.App, item, total int) (string, error) {

	job.ProgressOperation("preparing", "Preparing "+app.Name, app.Name, operationPercent(item, total, 4), item, total)

	// Use the manifest's validated digest when pinned, else the catalog tag.
	img := s.imageFor(ctx, app)

	if app.Build != "" {
		// Locally-built image (no upstream): materialize the embedded context and build.
		job.ProgressOperation("building", "Building "+app.Name+" from its reviewed source", app.Name, operationPercent(item, total, 12), item, total)
		dir, err := apps.Materialize(app.Build)
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		if err := s.eng.Build(ctx, app.Image, dir, func(l string) {
			log.Printf("[build %s] %s", app.ID, l)
			if message := workbenchBuildMessage(app.Name, l); message != "" {
				job.ProgressOperation("building", message, app.Name, operationPercent(item, total, 45), item, total)
			}
		}); err != nil {
			return "", err
		}
		// A build context always produces the catalog tag locally. A hosted
		// manifest pin applies to pulled images and must not redirect the run
		// step away from the image that was just built.
		img = app.Image
	} else {
		job.ProgressOperation("pulling", "Contacting the image registry for "+app.Name, app.Name, operationPercent(item, total, 10), item, total)
		layers := map[string]*dockerPullLayer{}
		err := pullImageStreamResilient(ctx, s.eng, img, func(line string) {
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
				job.ProgressOperation("pulling", line, app.Name, operationPercent(item, total, 76), item, total)
				return
			case strings.HasPrefix(line, "Cloudless:"):
				job.ProgressOperation("pulling", line, app.Name, operationPercent(item, total, 15), item, total)
				return
			default:
				return
			}
			done, layerTotal, bytesDone, bytesTotal := dockerPullTotals(layers)
			within := 15
			if layerTotal > 0 {
				within += done * 60 / layerTotal
			}
			message := fmt.Sprintf("Downloading %s image layers — %d of %d complete", app.Name, done, layerTotal)
			job.ProgressOperation("pulling", message, app.Name, operationPercent(item, total, within), item, total)
			job.ProgressDetail("pulling", message, done, layerTotal)
			if bytesTotal > 0 {
				job.ProgressBytesDetail("pulling", message, bytesDone, bytesTotal)
			}
		})
		if err != nil {
			return "", err
		}
	}

	job.ProgressOperation("starting", "Starting "+app.Name+" container", app.Name, operationPercent(item, total, 82), item, total)
	// Keep the currently running version available while a replacement image is
	// downloaded or built. The brief cutover starts only after the new artifact
	// is ready locally, so background updates do not create download-length
	// application outages.
	_ = s.eng.Remove(ctx, app.ContainerName())
	spec, err := s.appSpecChecked(app)
	if err != nil {
		return "", fmt.Errorf("prepare %s configuration: %w", app.Name, err)
	}
	spec.Image = img // run the exact image we pulled (pinned digest when manifest applies)
	id, err := s.eng.Run(ctx, spec)
	if err != nil {
		return "", err
	}
	job.ProgressOperation("verifying", "Checking "+app.Name+" readiness", app.Name, operationPercent(item, total, 90), item, total)
	if err := s.waitForAppHealth(ctx, app); err != nil {
		_ = s.eng.Remove(context.Background(), app.ContainerName())
		return "", err
	}
	if err := s.configureInstalledApp(ctx, job, app); err != nil {
		_ = s.eng.Remove(context.Background(), app.ContainerName())
		return "", fmt.Errorf("configure %s: %w", app.Name, err)
	}
	job.ProgressOperation("component-ready", app.Name+" is ready", app.Name, operationPercent(item+1, total, 0), item+1, total)
	return id, nil
}

func workbenchBuildMessage(name, line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#0 ") || strings.HasPrefix(line, "#1 ") {
		return ""
	}
	if len(line) > 180 {
		line = line[:177] + "..."
	}
	return "Building " + name + ": " + line
}

func (s *Server) waitForAppHealth(parent context.Context, app catalog.App) error {
	if s.appHealthCheck != nil {
		return s.appHealthCheck(parent, app)
	}
	timeout := time.Duration(app.Health.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var last string
	for {
		container, err := s.eng.Find(ctx, app.ContainerName())
		if err == nil && container != nil && container.State == "running" {
			healthPort := app.Health.Port
			if healthPort == 0 {
				healthPort = app.PrimaryHostPort()
			}
			if app.Health.Kind == "container" || healthPort == 0 {
				return nil
			}
			path := app.Health.Path
			if path == "" {
				path = "/"
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
				fmt.Sprintf("http://127.0.0.1:%d%s", healthPort, path), nil)
			if resp, requestErr := http.DefaultClient.Do(req); requestErr == nil {
				_ = resp.Body.Close()
				if resp.StatusCode < http.StatusInternalServerError {
					return nil
				}
				last = resp.Status
			} else {
				last = requestErr.Error()
			}
		} else if err != nil {
			last = err.Error()
		} else if container != nil {
			last = container.State
		}
		select {
		case <-ctx.Done():
			if last == "" {
				last = ctx.Err().Error()
			}
			return fmt.Errorf("%s failed its %s health contract: %s", app.Name, app.Health.Kind, last)
		case <-time.After(time.Second):
		}
	}
}

func (s *Server) jobState(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown job"})
		return
	}
	writeJSON(w, http.StatusOK, jobs.Snapshot{ID: job.ID, AppID: job.AppID, Update: job.Snapshot()})
}

// jobList exposes daemon-owned operation snapshots so a reloaded or repainted
// interface can reconnect without owning the lifecycle of the work.
func (s *Server) jobList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.jobs.List(r.URL.Query().Get("prefix")))
}

// jobEvents streams job updates as Server-Sent Events until the job is done.
func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := job.Subscribe()
	defer job.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case u, open := <-ch:
			if !open {
				return
			}
			b, _ := json.Marshal(u)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			if u.Done {
				return
			}
		}
	}
}

// splitStatus parses a "<id>: <status>" docker pull line.
func splitStatus(line string) (id, status string, ok bool) {
	i := strings.Index(line, ": ")
	if i <= 0 {
		return "", "", false
	}
	return line[:i], line[i+2:], true
}

func countComplete(m map[string]bool) (done, total int) {
	total = len(m)
	for _, complete := range m {
		if complete {
			done++
		}
	}
	return done, total
}

type dockerPullLayer struct {
	complete bool
	done     int64
	total    int64
}

func dockerPullTotals(layers map[string]*dockerPullLayer) (done, total int, bytesDone, bytesTotal int64) {
	total = len(layers)
	for _, layer := range layers {
		if layer == nil {
			continue
		}
		if layer.complete {
			done++
		}
		bytesDone += layer.done
		bytesTotal += layer.total
	}
	return
}

func parseDockerLayerProgress(status string) (done, total int64, ok bool) {
	fields := strings.Fields(status)
	for _, field := range fields {
		parts := strings.Split(field, "/")
		if len(parts) != 2 {
			continue
		}
		done, errDone := parseDockerSize(parts[0])
		total, errTotal := parseDockerSize(parts[1])
		if errDone == nil && errTotal == nil && total > 0 {
			return done, total, true
		}
	}
	return 0, 0, false
}

func parseDockerSize(value string) (int64, error) {
	value = strings.TrimSpace(value)
	units := []struct {
		suffix string
		factor float64
	}{{"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			parsed, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, err
			}
			return int64(parsed * unit.factor), nil
		}
	}
	return 0, fmt.Errorf("unsupported Docker size %q", value)
}

// appReset wipes an app to a clean state: remove its container (and image, so a
// build app rebuilds the recipe), then reinstall. Runs as an async job.
func (s *Server) appReset(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	identity := "app:" + app.ID + ":reset"
	job, created := s.jobs.CreateUnique(identity, "app:"+app.ID+":")
	if !created {
		if job.AppID != identity {
			writeJSON(w, http.StatusConflict, map[string]string{"error": app.Name + " already has another background operation"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID, "operation": job.AppID})
		return
	}
	go func() {
		job.ProgressOperation("queued", "Reset queued in the background", app.Name, 0, 0, 1)
		unlock := s.appOperations.lock(app.ID)
		defer unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if app.ID == "hermes" {
			job.ProgressOperation("resetting", "Restoring the Cloudless model connection", app.Name, 25, 0, 1)
			if err := apps.ResetHermesModel(s.appConfigDir(app.ID)); err != nil {
				job.Fail(err)
				return
			}
			_ = s.eng.Remove(ctx, app.ContainerName())
			spec, err := s.appSpecChecked(app)
			if err != nil {
				job.Fail(err)
				return
			}
			spec.Image = s.imageFor(ctx, app)
			id, err := s.eng.Run(ctx, spec)
			if err != nil {
				job.Fail(err)
				return
			}
			job.Succeed(id)
			return
		}
		_ = s.eng.Remove(ctx, app.ContainerName())
		if app.Build != "" { // force a fresh rebuild of locally-built apps
			_ = s.eng.RemoveImage(ctx, app.Image)
		}
		if _, err := s.installOne(ctx, job, app, 0, 1); err != nil {
			job.Fail(err)
			return
		}
		job.Succeed("")
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID})
}

// appUninstall starts a daemon-owned background removal job.
func (s *Server) appUninstall(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	if app.ID == "hermes" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Hermes is a core CloudlessOS service and cannot be uninstalled"})
		return
	}
	identity := "app:" + app.ID + ":uninstall"
	job, created := s.jobs.CreateUnique(identity, "app:"+app.ID+":")
	if !created {
		if job.AppID != identity {
			writeJSON(w, http.StatusConflict, map[string]string{"error": app.Name + " already has another background operation"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID, "operation": job.AppID})
		return
	}
	go s.runAppUninstall(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "app": app.ID})
}

func (s *Server) runAppUninstall(job *jobs.Job, app catalog.App) {
	job.ProgressOperation("queued", "Removal queued in the background", app.Name, 0, 0, 2)
	unlock := s.appOperations.lock(app.ID)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	job.ProgressOperation("removing", "Stopping and removing "+app.Name, app.Name, 25, 0, 2)
	if err := s.removeAppRuntime(ctx, app); err != nil {
		job.Fail(fmt.Errorf("remove %s runtime: %w", app.Name, err))
		return
	}
	job.ProgressOperation("cleaning-image", "Removing the downloaded "+app.Name+" image", app.Name, 70, 1, 2)
	if err := s.eng.RemoveImage(ctx, s.imageFor(ctx, app)); err != nil {
		job.Fail(fmt.Errorf("remove %s image: %w", app.Name, err))
		return
	}
	job.ProgressOperation("removed", app.Name+" was removed; its persistent data was kept", app.Name, 100, 2, 2)
	job.Succeed("")
}

// removeAppRuntime removes every ephemeral container Cloudless may have
// created for an application. Persistent named volumes and state directories
// are deliberately retained so reinstalling never destroys user data.
func (s *Server) removeAppRuntime(ctx context.Context, app catalog.App) error {
	container, err := s.eng.Find(ctx, app.ContainerName())
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", app.ContainerName(), err)
	}
	if container != nil {
		if err := s.eng.Remove(ctx, app.ContainerName()); err != nil {
			return err
		}
	}
	for _, sidecar := range []string{app.LanName(), app.TunnelName()} {
		container, err = s.eng.Find(ctx, sidecar)
		if err != nil {
			return fmt.Errorf("inspect sidecar %s: %w", sidecar, err)
		}
		if container != nil {
			if err := s.eng.Remove(ctx, sidecar); err != nil {
				return fmt.Errorf("remove sidecar %s: %w", sidecar, err)
			}
		}
	}
	return nil
}

// appConfigDir is where an app's editable config files live in the state dir.
func (s *Server) appConfigDir(appID string) string {
	return filepath.Join(s.state.Dir(), "apps", appID)
}

// configVolumes seeds missing config files from their embedded defaults and
// returns host->container mounts so the app reads the user's editable config.
// Stateful apps can mount the complete directory, allowing their own UI to also
// persist sessions, memory and settings alongside Cloudless-seeded files.
func (s *Server) configVolumes(app catalog.App) map[string]string {
	dir := s.appConfigDir(app.ID)
	vols, err := apps.ConfigVolumes(dir, app)
	if err != nil {
		log.Printf("config: prepare %s: %v", app.ID, err)
		return map[string]string{}
	}
	return vols
}

// appSpec is app.Spec() plus the user's mounted config files. It builds a fresh
// volumes map so it never mutates the catalog's shared map (which would otherwise
// accumulate config mounts for an app that has both data volumes and config files).
func (s *Server) appSpec(app catalog.App) engine.RunSpec {
	spec, err := s.appSpecChecked(app)
	if err != nil {
		log.Printf("config: prepare %s: %v", app.ID, err)
		return app.Spec()
	}
	return spec
}

func (s *Server) appSpecChecked(app catalog.App) (engine.RunSpec, error) {
	rs := app.Spec()
	merged := map[string]string{}
	for h, c := range rs.Volumes {
		merged[h] = c
	}
	volumes, err := apps.ConfigVolumes(s.appConfigDir(app.ID), app)
	if err != nil {
		return engine.RunSpec{}, err
	}
	for h, c := range volumes {
		merged[h] = c
	}
	if len(merged) > 0 {
		rs.Volumes = merged
	}
	// Env-injected config files (e.g. Open WebUI's webui.env) overlay the catalog
	// env. Clone first — rs.Env aliases the shared catalog map. Same logic runs in
	// the boot provisioner, so the setting survives a reboot.
	if ov := apps.EnvOverrides(s.appConfigDir(app.ID), app); len(ov) > 0 {
		env := map[string]string{}
		for k, v := range rs.Env {
			env[k] = v
		}
		for k, v := range ov {
			env[k] = v
		}
		rs.Env = env
	}
	return rs, nil
}

func (s *Server) appConfigGet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || len(app.Config) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no config"})
		return
	}
	type file struct {
		File    string `json:"file"`
		Lang    string `json:"lang"`
		Content string `json:"content"`
	}
	dir := s.appConfigDir(app.ID)
	files := []file{}
	for _, cf := range app.Config {
		content := ""
		if b, err := os.ReadFile(filepath.Join(dir, cf.File)); err == nil {
			content = string(b)
		} else if def, derr := apps.ReadDefault(app.ID, cf.File); derr == nil {
			content = string(def)
		}
		files = append(files, file{File: cf.File, Lang: cf.Lang, Content: content})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) appConfigSet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || len(app.Config) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no config"})
		return
	}
	var body struct {
		Files map[string]string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	dir := s.appConfigDir(app.ID)
	_ = os.MkdirAll(dir, 0o755)
	for _, cf := range app.Config {
		if content, present := body.Files[cf.File]; present {
			if err := os.WriteFile(filepath.Join(dir, cf.File), []byte(content), 0o644); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
	}
	s.restartApp(w, app)
}

func (s *Server) appConfigReset(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok || len(app.Config) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no config"})
		return
	}
	dir := s.appConfigDir(app.ID)
	for _, cf := range app.Config {
		_ = os.Remove(filepath.Join(dir, cf.File)) // appSpec reseeds defaults on next run
	}
	s.restartApp(w, app)
}

// restartApp recreates the app's container (applying current config) if it's
// installed; otherwise acknowledges (config applies on next install).
func (s *Server) restartApp(w http.ResponseWriter, app catalog.App) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	c, _ := s.eng.Find(ctx, app.ContainerName())
	cancel()
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
		return
	}
	job := s.jobs.Create("config:" + app.ID)
	go func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer rcancel()
		job.Progress("switching", "Applying config…", -1, -1)
		_ = s.eng.Remove(rctx, app.ContainerName())
		spec, err := s.appSpecChecked(app)
		if err != nil {
			job.Fail(fmt.Errorf("prepare %s configuration: %w", app.Name, err))
			return
		}
		if _, err := s.eng.Run(rctx, spec); err != nil {
			job.Fail(err)
			return
		}
		if err := s.waitForAppHealth(rctx, app); err != nil {
			_ = s.eng.Remove(context.Background(), app.ContainerName())
			job.Fail(fmt.Errorf("restart %s: %w", app.Name, err))
			return
		}
		job.Succeed("")
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID})
}

func (s *Server) configFileFor(app catalog.App, file string) catalog.ConfigFile {
	for _, cf := range app.Config {
		if cf.File == file {
			return cf
		}
	}
	return catalog.ConfigFile{}
}

func (s *Server) readConfigContent(app catalog.App, file string) string {
	if b, err := os.ReadFile(filepath.Join(s.appConfigDir(app.ID), file)); err == nil {
		return string(b)
	}
	if def, err := apps.ReadDefault(app.ID, file); err == nil {
		return string(def)
	}
	return ""
}

func jsonGetPath(m map[string]any, path string) (string, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = mm[p]
		if !ok {
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case bool:
		if v {
			return "true", true
		}
		return "false", true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}
	return "", false
}

func jsonSetPath(m map[string]any, path, value, typ string) {
	parts := strings.Split(path, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	key := parts[len(parts)-1]
	switch typ {
	case "toggle":
		cur[key] = value == "true"
	case "number":
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			cur[key] = f
		} else {
			cur[key] = value
		}
	default:
		cur[key] = value
	}
}

func envGetKey(content, key string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, key+"=") {
			return strings.TrimPrefix(t, key+"="), true
		}
	}
	return "", false
}

func envSetKey(content, key, value string) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+"=") {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	out = append(out, key+"="+value)
	return strings.Join(out, "\n") + "\n"
}

func (s *Server) readField(app catalog.App, f catalog.Field) string {
	cf := s.configFileFor(app, f.File)
	content := s.readConfigContent(app, f.File)
	if cf.Lang == "env" {
		if v, ok := envGetKey(content, f.Path); ok {
			return v
		}
		return f.Default
	}
	var m map[string]any
	if json.Unmarshal([]byte(content), &m) == nil {
		if v, ok := jsonGetPath(m, f.Path); ok {
			return v
		}
	}
	return f.Default
}

func (s *Server) appSettingsGet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	type field struct {
		Key     string   `json:"key"`
		Label   string   `json:"label"`
		Help    string   `json:"help,omitempty"`
		Type    string   `json:"type"`
		Options []string `json:"options,omitempty"`
		Default string   `json:"default"`
		Value   string   `json:"value"`
	}
	out := []field{}
	for _, f := range app.Settings {
		out = append(out, field{f.Key, f.Label, f.Help, f.Type, f.Options, f.Default, s.readField(app, f)})
	}
	admin := app.Admin
	if admin != nil && admin.ManagedPassword != "" {
		copy := *admin
		if secret, err := apps.ManagedSecret(s.appConfigDir(app.ID), app.ID, admin.ManagedPassword); err == nil {
			copy.Pass = secret
		}
		copy.ManagedPassword = ""
		admin = &copy
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": out, "admin": admin})
}

func (s *Server) appSettingsSet(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}

	// Group fields by file so each file is read/written once.
	byFile := map[string][]catalog.Field{}
	var order []string
	for _, f := range app.Settings {
		if _, seen := byFile[f.File]; !seen {
			order = append(order, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	dir := s.appConfigDir(app.ID)
	_ = os.MkdirAll(dir, 0o755)
	for _, file := range order {
		cf := s.configFileFor(app, file)
		content := s.readConfigContent(app, file)
		if cf.Lang == "env" {
			for _, f := range byFile[file] {
				if v, present := body.Values[f.Key]; present {
					content = envSetKey(content, f.Path, v)
				}
			}
		} else {
			var m map[string]any
			if err := json.Unmarshal([]byte(content), &m); err != nil || m == nil {
				m = map[string]any{}
			}
			for _, f := range byFile[file] {
				if v, present := body.Values[f.Key]; present {
					jsonSetPath(m, f.Path, v, f.Type)
				}
			}
			b, _ := json.MarshalIndent(m, "", "  ")
			content = string(b) + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	s.restartApp(w, app)
}

func (s *Server) settingsGet(w http.ResponseWriter, r *http.Request) {
	st := s.state.Get()
	model := st.Model
	if model == "" {
		model = catalog.DefaultModel()
	}
	runtimeKind, runtimeID, runtimeName := "", "", ""
	if !st.EngineUnloaded {
		if st.LocalRecipeID != "" {
			runtimeKind, runtimeID, runtimeName = "recipe", st.LocalRecipeID, st.LocalRecipeID
			if recipe, ok, err := s.recipes.Get(st.LocalRecipeID); err == nil && ok && strings.TrimSpace(recipe.Name) != "" {
				runtimeName = recipe.Name
			}
		} else {
			runtimeKind, runtimeID, runtimeName = "model", resolveModel(model), resolveModel(model)
			if selected, ok := models.Get(runtimeID); ok && strings.TrimSpace(selected.Name) != "" {
				runtimeName = selected.Name
			} else if custom, ok := st.CustomModels[runtimeID]; ok && strings.TrimSpace(custom.Name) != "" {
				runtimeName = custom.Name
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model":        model,
		"defaultModel": catalog.DefaultModel(),
		"unloaded":     st.EngineUnloaded,
		"runtimeKind":  runtimeKind,
		"runtimeId":    runtimeID,
		"runtimeName":  runtimeName,
		"executionMode": func() string {
			if st.ExecutionMode == "cluster" {
				return "cluster"
			}
			return "local"
		}(),
	})
}

// settingsModel sets the served model and restarts the active engine to apply it.
func (s *Server) settingsModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
		Mode  string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == catalog.DefaultModel() {
		model = "" // store empty to mean "default"
	}
	mode := "local"
	if body.Mode == "cluster" {
		modelID := resolveModel(model)
		if selected, ok := models.Get(modelID); ok && selected.SingleNodeOnly {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "this model currently has a reviewed single-Spark runtime only"})
			return
		}
		cluster := clusterCompute(r.Context(), totalVRAMGB(r.Context()))
		if !cluster.DistributedReady {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "the Spark cluster is not healthy"})
			return
		}
		engineCtx, engineCancel := context.WithTimeout(r.Context(), 5*time.Second)
		activeEngine := s.activeEngine(engineCtx)
		engineCancel()
		if activeEngine == "" {
			activeEngine = s.state.Get().Engine
		}
		if activeEngine == "" {
			activeEngine = catalog.DefaultEngine()
		}
		if activeEngine != "vllm" {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "distributed inference currently requires the reviewed vLLM engine"})
			return
		}
		if _, err := reviewedDistributedProfile(modelID, activeEngine, cluster.Nodes); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		mode = "cluster"
	}
	previous := s.state.Get().InferenceRuntime()
	_ = s.state.SetModel(model)
	_ = s.state.SetExecutionMode(mode)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	active := s.activeEngine(ctx)
	cancel()
	if active == "" {
		active = catalog.DefaultEngine()
	}
	app, ok := customengine.Get(s.state, active)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the selected inference engine is unavailable"})
		return
	}
	job := s.jobs.Create("model:" + app.ID)
	s.observeInferenceJob(job, "model", app.ID, previous)
	go s.applyEngine(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "mode": mode})
}

func (s *Server) onboardingReset(w http.ResponseWriter, r *http.Request) {
	if err := s.state.SetOnboarded(false); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.eng.Stop(ctx, app.ContainerName()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	app, ok := catalog.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown app"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.eng.Remove(ctx, app.ContainerName()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}
