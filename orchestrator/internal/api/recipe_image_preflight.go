package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	hostplatform "github.com/cloudless/orchestrator/internal/platform"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

var recipeImageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type recipeImagePlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

type recipeImageDescriptor struct {
	Digest   string              `json:"digest"`
	Size     int64               `json:"size"`
	Platform recipeImagePlatform `json:"platform"`
}

type recipeRegistryManifest struct {
	Digest    string                  `json:"digest"`
	Config    recipeImageDescriptor   `json:"config"`
	Layers    []recipeImageDescriptor `json:"layers"`
	Manifests []recipeImageDescriptor `json:"manifests"`
}

type recipeRegistryImage struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Config       struct {
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
	} `json:"config"`
}

type recipeImagePreflight struct {
	Reference       string
	Digest          string
	LocalID         string
	OS              string
	Architecture    string
	CompressedBytes int64
	Entrypoint      []string
	Command         []string
}

func reusablePreparedRecipeImage(expectedDigest string, image recipeImagePreflight, inspectErr error) bool {
	expectedDigest = strings.ToLower(strings.TrimSpace(expectedDigest))
	return inspectErr == nil && recipeImageDigestPattern.MatchString(expectedDigest) && image.Digest == expectedDigest
}

// prepareRecipeImageForPreflight makes the exact inspected image runnable on
// every selected node before the model download begins. Registry images are
// pulled by immutable manifest; custom images were already built by Check.
// Multi-node images are distributed from the coordinator so peers cannot race
// a mutable registry tag or independently receive different content.
func (s *Server) prepareRecipeImageForPreflight(ctx context.Context, job *jobs.Job, operationID string, recipe localrecipes.Recipe, cluster sparkcluster.State, checkout string, env map[string]string, image recipeImagePreflight) (string, error) {
	reference := image.Reference
	if recipe.Runtime.Lifecycle.Build.Program != "" {
		if !recipeImageDigestPattern.MatchString(image.LocalID) {
			return "", errors.New("checked custom runtime has no immutable local image ID")
		}
		reference = image.LocalID
	} else {
		if err := s.eng.Pull(ctx, reference); err != nil {
			return "", fmt.Errorf("pull immutable runtime image: %w", err)
		}
	}
	localImage, err := inspectLocalRecipeImage(ctx, s.eng, reference)
	if err != nil {
		return "", fmt.Errorf("inspect prepared runtime image: %w", err)
	}
	if localImage.Digest != image.Digest {
		return "", fmt.Errorf("prepared runtime image digest %s differs from checked digest %s", localImage.Digest, image.Digest)
	}
	if !recipeImageDigestPattern.MatchString(localImage.LocalID) {
		return "", errors.New("prepared runtime image has no immutable local image ID")
	}
	transferReference := "cloudless/recipe-preflight:" + strings.TrimPrefix(localImage.LocalID, "sha256:")[:16]
	if err := s.eng.TagImage(ctx, localImage.LocalID, transferReference); err != nil {
		return "", fmt.Errorf("tag immutable runtime image for node distribution: %w", err)
	}
	if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "image", ID: transferReference, Node: localRecipeNodeName()}); err != nil {
		return "", err
	}
	if _, err := s.bindPreparedRecipeImage(operationID, localImage.LocalID, localImage.LocalID); err != nil {
		return "", err
	}
	if recipe.Distributed.Nodes <= 1 {
		return localImage.LocalID, nil
	}
	peers, err := recipeDistributionPeers(recipe, cluster, env)
	if err != nil {
		return "", err
	}
	prepared := recipe
	prepared.Engine.Image = transferReference
	job.Progress("checking-image-nodes", "Placing the exact verified runtime image on every selected Spark...", 4, 10)
	if err := distributeRecipeImage(ctx, s.eng, job, prepared, checkout, env, peers); err != nil {
		return "", fmt.Errorf("distribute verified runtime image: %w", err)
	}
	for _, peer := range peers {
		if err := s.claimRecipeResource(operationID, recipeops.Resource{Kind: "image", ID: transferReference, Node: peer.Name, Locator: peer.Alias}); err != nil {
			return "", err
		}
	}
	// Every node now has this exact immutable image ID. Run probes by content
	// identity; the transfer tag exists only to make Docker save/load explicit.
	return localImage.LocalID, nil
}

