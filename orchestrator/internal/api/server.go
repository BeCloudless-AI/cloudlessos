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
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/hardware"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/places"
	"github.com/cloudless/orchestrator/internal/power"
	"github.com/cloudless/orchestrator/internal/provision"
	"github.com/cloudless/orchestrator/internal/remoteaccess"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
	"github.com/cloudless/orchestrator/internal/state"
	"github.com/cloudless/orchestrator/internal/usage"
)

//go:embed all:web
var webFS embed.FS

// Server wires the container engine, job manager, and state store to HTTP handlers.
type Server struct {
	eng           engine.Engine
	jobs          *jobs.Manager
	state         *state.Store
	manifest      *manifest.Store
	mfModels      *manifest.ModelsStore
	mfDiff        *manifest.DiffusionStore
	usage         *usage.Store
	power         *power.Store
	shutdown      func() error
	gatewayRebind func(int) error
	virtualKey    func(context.Context, string, string) error
	virtualKeyMu  sync.Mutex

	shutdownMu     sync.Mutex
	shutdownQueued bool
	shutdownDelay  time.Duration

	modelsMu     sync.Mutex
	modelsHave   map[string]bool
	modelsHaveAt time.Time

	modelJobsMu sync.Mutex
	modelJobs   map[string]context.CancelFunc

	engineJobsMu sync.Mutex
	engineJobs   map[string]context.CancelFunc
	recipeJobsMu sync.Mutex
	recipeJobs   map[string]recipeJobControl

	appOperations appOperationLocks // serialize only operations that touch the same app
	recipes       *localrecipes.Store
	remoteAccess  remoteaccess.Service
}

