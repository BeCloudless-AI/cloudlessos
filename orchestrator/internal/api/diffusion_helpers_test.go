package api

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/diffusion"
)

func TestSafeDiffusionFile(t *testing.T) {
	if !safeDiffusionFile(diffusion.Model{File: "model.safetensors", Dir: "checkpoints"}) {
		t.Fatal("valid diffusion model was rejected")
	}
	for _, model := range []diffusion.Model{
		{File: "../model.safetensors", Dir: "checkpoints"},
		{File: "model.txt", Dir: "checkpoints"},
		{File: "model.safetensors", Dir: "../outside"},
		{File: "model.safetensors", Dir: "/outside"},
	} {
		if safeDiffusionFile(model) {
			t.Errorf("unsafe diffusion model accepted: %#v", model)
		}
	}
}
