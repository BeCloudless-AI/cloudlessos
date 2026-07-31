package recipeops

import (
	"context"
	"sort"
)

// Presence is the read-only reconciliation status of a journaled resource.
type Presence string

const (
	PresencePresent Presence = "present"
	PresenceMissing Presence = "missing"
	PresenceUnknown Presence = "unknown"
)

// Observation compares a journal ownership claim with the current system.
type Observation struct {
	Resource Resource `json:"resource"`
	Presence Presence `json:"presence"`
	Detail   string   `json:"detail,omitempty"`
}

// Inspector performs a read-only probe for one owned resource.
type Inspector interface {
	Inspect(context.Context, Resource) Observation
}

// Inventory inspects every resource in deterministic node/kind/id order.
func Inventory(ctx context.Context, operation Operation, inspector Inspector) []Observation {
	resources := append([]Resource(nil), operation.Resources...)
	sortResources(resources)
	observations := make([]Observation, 0, len(resources))
	for _, resource := range resources {
		if err := ctx.Err(); err != nil {
			observations = append(observations, Observation{Resource: resource, Presence: PresenceUnknown, Detail: err.Error()})
			continue
		}
		observation := inspector.Inspect(ctx, resource)
		observation.Resource = resource
		if observation.Presence != PresencePresent && observation.Presence != PresenceMissing {
			observation.Presence = PresenceUnknown
		}
		observations = append(observations, observation)
	}
	sort.SliceStable(observations, func(i, j int) bool {
		return observations[i].Resource.Node < observations[j].Resource.Node
	})
	return observations
}
