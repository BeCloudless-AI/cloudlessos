package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/customengine"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/state"
)

const defaultBootstrapModel = "Qwen/Qwen2.5-1.5B-Instruct"

// BootstrapModel is deliberately small enough to become useful quickly while
// the platform default downloads in the background. An empty override disables
// bootstrap promotion for controlled deployments.
func BootstrapModel() string {
	if value, ok := os.LookupEnv("CLOUDLESS_BOOTSTRAP_MODEL"); ok {
		return strings.TrimSpace(value)
	}
	return defaultBootstrapModel
}

// ShouldBootstrap is conservative for upgraded installations: it starts this
// lifecycle only on a genuinely new store, or resumes a transition already
// recorded in that store. Existing systems with an implicit default are never
// unexpectedly downgraded during package upgrade.
func ShouldBootstrap(current state.State, firstRun bool) bool {
	bootstrap, target := BootstrapModel(), catalog.DefaultModel()
	if bootstrap == "" || bootstrap == target || current.Model != "" || current.EngineUnloaded || current.ExecutionMode == "cluster" {
		return false
	}
	p := current.ModelPromotion
	if p.TargetReady && p.Target == target {
		return false
	}
	return firstRun || (p.Target == target && p.Phase != "ready")
}

// RecordBootstrapPending preserves eligibility when a brand-new installation
// starts before its accelerator/driver is available. Without this marker, the
// next daemon boot would look like an upgraded installation and skip bootstrap.
func RecordBootstrapPending(st *state.Store, message string) {
	current := st.Get()
	if !ShouldBootstrap(current, st.FirstRun()) {
		return
	}
	bootstrap, target := BootstrapModel(), catalog.DefaultModel()
	p := promotionSnapshot(current.ModelPromotion, "waiting-hardware", bootstrap, target, "", message)
	p.Rollback = bootstrap
	_ = st.SetModelPromotion(p)
}

func promotionSnapshot(previous state.ModelPromotion, phase, bootstrap, target, active, message string) state.ModelPromotion {
	previous.Phase = phase
	previous.Bootstrap = bootstrap
	previous.Target = target
	previous.Active = active
	previous.Message = message
	previous.Error = ""
	if previous.Started == "" {
		previous.Started = time.Now().UTC().Format(time.RFC3339)
	}
	return previous
}

