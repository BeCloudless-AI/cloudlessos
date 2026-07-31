package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

const (
	recipeGCMinimumFreeBytes = int64(20 * 1024 * 1024 * 1024)
	recipeGCStagingAge       = 7 * 24 * time.Hour
	recipeGCOrphanAge        = 30 * 24 * time.Hour
)

type recipeGCReferences struct {
	RecipeIDs    map[string]struct{}
	ArtifactKeys map[string]struct{}
	Models       map[string]struct{}
	Operations   map[string]struct{}
}

func recipeGCReferenceSet(recipes []localrecipes.Recipe, operations []recipeops.Operation) recipeGCReferences {
	refs := recipeGCReferences{
		RecipeIDs: make(map[string]struct{}), ArtifactKeys: make(map[string]struct{}),
		Models: make(map[string]struct{}), Operations: make(map[string]struct{}),
	}
	protectRecipe := func(recipe localrecipes.Recipe) {
		if recipe.ID == "" {
			return
		}
		refs.RecipeIDs[recipe.ID] = struct{}{}
		refs.ArtifactKeys[recipeModelArtifactKey(recipe)] = struct{}{}
		refs.Models[recipe.Model.ID+"\x00"+recipe.Model.Revision] = struct{}{}
	}
	for _, recipe := range recipes {
		protectRecipe(recipe)
	}
	for _, operation := range operations {
		if operationNeedsArtifactRetention(operation) {
			protectRecipe(operation.RecipeSnapshot)
			refs.Operations[operation.ID] = struct{}{}
		}
	}
	return refs
}

func operationNeedsArtifactRetention(operation recipeops.Operation) bool {
	switch operation.Phase {
	case recipeops.PhaseStopped, recipeops.PhaseFailed, recipeops.PhaseAborted:
		return len(operationCleanupResources(operation)) > 0
	default:
		return true
	}
}

type recipeGCCandidate struct {
	Kind      string
	Identity  string
	Path      string
	Bytes     int64
	UpdatedAt time.Time
	remove    func() error
}