func parseRecipeRegistryManifest(payload string) (recipeRegistryManifest, error) {
	var manifest recipeRegistryManifest
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &manifest); err != nil {
		return manifest, fmt.Errorf("decode registry image manifest: %w", err)
	}
	return manifest, nil
}

func selectRecipeImageManifest(manifest recipeRegistryManifest, architecture string) (string, error) {
	if len(manifest.Manifests) == 0 {
		if !recipeImageDigestPattern.MatchString(strings.ToLower(manifest.Digest)) {
			return "", errors.New("registry did not return an immutable image digest")
		}
		return strings.ToLower(manifest.Digest), nil
	}
	for _, candidate := range manifest.Manifests {
		if candidate.Platform.OS == "linux" && candidate.Platform.Architecture == architecture && recipeImageDigestPattern.MatchString(strings.ToLower(candidate.Digest)) {
			return strings.ToLower(candidate.Digest), nil
		}
	}
	return "", fmt.Errorf("image has no linux/%s manifest", architecture)
}

func recipeImageRepository(reference string) string {
	reference = strings.TrimSpace(reference)
	if index := strings.IndexByte(reference, '@'); index >= 0 {
		return reference[:index]
	}
	lastSlash := strings.LastIndexByte(reference, '/')
	if lastColon := strings.LastIndexByte(reference, ':'); lastColon > lastSlash {
		return reference[:lastColon]
	}
	return reference
}

func recipeManifestCompressedBytes(manifest recipeRegistryManifest) int64 {
	total := manifest.Config.Size
	for _, layer := range manifest.Layers {
		if layer.Size > 0 {
			total += layer.Size
		}
	}
	return total
}

func inspectRecipeRegistryImage(ctx context.Context, runtime engine.Engine, reference string) (recipeImagePreflight, error) {
	architecture := hostplatform.Architecture()
	inspectionCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	manifestJSON, err := runtime.RemoteImageManifest(inspectionCtx, reference)
	if err != nil {
		return recipeImagePreflight{}, fmt.Errorf("inspect registry image manifest: %w", err)
	}
	manifest, err := parseRecipeRegistryManifest(manifestJSON)
	if err != nil {
		return recipeImagePreflight{}, err
	}
	digest, err := selectRecipeImageManifest(manifest, architecture)
	if err != nil {
		return recipeImagePreflight{}, err
	}
	pinned := recipeImageRepository(reference) + "@" + digest
	selectedManifestJSON, err := runtime.RemoteImageManifest(inspectionCtx, pinned)
	if err != nil {
		return recipeImagePreflight{}, fmt.Errorf("inspect selected image manifest: %w", err)
	}
	selectedManifest, err := parseRecipeRegistryManifest(selectedManifestJSON)
	if err != nil {
		return recipeImagePreflight{}, err
	}
	imageJSON, err := runtime.RemoteImageConfig(inspectionCtx, pinned)
	if err != nil {
		return recipeImagePreflight{}, fmt.Errorf("inspect selected image configuration: %w", err)
	}
	var image recipeRegistryImage
	if err := json.Unmarshal([]byte(strings.TrimSpace(imageJSON)), &image); err != nil {
		return recipeImagePreflight{}, fmt.Errorf("decode selected image configuration: %w", err)
	}
	if image.OS != "linux" || image.Architecture != architecture {
		return recipeImagePreflight{}, fmt.Errorf("selected image reports %s/%s, expected linux/%s", image.OS, image.Architecture, architecture)
	}
	return recipeImagePreflight{
		Reference: pinned, Digest: digest, OS: image.OS, Architecture: image.Architecture,
		CompressedBytes: recipeManifestCompressedBytes(selectedManifest),
		Entrypoint:      append([]string(nil), image.Config.Entrypoint...),
		Command:         append([]string(nil), image.Config.Cmd...),
	}, nil
}

