// Package provision auto-installs the apps that should ship "pre-installed" on a
// Cloudless machine (the engine and core Cloudless services) on daemon startup, wires them
// onto a shared network, and optionally pulls a default chat model so
// "Chat with your Cloudless AI" works out of the box.
package provision

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cloudless/orchestrator/internal/apps"
	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/manifest"
	"github.com/cloudless/orchestrator/internal/platform"
	"github.com/cloudless/orchestrator/internal/state"
)

// Network is the shared docker network for inter-app DNS.
const Network = "cloudless"

// EngineMu serializes engine start/stop between the startup provisioner and the
// engine-switch API so they can't clobber each other (D15).
var EngineMu sync.Mutex

// pinnedImage returns the manifest's validated digest ref for an app, else the catalog tag.
func pinnedImage(ctx context.Context, mf *manifest.Store, app catalog.App) string {
	if p, ok := mf.PinFor(ctx, app.ID, app.Image); ok {
		return p.Ref()
	}
	return app.Image
}

func defaultModelPinKey() string {
	if platform.IsDGXSpark() {
		return "dgx-spark"
	}
	return "default"
}

// Run provisions the bundled apps. Both engine images are pulled (so either is
// ready), but only the selected engine runs (others are stopped) — exactly one
// engine holds the stable alias at a time. Best-effort and idempotent; intended
// for a background goroutine.
func Run(ctx context.Context, eng engine.Engine, st *state.Store, mf *manifest.Store, logf func(string)) {
	if err := eng.EnsureNetwork(ctx, Network); err != nil {
		logf("network: " + err.Error())
	} else {
		logf("network " + Network + " ready")
	}
	if mf.Enabled() {
		if ch := mf.Channel(ctx); ch != "" {
			logf("manifest channel: " + ch)
		}
	}

	// Engines: pull all images (either is ready); the slow pulls need no lock.
	for _, e := range catalog.Engines() {
		img := pinnedImage(ctx, mf, e)
		logf(e.ID + ": pulling " + img + " …")
		if err := eng.Pull(ctx, img); err != nil {
			logf(e.ID + ": pull failed: " + err.Error())
		}
	}

	// Start/stop under the lock and re-read the desired engine inside it, so a
	// concurrent switch (which sets it + holds the same lock) isn't clobbered.
	EngineMu.Lock()
	desired := st.Get().Engine
	if desired == "" {
		desired = catalog.DefaultEngine()
	}
	model := st.Get().Model
	// Stop any non-selected engine first, so the shared port/alias is free.
	for _, e := range catalog.Engines() {
		if e.ID == desired {
			continue
		}
		if c, _ := eng.Find(ctx, e.ContainerName()); c != nil && c.State == "running" {
			logf(e.ID + ": stopping (not the active engine)")
			_ = eng.Stop(ctx, e.ContainerName())
		} else {
			logf(e.ID + ": ready (inactive)")
		}
	}
	// Ensure the selected engine is running with the stable alias.
	for _, e := range catalog.Engines() {
		if e.ID != desired {
			continue
		}
		running, hasAlias := false, false
		if c, _ := eng.Find(ctx, e.ContainerName()); c != nil && c.State == "running" {
			running = true
			hasAlias, _ = eng.HasAlias(ctx, e.ContainerName(), catalog.EngineAlias)
		}
		if running && hasAlias {
			logf(e.ID + ": active engine running")
		} else {
			_ = eng.Remove(ctx, e.ContainerName())
			spec := catalog.EngineSpec(e, model)
			spec.Image = pinnedImage(ctx, mf, e)
			// Pin the served model's revision when the default model is in use.
			served := model
			if served == "" {
				served = catalog.DefaultModel()
			}
			if override, ok := st.EngineCmd(e.ID, served); ok {
				spec.Args = override // a user-saved launch command wins verbatim
				logf(e.ID + ": using saved launch command")
			} else if mp, ok := mf.ModelPin(ctx, defaultModelPinKey()); ok && served == mp.Repo {
				args := append([]string{}, spec.Args...) // copy: don't mutate the shared catalog slice
				spec.Args = append(args, "--revision", mp.Revision)
				logf(e.ID + ": pinning model revision " + mp.Revision[:12])
			}
			if _, err := eng.Run(ctx, spec); err != nil {
				logf(e.ID + ": start failed: " + err.Error())
			} else {
				logf(e.ID + ": started (active engine)")
			}
		}
	}
	EngineMu.Unlock()

	// Non-engine bundled apps (Open WebUI and Hermes): pull and run.
	for _, app := range catalog.Bundled() {
		if app.Engine {
			continue
		}
		if c, _ := eng.Find(ctx, app.ContainerName()); c != nil && c.State == "running" {
			if app.ID != "hermes" || hermesRuntimeMatches(ctx, eng, st, app) {
				logf(app.ID + ": already running")
				continue
			}
			// Update persistent config from inside the still-running container;
			// this also migrates installations whose /opt/data is owned by the
			// official image's UID and unreadable to an unprivileged dev daemon.
			_ = eng.Exec(ctx, app.ContainerName(), "hermes", "config", "set", "model.context_length", "65536")
			_ = eng.Exec(ctx, app.ContainerName(), "hermes", "config", "set", "model.max_tokens", "4096")
			logf(app.ID + ": runtime configuration changed; recreating")
		}
		img := pinnedImage(ctx, mf, app)
		logf(app.ID + ": pulling " + img + " …")
		if err := eng.Pull(ctx, img); err != nil {
			logf(app.ID + ": pull failed: " + err.Error())
			continue
		}
		if !app.Preinstall { // prefetch-only: image ready, don't start
			logf(app.ID + ": fetched (ready to start)")
			continue
		}
		_ = eng.Remove(ctx, app.ContainerName())
		spec := app.Spec()
		spec.Image = img
		configDir := filepath.Join(st.Dir(), "apps", app.ID)
		if vols, err := apps.ConfigVolumes(configDir, app); err != nil {
			logf(app.ID + ": config failed: " + err.Error())
			continue
		} else if len(vols) > 0 {
			merged := map[string]string{}
			for host, container := range spec.Volumes {
				merged[host] = container
			}
			for host, container := range vols {
				merged[host] = container
			}
			spec.Volumes = merged
		}
		// Overlay env-injected config (e.g. Open WebUI's WEBUI_AUTH) so a setting
		// chosen in the UI persists across reboots (same logic as the API's appSpec).
		if ov := apps.EnvOverrides(configDir, app); len(ov) > 0 {
			env := map[string]string{}
			for k, v := range spec.Env {
				env[k] = v
			}
			for k, v := range ov {
				env[k] = v
			}
			spec.Env = env
		}
		if _, err := eng.Run(ctx, spec); err != nil {
			logf(app.ID + ": start failed: " + err.Error())
			continue
		}
		if app.ID == "hermes" && !hermesModelConfigMatches(ctx, eng, app.ContainerName()) {
			_ = eng.Exec(ctx, app.ContainerName(), "hermes", "config", "set", "model.context_length", "65536")
			_ = eng.Exec(ctx, app.ContainerName(), "hermes", "config", "set", "model.max_tokens", "4096")
			_ = eng.Remove(ctx, app.ContainerName())
			if _, err := eng.Run(ctx, spec); err != nil {
				logf(app.ID + ": restart after model configuration failed: " + err.Error())
				continue
			}
		}
		logf(app.ID + ": started")
	}

	// Local-network serving: match the persisted preference (default ON).
	EnsureLAN(ctx, eng, mf, PrimaryLANIP(), st.LocalNetwork(), logf)
	logf("done")
}

