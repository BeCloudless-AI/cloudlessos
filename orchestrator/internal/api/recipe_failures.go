package api

import (
	"errors"
	"fmt"
	"sync"
)

// recipeBoundary names a lifecycle edge where a later failure-injection suite
// must prove rollback and resource cleanup. Production servers have no injector
// and therefore pay only a nil check; test code can fail an exact occurrence.
type recipeBoundary string

const (
	recipeBoundarySourceCheckout recipeBoundary = "source-checkout"
	recipeBoundaryImagePrepare   recipeBoundary = "image-prepare"
	recipeBoundaryPeerTransfer   recipeBoundary = "peer-transfer"
	recipeBoundaryModelDownload  recipeBoundary = "model-download"
	recipeBoundaryModelTransfer  recipeBoundary = "model-transfer"
	recipeBoundaryEngineStop     recipeBoundary = "engine-stop"
	recipeBoundaryRuntimeStart   recipeBoundary = "runtime-start"
	recipeBoundaryPrivateHealth  recipeBoundary = "private-health"
	recipeBoundaryProxyPull      recipeBoundary = "proxy-pull"
	recipeBoundaryProxyCreate    recipeBoundary = "proxy-create"
	recipeBoundaryPromotion      recipeBoundary = "promotion"
	recipeBoundaryStateCommit    recipeBoundary = "state-commit"
)

type recipeFailureDisposition string

const (
	recipeFailurePreservesCurrent recipeFailureDisposition = "preserve-current"
	recipeFailureRequiresRollback recipeFailureDisposition = "rollback"
)

// recipeBoundaryDispositions is the exhaustive lifecycle fault registry.
// Adding a production boundary without deciding its safe end state is an error,
// including when failure injection is disabled.
var recipeBoundaryDispositions = map[recipeBoundary]recipeFailureDisposition{
	recipeBoundarySourceCheckout: recipeFailurePreservesCurrent,
	recipeBoundaryImagePrepare:   recipeFailurePreservesCurrent,
	recipeBoundaryPeerTransfer:   recipeFailurePreservesCurrent,
	recipeBoundaryModelDownload:  recipeFailurePreservesCurrent,
	recipeBoundaryModelTransfer:  recipeFailurePreservesCurrent,
	recipeBoundaryEngineStop:     recipeFailurePreservesCurrent,
	recipeBoundaryRuntimeStart:   recipeFailureRequiresRollback,
	recipeBoundaryPrivateHealth:  recipeFailureRequiresRollback,
	recipeBoundaryProxyPull:      recipeFailureRequiresRollback,
	recipeBoundaryProxyCreate:    recipeFailureRequiresRollback,
	recipeBoundaryPromotion:      recipeFailureRequiresRollback,
	recipeBoundaryStateCommit:    recipeFailureRequiresRollback,
}

type recipeFailureInjector interface {
	Arrive(operationID string, boundary recipeBoundary) error
}

func (s *Server) recipeBoundary(operationID string, boundary recipeBoundary) error {
	if _, known := recipeBoundaryDispositions[boundary]; !known {
		return fmt.Errorf("unclassified recipe lifecycle boundary %q", boundary)
	}
	if s.recipeFailures == nil {
		return nil
	}
	if err := s.recipeFailures.Arrive(operationID, boundary); err != nil {
		return fmt.Errorf("injected recipe failure at %s: %w", boundary, err)
	}
	return nil
}

// recipeFailurePlan is deliberately unexported and only constructed by tests.
// There is no environment variable or HTTP endpoint which can enable failures
// on a production Cloudless installation.
type recipeFailurePlan struct {
	mu       sync.Mutex
	failures map[recipeBoundary]int
	visits   []recipeBoundary
}

func newRecipeFailurePlan(failures map[recipeBoundary]int) *recipeFailurePlan {
	copyOfFailures := make(map[recipeBoundary]int, len(failures))
	for boundary, occurrence := range failures {
		copyOfFailures[boundary] = occurrence
	}
	return &recipeFailurePlan{failures: copyOfFailures}
}

func (p *recipeFailurePlan) Arrive(_ string, boundary recipeBoundary) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.visits = append(p.visits, boundary)
	wanted, ok := p.failures[boundary]
	if !ok {
		return nil
	}
	seen := 0
	for _, visit := range p.visits {
		if visit == boundary {
			seen++
		}
	}
	if wanted <= 0 || seen == wanted {
		return errors.New("planned test failure")
	}
	return nil
}

func (p *recipeFailurePlan) snapshot() []recipeBoundary {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recipeBoundary(nil), p.visits...)
}
