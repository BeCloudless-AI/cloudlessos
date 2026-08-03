package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebExplainsManagedRecipeStartupProgress(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`'installing-runtime':'preparing-dependencies'`,
		`'loading-draft':'loading-model'`,
		`'warming-engine':'compiling-kernels'`,
		`'downloading-dependencies':'Downloading runtime dependencies'`,
		`'loading-model':'Loading main model weights'`,
		`recipeProgressComponentsHTML(job)`,
		`recipeProgressStallHTML(job)`,
		`data-retry-stalled-recipe`,
		`job&&job.percent>0?job.percent`,
		`' checkpoint shards'`,
		`operationDuration(job.etaSeconds)+' remaining'`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing managed recipe startup detail %q", want)
		}
	}
}
