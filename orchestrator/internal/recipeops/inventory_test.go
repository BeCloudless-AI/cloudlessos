package recipeops

import (
	"context"
	"testing"
)

type testInspector map[Resource]Observation

func (i testInspector) Inspect(_ context.Context, resource Resource) Observation {
	return i[resource]
}

func TestInventoryIsDeterministicAndNormalizesUnknownStatus(t *testing.T) {
	checkout := Resource{Kind: "checkout", ID: "/runtime", Node: "spark-a"}
	peerPort := Resource{Kind: "private-port", ID: "8890", Node: "spark-b"}
	operation := Operation{Resources: []Resource{peerPort, checkout}}
	got := Inventory(context.Background(), operation, testInspector{
		checkout: {Presence: PresencePresent, Detail: "directory exists"},
		peerPort: {Presence: "invalid", Detail: "peer unavailable"},
	})
	if len(got) != 2 {
		t.Fatalf("inventory = %#v", got)
	}
	if got[0].Resource != checkout || got[0].Presence != PresencePresent {
		t.Fatalf("first observation = %#v", got[0])
	}
	if got[1].Resource != peerPort || got[1].Presence != PresenceUnknown {
		t.Fatalf("second observation = %#v", got[1])
	}
}
