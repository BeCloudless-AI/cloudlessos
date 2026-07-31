// Package customengine adapts locally-built inference images to the same
// catalog contract used by Cloudless-managed engines.
package customengine

import (
	"strings"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/state"
)

const Prefix = "custom-"

// App converts a persisted definition into a regular engine App. Copying a
// signed base contract means custom images reuse Cloudless model substitution,
// cache volumes, loopback port binding, GPU access and OpenAI health checks.
func App(def state.CustomEngine) (catalog.App, bool) {
	base, ok := catalog.Get(def.Base)
	if !ok || !base.Engine || (def.Base != "vllm" && def.Base != "sglang") {
		return catalog.App{}, false
	}
	base.ID = def.ID
	base.Name = def.Name
	base.Description = "Locally built " + strings.TrimSuffix(def.Name, " Engine") + " runtime."
	base.Image = def.Image
	if def.ResolvedImage != "" {
		base.Image = def.ResolvedImage
	}
	// The upstream vLLM Dockerfile has `vllm serve` as its entrypoint, while
	// NVIDIA's Spark image expects those words in argv. Registration inspects the
	// image so either source-build style receives the correct model arguments.
	if def.Base == "vllm" && def.CommandMode == "vllm-entrypoint" && len(base.Command) >= 2 && base.Command[0] == "vllm" && base.Command[1] == "serve" {
		base.Command = append([]string(nil), base.Command[2:]...)
	}
	base.ArchImages = nil
	base.ArchCommands = nil
	base.Architectures = nil
	base.ArchPlatforms = nil
	base.Platforms = nil
	base.Preinstall = false
	base.Prefetch = false
	base.Verified = false
	base.SupportLevel = "experimental"
	return base, true
}

func Custom(st *state.Store) []catalog.App {
	out := make([]catalog.App, 0)
	for _, def := range st.CustomEngineList() {
		if app, ok := App(def); ok {
			out = append(out, app)
		}
	}
	return out
}

func All(st *state.Store) []catalog.App {
	out := append([]catalog.App(nil), catalog.Engines()...)
	return append(out, Custom(st)...)
}

func Get(st *state.Store, id string) (catalog.App, bool) {
	if app, ok := catalog.Get(id); ok && app.Engine {
		return app, true
	}
	for _, def := range st.CustomEngineList() {
		if def.ID == id {
			return App(def)
		}
	}
	return catalog.App{}, false
}

func IsCustom(id string) bool { return strings.HasPrefix(id, Prefix) }
