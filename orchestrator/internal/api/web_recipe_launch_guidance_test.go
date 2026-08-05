package api

import (
	"strings"
	"testing"
)

func TestEmbeddedRecipeLaunchGuidanceAppearsOnlyAfterSuccess(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		"function maybeShowFirstRecipeLaunchGuidance()",
		"'/api/guidance/recipe-launch'",
		"title: 'Your recipe is ready'",
		"Connect Hermes Desktop",
		"Create an API key",
		"confirmLabel: 'Open the Guide'",
		"cancelLabel: 'I know'",
		"openGuide('hermes')",
		"if (!checking && operation === 'run') maybeShowFirstRecipeLaunchGuidance();",
		"if (ok && !checking && operation === 'run') maybeShowFirstRecipeLaunchGuidance();",
		"maybeShowRuntimeRestartOffer().then(() => setTimeout(maybeShowFirstRecipeLaunchGuidance, 700));",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("embedded post-recipe guidance is missing %q", want)
		}
	}
}
