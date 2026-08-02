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
		`'installing-runtime':'starting'`,
		`'loading-model':'starting'`,
		`'loading-draft':'starting'`,
		`'compiling-kernels':'starting'`,
		`'warming-engine':'starting'`,
		`'loading-model':'Loading main model weights'`,
		`'loading-draft':'Loading DFlash draft weights'`,
		`job&&job.percent>0?job.percent`,
		`' checkpoint shards'`,
		`operationDuration(job.etaSeconds)+' remaining'`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing managed recipe startup detail %q", want)
		}
	}
}