// NewServer constructs a Server backed by the given engine, state store and manifests.
func NewServer(eng engine.Engine, st *state.Store, mf *manifest.Store, mfModels *manifest.ModelsStore, mfDiff *manifest.DiffusionStore, us *usage.Store, pw *power.Store) *Server {
	return &Server{
		eng: eng, jobs: jobs.NewManager(), state: st, manifest: mf, mfModels: mfModels,
		mfDiff: mfDiff, usage: us, power: pw, shutdown: systemShutdown, virtualKey: emitSystemVirtualKey,
		shutdownDelay: time.Second, recipes: localrecipes.New(st.Dir()), remoteAccess: remoteaccess.New(),
	}
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
	mux.Handle("GET /terminal/", cloudlessTerminalProxy())
	mux.HandleFunc("GET /api/gpu", s.gpu)
	mux.HandleFunc("GET /api/system", s.system)
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
	mux.HandleFunc("GET /api/system/update", s.systemUpdateGet)
	mux.HandleFunc("POST /api/system/update/check", s.systemUpdateCheck)
	mux.HandleFunc("POST /api/system/update/apply", s.systemUpdateApply)
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
	mux.HandleFunc("POST /api/recipes", s.localRecipeCreate)
	mux.HandleFunc("POST /api/recipes/import/preview", s.localRecipeImportPreview)
	mux.HandleFunc("POST /api/recipes/import", s.localRecipeImport)
	mux.HandleFunc("DELETE /api/recipes/{id}", s.localRecipeDelete)
	mux.HandleFunc("PUT /api/recipes/{id}", s.localRecipeUpdate)
	mux.HandleFunc("GET /api/recipes/{id}/source", s.localRecipeSource)
	mux.HandleFunc("POST /api/recipes/{id}/check", s.localRecipeCheck)
	mux.HandleFunc("POST /api/recipes/{id}/run", s.localRecipeRun)
	mux.HandleFunc("POST /api/recipes/{id}/abort", s.localRecipeAbort)
	mux.HandleFunc("POST /api/recipes/{id}/stop", s.localRecipeStop)
	mux.HandleFunc("GET /api/diffusion", s.diffusionList)
	mux.HandleFunc("GET /api/diffusion/downloads", s.diffusionDownloads)
	mux.HandleFunc("POST /api/diffusion/{id}/download", s.diffusionDownload)
	mux.HandleFunc("POST /api/diffusion/{id}/uninstall", s.diffusionUninstall)
	mux.HandleFunc("POST /api/onboarding/reset", s.onboardingReset)
	mux.HandleFunc("GET /api/onboarding", s.onboardingGet)
	mux.HandleFunc("POST /api/onboarding/complete", s.onboardingComplete)
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
	mux.HandleFunc("POST /api/gateway/tunnel", s.gatewayTunnelSet)
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
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/models", catalog.EnginePort)
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

func engineReady(ctx context.Context) bool {
	return engineEndpointError(ctx) == nil
}

func (s *Server) engineState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	active := s.activeEngine(ctx)
	currentState := s.state.Get()
	type eng struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Active   bool   `json:"active"`
		Selected bool   `json:"selected"`
		Custom   bool   `json:"custom,omitempty"`
		Image    string `json:"image,omitempty"`
		Base     string `json:"base,omitempty"`
	}
	selected := currentState.Engine
	if selected == "" {
		selected = catalog.DefaultEngine()
	}
	list := []eng{}
	for _, e := range customengine.All(s.state) {
		item := eng{ID: e.ID, Name: e.Name, Active: e.ID == active, Selected: e.ID == selected, Custom: customengine.IsCustom(e.ID)}
		if item.Custom {
			item.Image = e.Image
			for _, def := range currentState.CustomEngines {
				if def.ID == e.ID {
					item.Base = def.Base
					break
				}
			}
		}
		list = append(list, item)
	}
	var startup *jobs.Snapshot
	startupSequence := -1
	for _, prefix := range []string{"engine:", "model:"} {
		for _, snapshot := range s.jobs.List(prefix) {
			sequence, _ := strconv.Atoi(strings.TrimPrefix(snapshot.ID, "job-"))
			if !snapshot.Done && sequence > startupSequence {
				copy := snapshot
				startup = &copy
				startupSequence = sequence
			}
		}
	}
	ready := active != "" && engineReady(ctx)
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
						if mount, mountErr := s.eng.Output(ctx, "volume", "inspect", "cloudless-hf", "--format", "{{.Mountpoint}}"); mountErr == nil {
							bytesTotal = s.modelRepoBytes(ctx, modelID, strings.TrimSpace(mount)) * int64(len(peers))
						}
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
	writeJSON(w, http.StatusOK, map[string]any{
		"active":         active,
		"ready":          ready,
		"unloaded":       currentState.EngineUnloaded,
		"engines":        list,
		"startup":        startup,
		"promotion":      currentState.ModelPromotion,
		"operation":      operation,
		"operationJobId": operationJobID,
		"canAbort":       canAbort,
		"cluster":        clusterCompute(ctx, totalVRAMGB(ctx)),
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
func describeEngineOperation(startup *jobs.Snapshot, ready, unloaded bool) (operation, jobID string, canAbort bool) {
	if startup != nil && !startup.Done {
		switch startup.AppID {
		case "engine:unload":
			return "unloading", startup.ID, false
		case "engine:abort":
			return "aborting", startup.ID, false
		}
		if !ready && !unloaded && (strings.HasPrefix(startup.AppID, "engine:") || strings.HasPrefix(startup.AppID, "model:")) {
			return "loading", startup.ID, startup.ID != ""
		}
	}
	return "idle", "", false
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
	if s.cancelEngineJobs() == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no model launch is currently active"})
		return
	}
	job := s.jobs.Create("engine:abort")
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

func (s *Server) runEngineUnload(job *jobs.Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	provision.EngineMu.Lock()
	defer provision.EngineMu.Unlock()
	s.stopActiveLocalRecipeRuntime(context.Background(), job)

	job.Progress("stopping", "Stopping inference and releasing accelerator memory …", -1, -1)
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
	app, ok := customengine.Get(s.state, r.PathValue("id"))
	if !ok || !app.Engine {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown engine"})
		return
	}
	job := s.jobs.Create("engine:" + app.ID)
	go s.runSwitch(job, app)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": job.ID, "engine": app.ID})
}

// engineRestart recreates the active engine container with its current catalog spec
// (e.g. to pick up SGLang's --enable-metrics). Unlike a switch, it always recreates.
func (s *Server) engineRestart(w http.ResponseWriter, r *http.Request) {
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
	st := s.state.Get()
	model := st.Model
	modelID := resolveModel(model)
	modelRuntime, hasModelRuntime := models.Get(modelID)
	clusterNodes := 2
	localFallback := job.AppID == "engine:cluster-disconnect-fallback"
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
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cleanupCancel()
		provision.EngineMu.Lock()
		_ = s.eng.Remove(cleanupCtx, "cloudless-cluster-engine-proxy")
		_ = s.eng.Remove(cleanupCtx, target.ContainerName())
		if distributed {
			_ = sparkcluster.StopWorker(cleanupCtx)
		}
		_ = s.state.SetEngineUnloaded(true)
		provision.EngineMu.Unlock()
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
	if localFallback {
		job.Progress("switching", "Releasing the distributed model and its memory …", -1, -1)
	}
	_ = s.eng.Remove(ctx, target.ContainerName())
	_ = s.eng.Remove(ctx, "cloudless-cluster-engine-proxy")
	if !distributed {
		_ = sparkcluster.StopWorker(ctx)
	}
	startMessage := "Starting " + target.Name + " …"
	if localFallback {
		startMessage = "Starting " + target.Name + " locally on this Spark …"
	}
	job.Progress("switching", startMessage, -1, -1)
	var runErr error
	if distributed {
		cluster := clusterCompute(ctx, totalVRAMGB(ctx))
		clusterNodes = max(2, cluster.Nodes)
		if !cluster.DistributedReady {
			provision.EngineMu.Unlock()
			job.Fail(errors.New("the Spark cluster is not healthy enough for distributed inference"))
			return
		}
		spec := sparkcluster.CoordinatorSpec(s.managedEngineSpec(ctx, target, model, nil))
		job.Progress("cluster", "Starting the coordinator on this Spark …", -1, -1)
		_, runErr = s.eng.Run(ctx, spec)
		if runErr != nil {
			provision.EngineMu.Unlock()
			job.Fail(runErr)
			return
		}
		started := time.Now()
		job.Progress("cluster", fmt.Sprintf("Preparing %d worker Sparks. First launch downloads may take several minutes …", clusterNodes-1), -1, -1)
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
				job.Progress("cluster", fmt.Sprintf("Preparing %d worker Sparks for distributed inference (%s elapsed) …", clusterNodes-1, elapsed), -1, -1)
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
		job.Progress("cluster", fmt.Sprintf("Waiting for all %d Sparks to join the inference cluster …", clusterNodes), -1, -1)
		waitCommand := fmt.Sprintf("until ray status 2>/dev/null | grep -q '/%d.0 GPU'; do sleep 2; done", clusterNodes)
		if err := s.eng.Exec(ctx, target.ContainerName(), "/bin/bash", "-lc", waitCommand); err != nil {
			runErr = err
		}
		job.Progress("cluster", fmt.Sprintf("All %d Sparks joined. Starting the distributed model …", clusterNodes), -1, -1)
		if err := s.eng.Exec(ctx, target.ContainerName(), "touch", "/tmp/cloudless-ray-worker"); err != nil {
			runErr = err
		}
		if runErr == nil {
			job.Progress("cluster", fmt.Sprintf("Connecting Cloudless apps to the %d-Spark engine …", clusterNodes), -1, -1)
			_ = s.eng.Pull(ctx, "alpine/socat:latest")
			_, runErr = s.eng.Run(ctx, sparkcluster.ProxySpec())
		}
		if runErr != nil {
			_ = sparkcluster.StopWorker(ctx)
		}
	} else {
		override, _ := s.state.EngineCmd(target.ID, modelID)
		_, runErr = s.eng.Run(ctx, s.managedEngineSpec(ctx, target, model, override))
	}
	provision.EngineMu.Unlock()
	if runErr != nil {
		job.Fail(runErr)
		return
	}

	loadingMessage := "Loading model …"
	if localFallback {
		loadingMessage = "Loading " + resolveModel(model) + " locally on this Spark …"
	}
	job.Progress("loading", loadingMessage, -1, -1)
	var peerTotal, peerLastBytes int64
	var peerLastAt, nextPeerProbe time.Time
	var peerBytesPerSecond float64
	if distributed {
		totalCtx, totalCancel := context.WithTimeout(ctx, 8*time.Second)
		token, _ := s.state.HuggingFaceToken()
		peerTotal = huggingFaceModelBytes(totalCtx, modelID, token)
		totalCancel()
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
				now := time.Now()
				if !peerLastAt.IsZero() && peerBytes > peerLastBytes {
					instant := float64(peerBytes-peerLastBytes) / now.Sub(peerLastAt).Seconds()
					if peerBytesPerSecond == 0 {
						peerBytesPerSecond = instant
					} else {
						peerBytesPerSecond = peerBytesPerSecond*0.7 + instant*0.3
					}
				}
				peerLastBytes, peerLastAt = peerBytes, now
				clusterTotal := peerTotal * int64(len(peers))
				if len(downloading) > 0 {
					message := peerDownloadStatus(strings.Join(downloading, ", "), modelID, peerBytes, clusterTotal, peerBytesPerSecond)
					job.ProgressBytes("peer-downloading", message, peerBytes, clusterTotal)
					time.Sleep(2 * time.Second)
					continue
				}
				if !allLoaded {
					job.ProgressBytes("peer-loading", strings.Join(loading, ", ")+" finished downloading and are loading model weights …", 0, 0)
					time.Sleep(2 * time.Second)
					continue
				}
			}
		}
		if logs, err := s.eng.Logs(ctx, target.ContainerName()); err == nil {
			done, total := modelCheckpointProgress(logs)
			if total > 0 && done < total {
				job.Progress("loading", fmt.Sprintf("Loading model weights — %d of %d checkpoint shards", done, total), done, total)
			} else if total > 0 {
				job.Progress("optimizing", fmt.Sprintf("All %d Sparks loaded the model. Optimizing distributed inference …", clusterNodes), done, total)
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
		"completed":   st.Onboarded,
		"firstLaunch": s.state.FirstRun(),
		"firstSeen":   st.FirstSeen,
		"bootID":      currentBootID(),
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

	for index, dependency := range dependencies {
		if current, _ := s.eng.Find(ctx, dependency.ContainerName()); current != nil && current.State == "running" {
			job.ProgressOperation("dependency", dependency.Name+" is already ready", dependency.Name, operationPercent(index+1, len(targets), 0), index+1, len(targets))
			continue
		}
		job.ProgressOperation("dependency", "Preparing required service: "+dependency.Name, dependency.Name, operationPercent(index, len(targets), 5), index, len(targets))
		if _, err := s.installOne(ctx, job, dependency, index, len(targets)); err != nil {
			job.Fail(fmt.Errorf("dependency %s: %w", dependency.Name, err))
			return
		}
	}
	id, err := s.installOne(ctx, job, app, len(targets)-1, len(targets))
	if err != nil {
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
		err := s.eng.PullStream(ctx, img, func(line string) {
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
	spec := s.appSpec(app)
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
			spec := s.appSpec(app)
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
	if err := s.eng.Remove(ctx, app.ContainerName()); err != nil {
		job.Fail(fmt.Errorf("remove %s container: %w", app.Name, err))
		return
	}
	job.ProgressOperation("cleaning-image", "Removing the downloaded "+app.Name+" image", app.Name, 70, 1, 2)
	if err := s.eng.RemoveImage(ctx, app.Image); err != nil {
		job.Fail(fmt.Errorf("remove %s image: %w", app.Name, err))
		return
	}
	job.ProgressOperation("removed", app.Name+" was removed; its persistent data was kept", app.Name, 100, 2, 2)
	job.Succeed("")
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
	rs := app.Spec()
	merged := map[string]string{}
	for h, c := range rs.Volumes {
		merged[h] = c
	}
	for h, c := range s.configVolumes(app) {
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
	return rs
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
		if _, err := s.eng.Run(rctx, s.appSpec(app)); err != nil {
			job.Fail(err)
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
	writeJSON(w, http.StatusOK, map[string]any{"fields": out, "admin": app.Admin})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"model":        model,
		"defaultModel": catalog.DefaultModel(),
		"unloaded":     st.EngineUnloaded,
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
		if selected, ok := models.Get(model); ok && selected.SingleNodeOnly {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "this model currently has a reviewed single-Spark runtime only"})
			return
		}
		cluster := clusterCompute(r.Context(), totalVRAMGB(r.Context()))
		if !cluster.DistributedReady {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "the Spark cluster is not healthy"})
			return
		}
		mode = "cluster"
	}
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
