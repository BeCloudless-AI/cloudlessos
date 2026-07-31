package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudless/orchestrator/internal/catalog"
	"github.com/cloudless/orchestrator/internal/distributedprofiles"
	"github.com/cloudless/orchestrator/internal/models"
	"github.com/cloudless/orchestrator/internal/platform"
)

// reviewedDistributedProfile is the fail-closed boundary between the generic
// cluster machinery and a model launch. Cluster health alone is not evidence
// that an arbitrary artifact/runtime/topology combination is safe to run.
func reviewedDistributedProfile(modelID, engineID string, nodes int) (distributedprofiles.Profile, error) {
	model, ok := models.Get(resolveModel(modelID))
	if !ok {
		return distributedprofiles.Profile{}, fmt.Errorf("model %q has no reviewed distributed launch contract", resolveModel(modelID))
	}
	return distributedprofiles.Resolve(model, distributedprofiles.Environment{
		Engine:       engineID,
		Architecture: platform.Architecture(),
		Platform:     platform.Detect(),
		MemoryType:   "unified",
		Nodes:        nodes,
	})
}

// reviewedDistributedImage returns only an immutable image reference which was
// either supplied by an already-verified update operation or selected by the
// signed Cloudless manifest. Distributed launches never fall back to a tag.
func (s *Server) reviewedDistributedImage(ctx context.Context, target catalog.App, exactImage string) (string, error) {
	if exactImage != "" {
		if !strings.Contains(exactImage, "@sha256:") {
			return "", errors.New("the reviewed distributed engine image is not pinned to a digest")
		}
		return exactImage, nil
	}
	if s.manifest == nil {
		return "", errors.New("the signed engine manifest is unavailable")
	}
	pin, ok := s.manifest.PinFor(ctx, target.ID, target.Image)
	if !ok || !pin.Verified || pin.CurrentDigest() == "" {
		return "", errors.New("the signed manifest has no verified engine image for this architecture")
	}
	ref := pin.Ref()
	if ref == "" {
		return "", errors.New("the signed engine image pin is incomplete")
	}
	return ref, nil
}