// PromoteDefault downloads the target without interrupting the bootstrap model,
// then performs a verified cut-over. The stable cloudless-ai alias means all
// consumers follow the replacement. If either readiness or an actual chat probe
// fails, the bootstrap model is restored and the failure remains visible.
func PromoteDefault(ctx context.Context, eng engine.Engine, st *state.Store, mf *manifest.Store, logf func(string)) {
	current := st.Get()
	if !ShouldBootstrap(current, st.FirstRun()) {
		return
	}
	bootstrap, target := BootstrapModel(), catalog.DefaultModel()
	p := promotionSnapshot(current.ModelPromotion, "bootstrap", bootstrap, target, bootstrap, "Cloudless AI is available while the full model is prepared.")
	p.Rollback = bootstrap
	p.Attempts++
	_ = st.SetModelPromotion(p)

	selected := current.Engine
	if selected == "" {
		selected = catalog.DefaultEngine()
	}
	if customengine.IsCustom(selected) {
		return
	}
	app, ok := catalog.Get(selected)
	if !ok || !app.Engine || selected == "llamacpp" {
		failPromotion(st, p, errors.New("selected engine does not support background Hugging Face promotion"), bootstrap)
		return
	}

	p = promotionSnapshot(p, "downloading", bootstrap, target, bootstrap, "Downloading the full Cloudless model in the background.")
	_ = st.SetModelPromotion(p)
	logf("model promotion: downloading " + target + " while " + bootstrap + " remains available")
	if err := downloadPromotionTarget(ctx, eng, st, mf, app, target, &p); err != nil {
		failPromotion(st, p, err, bootstrap)
		logf("model promotion: download failed: " + err.Error())
		return
	}

	EngineMu.Lock()
	defer EngineMu.Unlock()
	latest := st.Get()
	latestEngine := latest.Engine
	if latestEngine == "" {
		latestEngine = catalog.DefaultEngine()
	}
	if latest.Model != "" || latest.EngineUnloaded || latestEngine != app.ID || latest.ExecutionMode == "cluster" {
		p = promotionSnapshot(p, "canceled", bootstrap, target, bootstrap, "Automatic promotion stopped because the user changed the model, engine, or execution mode.")
		_ = st.SetModelPromotion(p)
		logf("model promotion: canceled because the desired inference configuration changed")
		return
	}
	p = promotionSnapshot(p, "switching", bootstrap, target, bootstrap, "Full model downloaded. Switching the inference engine.")
	_ = st.SetModelPromotion(p)
	_ = eng.Remove(ctx, app.ContainerName())

	spec := catalog.EngineSpec(app, target)
	spec.Image = pinnedImage(ctx, mf, app)
	if pin, ok := mf.ModelPin(ctx, defaultModelPinKey()); ok && pin.Repo == target {
		spec.Args = append(append([]string{}, spec.Args...), "--revision", pin.Revision)
	}
	if _, err := eng.Run(ctx, spec); err != nil {
		_ = eng.Remove(ctx, app.ContainerName())
		rollbackPromotion(ctx, eng, st, mf, app, p, err, logf)
		return
	}
	p = promotionSnapshot(p, "verifying", bootstrap, target, target, "Verifying the full model and Cloudless chat contract.")
	_ = st.SetModelPromotion(p)
	verifyCtx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	err := waitForModelContract(verifyCtx)
	if err == nil {
		err = verifyLLMConsumers(verifyCtx, eng)
	}
	cancel()
	if err != nil {
		_ = eng.Remove(ctx, app.ContainerName())
		rollbackPromotion(ctx, eng, st, mf, app, p, err, logf)
		return
	}

	if err := st.SetModel(target); err != nil {
		_ = eng.Remove(ctx, app.ContainerName())
		rollbackPromotion(ctx, eng, st, mf, app, p, err, logf)
		return
	}
	p = promotionSnapshot(p, "ready", bootstrap, target, target, "Full Cloudless model verified and active.")
	p.TargetReady = true
	p.BytesDone = p.BytesTotal
	_ = st.SetModelPromotion(p)
	logf("model promotion: " + target + " verified and active")
}

func downloadPromotionTarget(ctx context.Context, eng engine.Engine, st *state.Store, mf *manifest.Store, app catalog.App, target string, p *state.ModelPromotion) error {
	image := pinnedImage(ctx, mf, app)
	total := promotionModelBytes(ctx, target, func() string { token, _ := st.HuggingFaceToken(); return token }())
	p.BytesTotal = total
	_ = st.SetModelPromotion(*p)
	name := "cloudless-model-promotion"
	_ = eng.Remove(ctx, name)
	python := "import os; from huggingface_hub import snapshot_download; snapshot_download(os.environ['CLOUDLESS_MODEL_ID'])"
	env := map[string]string{"CLOUDLESS_MODEL_ID": target}
	if token, _ := st.HuggingFaceToken(); strings.TrimSpace(token) != "" {
		env["HF_TOKEN"] = strings.TrimSpace(token)
	}
	result := make(chan error, 1)
	go func() {
		_, err := eng.RunTransient(ctx, engine.RunSpec{
			Name:       name,
			Image:      image,
			Env:        env,
			Volumes:    map[string]string{modelcache.Root(): "/root/.cache/huggingface"},
			EntryPoint: "python3",
			Args:       []string{"-c", python},
		})
		result <- err
	}()
	root := modelcache.Root()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			if err == nil && total > 0 {
				p.BytesDone = total
				_ = st.SetModelPromotion(*p)
			}
			return err
		case <-ticker.C:
			if root != "" {
				p.BytesDone = directorySize(filepath.Join(root, "hub", "models--"+strings.ReplaceAll(target, "/", "--")))
				p.Message = promotionDownloadMessage(p.BytesDone, p.BytesTotal)
				_ = st.SetModelPromotion(*p)
			}
		case <-ctx.Done():
			_ = eng.Remove(context.Background(), name)
			return ctx.Err()
		}
	}
}

