package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebIncludesPersistentModelUnloadExperience(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`id="eb-load"`,
		`eb-action model-load-action hidden`,
		`btn primary model-load-action`,
		`.model-load-action .ui-icon { width: 15px; height: 15px; flex: 0 0 15px; margin: 0; }`,
		`.model-load-action > span { display: block; flex: none; }`,
		`Cloudless AI is unloaded`,
		`/api/engine/unload`,
		`/api/engine/load`,
		`/api/engine/abort`,
		`'X-Cloudless-Action': 'model-unload'`,
		`'X-Cloudless-Action': 'model-abort'`,
		`Unload model`,
		`if (engineLoadIsAbortable()) abortModelLoad();`,
		`else openModelManager();`,
		`<span>Abort</span>`,
		`grid-template-columns: auto minmax(0, 1fr) auto auto`,
		`.eb-action { grid-column: 4; grid-row: 1; }`,
		`function syncEngineState(d)`,
		`if (engineReady && operation === 'loading') operation = 'idle';`,
		`else if (m.active && engineReady)`,
		`renderSystem(); renderEngine(); }, 4000);`,
		`if (modelsOpen()) closeModelManager();`,
		"const snapshot = await api(`/api/jobs/${jobId}`);",
		`accelerator memory released`,
		`Downloaded · not loaded`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing model unload experience %q", want)
		}
	}
	if strings.Contains(page, `.eng-banner.abortable .eb-percent { display: none; }`) {
		t.Fatal("engine progress percentage is hidden instead of receiving its own layout column")
	}
	if strings.Contains(page, `if (!engineSwitching) renderEngine();`) {
		t.Fatal("engine polling is suppressed during loading, which freezes progress and leaves stale actions")
	}
	readyAction := strings.Index(page, `else if (m.active && engineReady)`)
	abortAction := strings.Index(page, `else if (m.active && engineLoadIsAbortable())`)
	if readyAction < 0 || abortAction < 0 || readyAction > abortAction {
		t.Fatal("a ready model must resolve to Unload before considering a cancellable loading action")
	}
}