// hermesRuntimeMatches prevents a healthy-looking but unusable split-brain
// state: Hermes can remain running with an older API_SERVER_KEY while a newly
// upgraded cloudlessd has generated its daemon-owned credential. Never log
// either value; simply recreate the container when they differ.
func hermesRuntimeMatches(ctx context.Context, eng engine.Engine, st *state.Store, app catalog.App) bool {
	want, err := apps.HermesAPIKey(filepath.Join(st.Dir(), "apps", app.ID))
	if err != nil || want == "" {
		return false
	}
	out, err := eng.Output(ctx, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", app.ContainerName())
	if err != nil {
		return false
	}
	return hasEnvValue(out, apps.HermesAPIKeyEnv, want) &&
		hasEnvValue(out, "HERMES_MAX_TOKENS", "4096") &&
		hermesModelConfigMatches(ctx, eng, app.ContainerName())
}

func hasEnvValue(env, key, value string) bool {
	for _, line := range strings.Split(env, "\n") {
		if strings.TrimSpace(line) == key+"="+value {
			return true
		}
	}
	return false
}

func hermesModelConfigMatches(ctx context.Context, eng engine.Engine, container string) bool {
	contextLength, err := eng.Output(ctx, "exec", container, "hermes", "config", "get", "model.context_length")
	if err != nil || !hasLine(contextLength, "65536") {
		return false
	}
	maxTokens, err := eng.Output(ctx, "exec", container, "hermes", "config", "get", "model.max_tokens")
	return err == nil && hasLine(maxTokens, "4096")
}

func hasLine(output, value string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == value {
			return true
		}
	}
	return false
}

// PrimaryLANIP returns this machine's primary non-loopback IPv4 address — the one
// other devices on the network use to reach it ("" if none). The UDP "dial" sends
// no packets; it just selects the outbound interface.
func PrimaryLANIP() string {
	if c, err := net.Dial("udp", "8.8.8.8:80"); err == nil {
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok && a.IP.To4() != nil {
			return a.IP.String()
		}
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip := ipn.IP.To4(); ip != nil && !ip.IsLoopback() && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

// EnsureLAN starts or removes the per-app LAN forwarders for every running web app
// to match `enabled`. Idempotent and best-effort.
func EnsureLAN(ctx context.Context, eng engine.Engine, mf *manifest.Store, ip string, enabled bool, logf func(string)) {
	socat := catalog.SocatImage
	if p, ok := mf.Pin(ctx, "socat"); ok {
		socat = p.Ref()
	}
	if enabled && ip != "" {
		_ = eng.Pull(ctx, socat)
	}
	for _, app := range catalog.All() {
		if !app.HasWebPort() {
			continue
		}
		name := app.LanName()
		if !enabled || ip == "" {
			_ = eng.Remove(ctx, name)
			continue
		}
		// Only forward an app that's actually running.
		if c, _ := eng.Find(ctx, app.ContainerName()); c == nil || c.State != "running" {
			_ = eng.Remove(ctx, name)
			continue
		}
		if c, _ := eng.Find(ctx, name); c != nil && c.State == "running" {
			continue // already serving
		}
		_ = eng.Remove(ctx, name)
		spec := app.LanSidecarSpec(ip)
		spec.Image = socat
		if _, err := eng.Run(ctx, spec); err != nil {
			logf("lan " + app.ID + ": " + err.Error())
		} else {
			logf("lan " + app.ID + ": serving on " + ip)
		}
	}
}
