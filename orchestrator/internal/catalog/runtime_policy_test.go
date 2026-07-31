package catalog_test

import (
	"testing"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/engine"
)

func TestEveryCuratedRuntimeSatisfiesContainerBrokerPolicy(t *testing.T) {
	for _, app := range catalog.All() {
		if app.Image == "" {
			continue
		}
		t.Run(app.ID, func(t *testing.T) {
			if err := engine.ValidateRunSpec(app.Spec()); err != nil {
				t.Fatalf("curated runtime violates broker policy: %v", err)
			}
		})
	}
}
