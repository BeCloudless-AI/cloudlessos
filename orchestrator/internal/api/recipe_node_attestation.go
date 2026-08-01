package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

const recipeRuntimeTreeAttestationScript = `import hashlib, os, pathlib, stat, sys
root = pathlib.Path(sys.argv[1]).resolve(strict=True)
manifest = hashlib.sha256()
entries = sorted(root.rglob("*"), key=lambda path: path.relative_to(root).as_posix())
for path in entries:
    relative = path.relative_to(root).as_posix()
    if (relative == ".cloudless-home" or relative.startswith(".cloudless-home/") or
            relative == ".git" or relative.startswith(".git/")):
        continue
    info = path.lstat()
    mode = oct(stat.S_IMODE(info.st_mode)).encode()
    if path.is_symlink():
        manifest.update(b"L\0" + relative.encode() + b"\0" + mode + b"\0" + os.readlink(path).encode() + b"\n")
    elif path.is_file():
        digest = hashlib.sha256()
        with path.open("rb") as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b""):
                digest.update(chunk)
        manifest.update(b"F\0" + relative.encode() + b"\0" + mode + b"\0" + str(info.st_size).encode() + b"\0" + digest.hexdigest().encode() + b"\n")
print("sha256:" + manifest.hexdigest())`

type recipeNodeAttestation struct {
	Node           string `json:"node"`
	Source         string `json:"source"`
	Image          string `json:"image"`
	Model          string `json:"model"`
	Runtime        string `json:"runtime"`
	Combined       string `json:"combined"`
	OperationID    string `json:"operationId"`
	RecipeRevision string `json:"recipeRevision"`
}

func attestRecipeNodes(ctx context.Context, recipe localrecipes.Recipe, operation recipeops.Operation, cluster sparkcluster.State, checkout string, env map[string]string, imageDigest, modelDigest string) ([]recipeNodeAttestation, error) {
	if strings.TrimSpace(operation.ResolvedSourceRevision) == "" || strings.TrimSpace(imageDigest) == "" || strings.TrimSpace(modelDigest) == "" {
		return nil, errors.New("source, image and model identities must be known before node attestation")
	}
	localRuntime, err := recipeRuntimeTreeDigest(ctx, checkout, env, nil)
	if err != nil {
		return nil, fmt.Errorf("attest generated runtime on %s: %w", localRecipeNodeName(), err)
	}
	attestations := []recipeNodeAttestation{newRecipeNodeAttestation(localRecipeNodeName(), operation, imageDigest, modelDigest, localRuntime)}
	if recipe.Distributed.Nodes <= 1 {
		return attestations, nil
	}
	peers, err := recipeDistributionPeers(recipe, cluster, env)
	if err != nil {
		return nil, err
	}
	for _, peer := range peers {
		peerImage, imageErr := recipePeerAttestedImageDigest(ctx, recipe, checkout, env, peer)
		if imageErr != nil {
			return nil, fmt.Errorf("attest runtime image on %s: %w", peer.Name, imageErr)
		}
		if peerImage != imageDigest {
			return nil, fmt.Errorf("runtime image on %s differs from %s: %s != %s", peer.Name, localRecipeNodeName(), peerImage, imageDigest)
		}
		peerRuntime, digestErr := recipeRuntimeTreeDigest(ctx, checkout, env, &peer)
		if digestErr != nil {
			return nil, fmt.Errorf("attest generated runtime on %s: %w", peer.Name, digestErr)
		}
		// Distributed recipes legitimately render node-specific runtime files
		// (for example rank, address, interface and Compose environment). Their
		// source revision, immutable image and model snapshot must be identical,
		// but requiring the generated working trees themselves to be byte-for-byte
		// equal rejects correct coordinator/worker configurations. Retain each
		// digest in the operation attestation so the exact per-node runtime remains
		// auditable without mistaking intentional node configuration for drift.
		attestations = append(attestations, newRecipeNodeAttestation(peer.Name, operation, peerImage, modelDigest, peerRuntime))
	}
	sort.Slice(attestations, func(i, j int) bool { return attestations[i].Node < attestations[j].Node })
	return attestations, nil
}

func recipePeerAttestedImageDigest(ctx context.Context, recipe localrecipes.Recipe, checkout string, env map[string]string, peer recipePeer) (string, error) {
	if recipe.Runtime.Lifecycle.Build.Program == "" && !recipeImageDigestPattern.MatchString(strings.ToLower(strings.TrimSpace(recipe.Engine.Image))) {
		return inspectPeerRecipeRegistryDigest(ctx, checkout, env, peer, recipe.Engine.Image)
	}
	return inspectPeerRecipeLocalImageDigest(ctx, checkout, env, peer, recipe.Engine.Image)
}

func recipeRuntimeTreeDigest(ctx context.Context, localCheckout string, env map[string]string, peer *recipePeer) (string, error) {
	args := []string{"/usr/bin/python3", "-c", recipeRuntimeTreeAttestationScript, localCheckout}
	var commandOutput string
	var err error
	if peer == nil {
		commandOutput, err = recipeCommandOutput(recipeLocalCommand(ctx, localCheckout, env, args[0], args[1:]...))
	} else {
		args[len(args)-1] = peer.Checkout
		commandOutput, err = recipeCommandOutput(recipeSSHCommand(ctx, localCheckout, env, *peer, args...))
	}
	if err != nil {
		return "", err
	}
	digest := strings.ToLower(strings.TrimSpace(commandOutput))
	if !recipeImageDigestPattern.MatchString(digest) {
		return "", errors.New("node returned an invalid runtime-tree digest")
	}
	return digest, nil
}

func newRecipeNodeAttestation(node string, operation recipeops.Operation, imageDigest, modelDigest, runtimeDigest string) recipeNodeAttestation {
	parts := []string{operation.ResolvedSourceRevision, imageDigest, modelDigest, runtimeDigest, operation.RecipeRevision, operation.ID}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return recipeNodeAttestation{
		Node: node, Source: operation.ResolvedSourceRevision, Image: imageDigest, Model: modelDigest,
		Runtime: runtimeDigest, Combined: "sha256:" + hex.EncodeToString(digest[:]),
		OperationID: operation.ID, RecipeRevision: operation.RecipeRevision,
	}
}

func recipeNodeAttestationCheck(attestations []recipeNodeAttestation) recipeops.CheckResult {
	values := make(map[string]string, len(attestations)*5)
	for index, attestation := range attestations {
		prefix := fmt.Sprintf("node.%d.", index)
		values[prefix+"name"] = attestation.Node
		values[prefix+"source"] = attestation.Source
		values[prefix+"image"] = attestation.Image
		values[prefix+"model"] = attestation.Model
		values[prefix+"runtime"] = attestation.Runtime
		values[prefix+"combined"] = attestation.Combined
	}
	return recipeops.CheckResult{ID: "node-consistency", Status: recipeops.CheckPass,
		Summary: fmt.Sprintf("Source, image and model match; per-node runtime identities recorded across %d selected node(s)", len(attestations)), Values: values}
}

func recipePeerRuntimePath(peer recipePeer, recipe localrecipes.Recipe) string {
	return filepath.Clean(filepath.Join(peer.Checkout, recipe.Runtime.WorkingDir))
}