func rollbackPromotion(ctx context.Context, eng engine.Engine, st *state.Store, mf *manifest.Store, app catalog.App, p state.ModelPromotion, cause error, logf func(string)) {
	bootstrap := p.Bootstrap
	p = promotionSnapshot(p, "rollback", bootstrap, p.Target, bootstrap, "The full model did not pass verification. Restoring the bootstrap model.")
	p.Error = cause.Error()
	_ = st.SetModelPromotion(p)
	spec := catalog.EngineSpec(app, bootstrap)
	spec.Image = pinnedImage(ctx, mf, app)
	_, rollbackErr := eng.Run(ctx, spec)
	if rollbackErr != nil {
		cause = fmt.Errorf("target failed: %v; rollback failed: %w", cause, rollbackErr)
		p.Phase = "error"
		p.Active = ""
		p.TargetReady = false
		p.Error = cause.Error()
		p.Message = "The full model failed and the bootstrap model could not be restored automatically. Use Model Manager to load a model."
		_ = st.SetModelPromotion(p)
		logf("model promotion: rollback failed: " + cause.Error())
		return
	}
	failPromotion(st, p, cause, bootstrap)
	logf("model promotion: rolled back to " + bootstrap + ": " + cause.Error())
}

func verifyLLMConsumers(ctx context.Context, eng engine.Engine) error {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, app := range catalog.All() {
		if app.LLM == nil || !app.LLM.Consumes {
			continue
		}
		container, err := eng.Find(ctx, app.ContainerName())
		if err != nil || container == nil || container.State != "running" {
			continue
		}
		port := app.PrimaryHostPort()
		if port == 0 {
			continue
		}
		path := app.Health.Path
		if path == "" {
			path = "/"
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return fmt.Errorf("%s did not survive model promotion: %w", app.Name, requestErr)
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 500 {
			return fmt.Errorf("%s did not survive model promotion: HTTP %d", app.Name, response.StatusCode)
		}
	}
	return nil
}

func failPromotion(st *state.Store, p state.ModelPromotion, err error, active string) {
	p.Phase = "error"
	p.Active = active
	p.TargetReady = false
	p.Error = err.Error()
	p.Message = "Cloudless kept the verified bootstrap model active. The full model can be retried."
	_ = st.SetModelPromotion(p)
}

func waitForModelContract(ctx context.Context) error {
	return waitForModelContractAt(ctx, "http://127.0.0.1:8000")
}

func waitForModelContractAt(ctx context.Context, endpoint string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("full model verification timed out: %w", ctx.Err())
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/models", nil)
		if response, err := client.Do(request); err == nil {
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				body, _ := json.Marshal(map[string]any{
					"model": "cloudless", "messages": []map[string]string{{"role": "user", "content": "Reply with READY."}}, "max_tokens": 8,
				})
				probe, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(body))
				probe.Header.Set("Content-Type", "application/json")
				if result, probeErr := client.Do(probe); probeErr == nil {
					result.Body.Close()
					if result.StatusCode >= 200 && result.StatusCode < 300 {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func promotionModelBytes(ctx context.Context, repo, token string) int64 {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://huggingface.co/api/models/"+repo+"?blobs=true", nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	var data struct {
		Siblings []struct {
			Size int64 `json:"size"`
		} `json:"siblings"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&data) != nil {
		return 0
	}
	var total int64
	for _, file := range data.Siblings {
		total += file.Size
	}
	return total
}

func directorySize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && entry.Type().IsRegular() {
			if info, infoErr := entry.Info(); infoErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func promotionDownloadMessage(done, total int64) string {
	if total <= 0 {
		return "Downloading the full Cloudless model in the background."
	}
	percent := float64(done) / float64(total) * 100
	return fmt.Sprintf("Downloading the full Cloudless model in the background — %.0f%%", min(100, percent))
}