func inspectLocalRecipeImage(ctx context.Context, runtime engine.Engine, reference string) (recipeImagePreflight, error) {
	image, err := runtime.InspectImage(ctx, reference)
	if err != nil {
		return recipeImagePreflight{}, err
	}
	architecture := hostplatform.Architecture()
	if image.OS != "linux" || image.Architecture != architecture {
		return recipeImagePreflight{}, fmt.Errorf("local image reports %s/%s, expected linux/%s", image.OS, image.Architecture, architecture)
	}
	digest := ""
	for _, repoDigest := range image.RepoDigests {
		if _, candidate, ok := strings.Cut(repoDigest, "@"); ok && recipeImageDigestPattern.MatchString(strings.ToLower(candidate)) {
			digest = strings.ToLower(candidate)
			break
		}
	}
	if digest == "" && recipeImageDigestPattern.MatchString(strings.ToLower(image.ID)) {
		digest = strings.ToLower(image.ID)
	}
	if digest == "" {
		return recipeImagePreflight{}, errors.New("local image has no immutable content identity")
	}
	return recipeImagePreflight{
		Reference: reference, Digest: digest, OS: image.OS, Architecture: image.Architecture,
		LocalID:         strings.ToLower(strings.TrimSpace(image.ID)),
		CompressedBytes: image.Size, Entrypoint: append([]string(nil), image.EntryPoint...),
		Command: append([]string(nil), image.Command...),
	}, nil
}

func inspectPeerRecipeRegistryDigest(ctx context.Context, checkout string, env map[string]string, peer recipePeer, reference string) (string, error) {
	inspectionCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	output, err := recipeCommandOutput(recipeSSHCommand(inspectionCtx, checkout, env, peer,
		"docker", "buildx", "imagetools", "inspect", reference, "--format", "{{json .Manifest}}"))
	if err != nil {
		return "", fmt.Errorf("inspect registry image from %s: %w", peer.Name, err)
	}
	manifest, err := parseRecipeRegistryManifest(output)
	if err != nil {
		return "", fmt.Errorf("inspect registry image from %s: %w", peer.Name, err)
	}
	return selectRecipeImageManifest(manifest, hostplatform.Architecture())
}

func inspectPeerRecipeLocalImageDigest(ctx context.Context, checkout string, env map[string]string, peer recipePeer, reference string) (string, error) {
	inspectionCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	output, err := recipeCommandOutput(recipeSSHCommand(inspectionCtx, checkout, env, peer,
		"docker", "image", "inspect", reference, "--format", "{{.Id}}"))
	if err != nil {
		return "", fmt.Errorf("inspect prepared local image from %s: %w", peer.Name, err)
	}
	digest := strings.ToLower(strings.TrimSpace(output))
	if !recipeImageDigestPattern.MatchString(digest) {
		return "", fmt.Errorf("%s returned an invalid local image identity", peer.Name)
	}
	return digest, nil
}

func (result recipeImagePreflight) checkValues() map[string]string {
	values := map[string]string{
		"reference": result.Reference, "digest": result.Digest,
		"platform":        result.OS + "/" + result.Architecture,
		"compressedBytes": strconv.FormatInt(result.CompressedBytes, 10),
	}
	if len(result.Entrypoint) > 0 {
		values["entrypoint"] = strings.Join(result.Entrypoint, " ")
	}
	if len(result.Command) > 0 {
		values["command"] = strings.Join(result.Command, " ")
	}
	return values
}

func recipeImageDisplayName(reference string) string {
	return filepath.Base(recipeImageRepository(reference))
}