type recipeGCCandidateView struct {
	Kind      string `json:"kind"`
	Identity  string `json:"identity"`
	Path      string `json:"path,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

type recipeGCReport struct {
	Pressure       bool                    `json:"storagePressure"`
	AvailableBytes int64                   `json:"availableBytes"`
	TargetBytes    int64                   `json:"targetBytes"`
	Candidates     []recipeGCCandidateView `json:"candidates"`
	Removed        []recipeGCCandidateView `json:"removed,omitempty"`
	ReclaimedBytes int64                   `json:"reclaimedBytes,omitempty"`
	Errors         []string                `json:"errors,omitempty"`
}

func (s *Server) recipeCacheGarbageCollect(ctx context.Context, apply bool) (recipeGCReport, error) {
	if s.state == nil || s.recipes == nil || s.recipeOps == nil {
		return recipeGCReport{}, errors.New("recipe cache ownership stores are unavailable")
	}
	recipes, err := s.recipes.List()
	if err != nil {
		return recipeGCReport{}, err
	}
	operations := s.recipeOps.List()
	refs := recipeGCReferenceSet(recipes, operations)
	cacheRoot := ""
	if s.eng != nil {
		cacheRoot = s.modelVolumePath(ctx)
	}
	measurementRoot := cacheRoot
	if measurementRoot == "" {
		measurementRoot = s.state.Dir()
	}
	available, _, err := filesystemAvailableBytes(measurementRoot)
	if err != nil {
		return recipeGCReport{}, err
	}
	pressure := available < recipeGCMinimumFreeBytes
	now := time.Now()
	stagingAge, orphanAge := recipeGCStagingAge, recipeGCOrphanAge
	if pressure {
		stagingAge, orphanAge = 24*time.Hour, 7*24*time.Hour
	}
	var candidates []recipeGCCandidate
	appendCandidates := func(items []recipeGCCandidate, candidateErr error) error {
		if candidateErr != nil {
			return candidateErr
		}
		candidates = append(candidates, items...)
		return nil
	}
	if err := appendCandidates(collectOrphanRecipeDirectories(filepath.Join(s.state.Dir(), "recipe-checks"), "check-workspace", refs.Operations, now.Add(-stagingAge))); err != nil {
		return recipeGCReport{}, err
	}
	if err := appendCandidates(collectOrphanRecipeDirectories(recipeRuntimeRoot, "runtime-checkout", refs.RecipeIDs, now.Add(-orphanAge))); err != nil {
		return recipeGCReport{}, err
	}
	if cacheRoot != "" {
		if err := appendCandidates(collectOrphanRecipeDirectories(filepath.Join(cacheRoot, ".cloudless-staging"), "model-staging", refs.ArtifactKeys, now.Add(-stagingAge))); err != nil {
			return recipeGCReport{}, err
		}
		if err := appendCandidates(collectOrphanRecipeDirectories(filepath.Join(cacheRoot, ".cloudless-backup"), "model-backup", refs.ArtifactKeys, now.Add(-stagingAge))); err != nil {
			return recipeGCReport{}, err
		}
		if err := appendCandidates(collectObsoleteRecipeSnapshots(cacheRoot, refs, now.Add(-orphanAge))); err != nil {
			return recipeGCReport{}, err
		}
	}
	if s.eng != nil {
		if err := appendCandidates(s.collectRecipeStagingVolumes(ctx, refs, now.Add(-stagingAge))); err != nil {
			return recipeGCReport{}, err
		}
		if err := appendCandidates(s.collectObsoleteRecipeImages(ctx, recipes, operations, now.Add(-orphanAge))); err != nil {
			return recipeGCReport{}, err
		}
	}
	sortRecipeGCCandidates(candidates)
	report := recipeGCReport{Pressure: pressure, AvailableBytes: available, TargetBytes: recipeGCMinimumFreeBytes, Candidates: recipeGCCandidateViews(candidates)}
	if !apply {
		return report, nil
	}
	currentAvailable := available
	for _, candidate := range candidates {
		if pressure && currentAvailable >= recipeGCMinimumFreeBytes {
			break
		}
		if err := candidate.remove(); err != nil {
			report.Errors = append(report.Errors, candidate.Kind+" "+candidate.Identity+": "+cleanInventoryError(err))
			continue
		}
		view := recipeGCCandidateViews([]recipeGCCandidate{candidate})[0]
		report.Removed = append(report.Removed, view)
		if measured, _, measureErr := filesystemAvailableBytes(measurementRoot); measureErr == nil {
			if measured > available {
				report.ReclaimedBytes = measured - available
			}
			currentAvailable = measured
		} else {
			report.Errors = append(report.Errors, "measure reclaimed storage: "+cleanInventoryError(measureErr))
		}
	}
	return report, nil
}

func (s *Server) collectObsoleteRecipeImages(ctx context.Context, recipes []localrecipes.Recipe, operations []recipeops.Operation, cutoff time.Time) ([]recipeGCCandidate, error) {
	protected := make(map[string]struct{})
	for _, recipe := range recipes {
		protected[recipe.Engine.Image] = struct{}{}
	}
	for _, operation := range operations {
		if operationNeedsArtifactRetention(operation) {
			protected[operation.RecipeSnapshot.Engine.Image] = struct{}{}
		}
	}
	candidatesByImage := make(map[string]recipeGCCandidate)
	for _, operation := range operations {
		recipe := operation.RecipeSnapshot
		image := strings.TrimSpace(recipe.Engine.Image)
		if image == "" || recipe.Runtime.Lifecycle.Build.Program == "" || operationNeedsArtifactRetention(operation) {
			continue
		}
		if _, ok := protected[image]; ok {
			continue
		}
		updated, err := time.Parse(time.RFC3339Nano, operation.UpdatedAt)
		if err != nil || updated.After(cutoff) {
			continue
		}
		if existing, ok := candidatesByImage[image]; ok && !updated.Before(existing.UpdatedAt) {
			continue
		}
		containers, err := s.eng.ContainerNamesByAncestor(ctx, image)
		if err != nil || len(containers) != 0 {
			continue
		}
		info, err := s.eng.InspectImage(ctx, image)
		if err != nil {
			continue
		}
		size := info.Size
		if size < 0 {
			continue
		}
		candidateImage := image
		candidatesByImage[image] = recipeGCCandidate{
			Kind: "runtime-image", Identity: image, Bytes: size, UpdatedAt: updated,
			remove: func() error { return s.eng.RemoveImage(ctx, candidateImage) },
		}
	}
	candidates := make([]recipeGCCandidate, 0, len(candidatesByImage))
	for _, candidate := range candidatesByImage {
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func recipeGCCandidateViews(candidates []recipeGCCandidate) []recipeGCCandidateView {
	views := make([]recipeGCCandidateView, 0, len(candidates))
	for _, candidate := range candidates {
		views = append(views, recipeGCCandidateView{
			Kind: candidate.Kind, Identity: candidate.Identity, Path: candidate.Path,
			Bytes: candidate.Bytes, UpdatedAt: candidate.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return views
}

func (s *Server) collectRecipeStagingVolumes(ctx context.Context, refs recipeGCReferences, cutoff time.Time) ([]recipeGCCandidate, error) {
	_ = ctx
	root := filepath.Join(modelcache.Root(), ".download-staging")
	protected := make(map[string]struct{}, len(refs.ArtifactKeys))
	for key := range refs.ArtifactKeys {
		protected[key[:24]] = struct{}{}
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	candidates := make([]recipeGCCandidate, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := protected[entry.Name()]; ok {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if filepath.Dir(path) != root {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil || info.ModTime().After(cutoff) {
			continue
		}
		candidatePath := path
		candidates = append(candidates, recipeGCCandidate{
			Kind: "download-staging-directory", Identity: entry.Name(), Path: path,
			Bytes: directoryBytes(path), UpdatedAt: info.ModTime(),
			remove: func() error { return os.RemoveAll(candidatePath) },
		})
	}
	return candidates, nil
}

func (s *Server) localRecipeCacheStatus(w http.ResponseWriter, r *http.Request) {
	report, err := s.recipeCacheGarbageCollect(r.Context(), false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) localRecipeCacheCleanup(w http.ResponseWriter, r *http.Request) {
	report, err := s.recipeCacheGarbageCollect(r.Context(), true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) maybeRecipeCacheGC() {
	if s.state == nil || s.recipes == nil || s.recipeOps == nil {
		return
	}
	s.recipeGCMu.Lock()
	if !s.recipeGCLast.IsZero() && time.Since(s.recipeGCLast) < 6*time.Hour {
		s.recipeGCMu.Unlock()
		return
	}
	s.recipeGCLast = time.Now()
	s.recipeGCMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if report, err := s.recipeCacheGarbageCollect(ctx, true); err != nil {
			fmt.Printf("[recipe-cache-gc] %v\n", err)
		} else if len(report.Removed) > 0 || len(report.Errors) > 0 {
			fmt.Printf("[recipe-cache-gc] removed=%d reclaimed=%d errors=%d pressure=%t\n", len(report.Removed), report.ReclaimedBytes, len(report.Errors), report.Pressure)
		}
	}()
}

func collectOrphanRecipeDirectories(root, kind string, protected map[string]struct{}, cutoff time.Time) ([]recipeGCCandidate, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	candidates := make([]recipeGCCandidate, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := protected[entry.Name()]; ok {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if filepath.Dir(path) != root {
			return nil, fmt.Errorf("unsafe %s cache path %q", kind, path)
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		candidatePath := path
		candidates = append(candidates, recipeGCCandidate{
			Kind: kind, Identity: entry.Name(), Path: path, Bytes: directoryBytes(path), UpdatedAt: info.ModTime(),
			remove: func() error { return os.RemoveAll(candidatePath) },
		})
	}
	return candidates, nil
}

func collectObsoleteRecipeSnapshots(cacheRoot string, refs recipeGCReferences, cutoff time.Time) ([]recipeGCCandidate, error) {
	markerRoot := filepath.Join(cacheRoot, ".cloudless-complete")
	entries, err := os.ReadDir(markerRoot)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	candidates := make([]recipeGCCandidate, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".json")
		if _, protected := refs.ArtifactKeys[key]; protected {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		marker := filepath.Join(markerRoot, entry.Name())
		manifest, err := loadRecipeArtifactManifest(marker)
		if err != nil {
			// A corrupt marker is not authority to remove model data. It may be
			// removed independently after the retention window.
			candidateMarker := marker
			candidates = append(candidates, recipeGCCandidate{Kind: "invalid-manifest", Identity: key, Path: marker, UpdatedAt: info.ModTime(), remove: func() error { return os.Remove(candidateMarker) }})
			continue
		}
		if _, protected := refs.Models[manifest.ModelID+"\x00"+manifest.Revision]; protected {
			continue
		}
		cacheName, ok := modelCacheName(manifest.ModelID)
		if !ok {
			continue
		}
		repository := filepath.Join(cacheRoot, "hub", cacheName)
		snapshot := filepath.Join(repository, "snapshots", manifest.Revision)
		if recipeRevisionReferenced(repository, manifest.Revision) {
			continue
		}
		if !pathWithin(snapshot, repository) {
			return nil, errors.New("unsafe recipe model snapshot path")
		}
		candidateMarker, candidateSnapshot, candidateRepository := marker, snapshot, repository
		candidates = append(candidates, recipeGCCandidate{
			Kind: "model-revision", Identity: manifest.ModelID + "@" + manifest.Revision,
			Path: snapshot, Bytes: manifest.Bytes, UpdatedAt: info.ModTime(),
			remove: func() error {
				if err := os.RemoveAll(candidateSnapshot); err != nil {
					return err
				}
				if err := os.Remove(candidateMarker); err != nil && !os.IsNotExist(err) {
					return err
				}
				return pruneUnreferencedRecipeBlobs(candidateRepository, cutoff)
			},
		})
	}
	return candidates, nil
}

func recipeRevisionReferenced(repository, revision string) bool {
	refsRoot := filepath.Join(repository, "refs")
	referenced := false
	_ = filepath.WalkDir(refsRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || referenced {
			return nil
		}
		value, readErr := os.ReadFile(path)
		if readErr == nil && strings.TrimSpace(string(value)) == revision {
			referenced = true
		}
		return nil
	})
	return referenced
}

func pruneUnreferencedRecipeBlobs(repository string, cutoff time.Time) error {
	blobsRoot := filepath.Join(repository, "blobs")
	referenced := make(map[string]struct{})
	_ = filepath.WalkDir(filepath.Join(repository, "snapshots"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr == nil {
			resolved, _ = filepath.Abs(resolved)
			root, _ := filepath.Abs(blobsRoot)
			if pathWithin(resolved, root) {
				referenced[resolved] = struct{}{}
			}
		}
		return nil
	})
	return filepath.WalkDir(blobsRoot, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || entry.IsDir() {
			return err
		}
		absolute, absErr := filepath.Abs(path)
		if absErr != nil {
			return absErr
		}
		if _, ok := referenced[absolute]; ok || strings.HasSuffix(entry.Name(), ".incomplete") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.ModTime().After(cutoff) {
			return infoErr
		}
		return os.Remove(path)
	})
}

func sortRecipeGCCandidates(candidates []recipeGCCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].Path < candidates[j].Path
		}
		return candidates[i].UpdatedAt.Before(candidates[j].UpdatedAt)
	})
}
