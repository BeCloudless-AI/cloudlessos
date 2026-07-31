package api

import (
	"strings"
	"testing"
)

func TestEmbeddedWebPlaysIntroOncePerSystemBoot(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"cloudless.lastBootIntro.v1",
		"function showStartupIntro(next, bootID = '', force = false)",
		"showStartupIntro(afterIntro, (o && o.bootID) || '')",
		"'/api/system/boot-health'",
		"consecutive verified boot",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded UI is missing %q", want)
		}
	}
	if strings.Contains(page, "cloudless.firstLaunchIntro.v1") {
		t.Fatal("startup animation must not be permanently suppressed after first launch")
	}
}
