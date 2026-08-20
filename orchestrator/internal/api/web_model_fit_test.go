package api

import (
	"strings"
	"testing"
)

func TestModelManagerExplainsFitEvidenceWithoutParameterGuessing(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`unknown: 'Not reviewed'`,
		`function fitEvidence(estimate = {})`,
		`Measured profile`,
		`Reviewed requirement`,
		`No matching profile`,
		`models with a fitEstimate never fall back to a parameter-derived guess`,
		`No reviewed runtime profile matches this exact model, engine, architecture, and topology.`,
		`does not claim memory compatibility until an exact runtime profile has been reviewed`,
		`Evidence: ${escapeHtml(fitEstimate.evidenceSource)}`,
		`No reviewed distributed requirement`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("Model Manager fit-evidence contract is missing %q", want)
		}
	}
}

func TestUnknownFitProducesAnExplicitLaunchWarning(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`m.fit === 'unknown' && !clusterEligible`,
		`Downloading is safe, but launch compatibility will only be known after the engine validates it.`,
		`m.clusterFit !== 'unknown'`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("unknown-fit launch guard is missing %q", want)
		}
	}
}

func TestModelManagerExplainsUnifiedMemoryAccounting(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`function memoryAccountingSummary(cluster, distributed)`,
		`GB physical ${memory}`,
		`AI capacity`,
		`available now`,
		`protected for CloudlessOS`,
		`reclaimable cache`,
		`Per Spark: ${memoryGB(clusterEstimate.totalPerNodeGB)} physical`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("unified-memory accounting is missing %q", want)
		}
	}
}

func TestModelManagerSurfacesHardwareRecommendationEvidence(t *testing.T) {
	content, err := webFS.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, want := range []string{
		`m.hardwareRecommended`,
		`Best for this machine`,
		`Best fit for this machine`,
		`m.hardwareRecommendationReason`,
		`m.hardwareRecommendationExecution === 'cluster'`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("hardware recommendation UX is missing %q", want)
		}
	}
}
