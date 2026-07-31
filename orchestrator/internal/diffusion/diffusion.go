// Package diffusion is the built-in FALLBACK catalog of image (diffusion) models the
// Model Manager offers (used when the hosted "Cloudless highlights" manifest is
// unreachable). Unlike LLMs (served by the engine), diffusion models are weight FILES
// placed in ComfyUI's models folders; "getting" one = downloading the file into the
// ComfyUI volume. Curation is the reliability bet: open (non-gated) models with known,
// ComfyUI-ready single-file checkpoints. VRAM figures are rough estimates for typical
// generation.
package diffusion

// ComfyModelsPath is where ComfyUI reads model files inside its volume (mmartial image).
const ComfyModelsPath = "/comfy/mnt/ComfyUI/models"

// Model is a curated, downloadable diffusion model file.
type Model struct {
	ID          string   `json:"id"`        // stable id, e.g. "sdxl-base"
	Name        string   `json:"name"`      // display name
	Base        string   `json:"base"`      // architecture: "SDXL" | "FLUX" | "SD1.5"
	Type        string   `json:"type"`      // "checkpoint" | "lora" | "vae"
	Dir         string   `json:"dir"`       // ComfyUI subfolder: "checkpoints" | "loras" | "vae"
	File        string   `json:"file"`      // target filename
	URL         string   `json:"url"`       // direct download URL (Hugging Face resolve)
	SizeGB      float64  `json:"sizeGB"`    // download size
	MinVRAMGB   int      `json:"minVramGB"` // rough VRAM to generate comfortably
	Tags        []string `json:"tags"`      // style / capability tags
	License     string   `json:"license"`
	Description string   `json:"description"`
}

var curated = []Model{
	{ID: "sd15", Name: "Stable Diffusion 1.5", Base: "SD1.5", Type: "checkpoint", Dir: "checkpoints",
		File:   "v1-5-pruned-emaonly.safetensors",
		URL:    "https://huggingface.co/stable-diffusion-v1-5/stable-diffusion-v1-5/resolve/main/v1-5-pruned-emaonly.safetensors",
		SizeGB: 4.0, MinVRAMGB: 4, Tags: []string{"sd1.5", "classic", "low-vram"}, License: "OpenRAIL-M",
		Description: "The classic, lightweight base — fast and runs on almost anything, with a huge ecosystem."},
	{ID: "sdxl-base", Name: "Stable Diffusion XL", Base: "SDXL", Type: "checkpoint", Dir: "checkpoints",
		File:   "sd_xl_base_1.0.safetensors",
		URL:    "https://huggingface.co/stabilityai/stable-diffusion-xl-base-1.0/resolve/main/sd_xl_base_1.0.safetensors",
		SizeGB: 6.9, MinVRAMGB: 8, Tags: []string{"sdxl", "photorealistic", "versatile"}, License: "OpenRAIL++-M",
		Description: "High-quality 1024px base model — sharp, versatile, great default for SDXL."},
	{ID: "sdxl-turbo", Name: "SDXL Turbo", Base: "SDXL", Type: "checkpoint", Dir: "checkpoints",
		File:   "sd_xl_turbo_1.0_fp16.safetensors",
		URL:    "https://huggingface.co/stabilityai/sdxl-turbo/resolve/main/sd_xl_turbo_1.0_fp16.safetensors",
		SizeGB: 6.9, MinVRAMGB: 8, Tags: []string{"sdxl", "fast", "real-time"}, License: "SAI-NC",
		Description: "Generates in 1–4 steps — near real-time previews. Best for speed over peak quality."},
	{ID: "flux-schnell", Name: "FLUX.1 schnell", Base: "FLUX", Type: "checkpoint", Dir: "checkpoints",
		File:   "flux1-schnell-fp8.safetensors",
		URL:    "https://huggingface.co/Comfy-Org/flux1-schnell/resolve/main/flux1-schnell-fp8.safetensors",
		SizeGB: 17.0, MinVRAMGB: 16, Tags: []string{"flux", "high-quality", "fast"}, License: "Apache-2.0",
		Description: "Top open image model: striking quality in ~4 steps. fp8 single-file build for ComfyUI."},
}

// All returns the built-in fallback diffusion catalog.
func All() []Model { return curated }

// Get returns the model with the given id.
func Get(id string) (Model, bool) {
	for _, m := range curated {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}
