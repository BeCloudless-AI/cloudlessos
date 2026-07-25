package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebIncludesSparkOnlyDGXDashboardSettings(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"hasCapability('dgx-appliance')",
		`html[data-cap-dgx-appliance="1"] .nvidia-brand`,
		`aria-label="NVIDIA DGX Spark"`,
		"id: 'dgx-dashboard'",
		"function renderDGXDashboardPage",
		"http://127.0.0.1:11000/",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
}

func TestEmbeddedWebUsesSparkAdaptiveRenderingWithoutReducingEngineMemory(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"root.classList.toggle('render-eco', root.dataset.cloudlessPlatform === 'dgx-spark')",
		"html.render-eco .wall::before",
		"html.render-inference-active .wall::before",
		"function sampleRenderGovernor()",
		"(Number(m.running) || 0) > 0",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing Spark rendering safeguard %q", want)
		}
	}
	for _, forbidden := range []string{"gpu-memory-utilization 0.5", "gpu_memory_utilization = 0.5"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("rendering policy must not reduce model concurrency via %q", forbidden)
		}
	}
}
