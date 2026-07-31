package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

const recipeDiagnosticLogLimit = 64 * 1024

type recipeDiagnosticDigests struct {
	RecipeRevision         string `json:"recipeRevision"`
	ResolvedSourceRevision string `json:"resolvedSourceRevision,omitempty"`
	ImageDigest            string `json:"imageDigest,omitempty"`
	PreparedImageReference string `json:"preparedImageReference,omitempty"`
	RuntimeArtifactDigest  string `json:"runtimeArtifactDigest,omitempty"`
	PreflightEvidenceHash  string `json:"preflightEvidenceHash,omitempty"`
	PlatformFingerprint    string `json:"platformFingerprint,omitempty"`
	ClusterFingerprint     string `json:"clusterFingerprint,omitempty"`
}

func (s *Server) localRecipeDiagnostics(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.GetAny(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read local recipe"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	if s.recipeOpsErr != nil || s.recipeOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recipe operation journal is unavailable"})
		return
	}
	operation, ok := s.recipeOps.Get(strings.TrimSpace(r.PathValue("operation")))
	if !ok || operation.RecipeID != recipe.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "recipe operation not found"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	inspector := &recipeResourceInspector{server: s, recipe: recipe, localNode: localRecipeNodeName()}
	inventory := recipeOperationInventory{Operation: sanitizeRecipeDiagnosticOperation(operation), Observations: recipeops.Inventory(ctx, operation, inspector)}
	logs := s.recipeDiagnosticLogs(ctx, operation, recipe)
	bundle, err := buildRecipeDiagnosticBundle(operation, recipe, inventory, clusterCompute(ctx, totalVRAMGB(ctx)), logs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not build recipe diagnostic bundle"})
		return
	}
	filename := fmt.Sprintf("cloudless-recipe-%s-%s.zip", safeRecipeDiagnosticName(recipe.ID), safeRecipeDiagnosticName(operation.ID))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(len(bundle)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bundle)
}

func buildRecipeDiagnosticBundle(operation recipeops.Operation, recipe localrecipes.Recipe, inventory recipeOperationInventory, topology any, logs map[string]string) ([]byte, error) {
	knownSecrets := recipeDiagnosticSecretValues(recipe)
	operation = sanitizeRecipeDiagnosticOperation(operation)
	recipe = sanitizeRecipeDiagnosticRecipe(recipe)
	inventory.Operation = operation
	for index := range inventory.Observations {
		inventory.Observations[index].Detail = redactRecipeDiagnosticText(inventory.Observations[index].Detail)
	}
	failure := buildRecipeFailureView(operation)
	digests := recipeDiagnosticDigests{
		RecipeRevision: operation.RecipeRevision, ResolvedSourceRevision: operation.ResolvedSourceRevision,
		RuntimeArtifactDigest: recipe.Runtime.ArtifactDigest, PreparedImageReference: operation.PreparedImageReference,
	}
	if operation.PreparedImageDigest != "" {
		digests.ImageDigest = operation.PreparedImageDigest
	}
	if operation.Preflight != nil {
		if digests.ImageDigest == "" {
			digests.ImageDigest = operation.Preflight.ImageDigest
		}
		digests.PreflightEvidenceHash = operation.Preflight.EvidenceHash
		digests.PlatformFingerprint = operation.Preflight.PlatformFingerprint
		digests.ClusterFingerprint = operation.Preflight.ClusterFingerprint
	}

	files := map[string]any{
		"operation.json": operation,
		"failure.json":   failure,
		"recipe.json":    recipe,
		"inventory.json": inventory,
		"topology.json":  topology,
		"digests.json":   digests,
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	readme, err := archive.Create("README.txt")
	if err != nil {
		return nil, err
	}
	_, _ = readme.Write([]byte("Cloudless recipe diagnostic bundle\n\nEnvironment values are omitted and detected credentials are redacted. Logs are bounded and may be incomplete when a node is unavailable.\n"))
	for _, name := range []string{"operation.json", "failure.json", "recipe.json", "inventory.json", "topology.json", "digests.json"} {
		payload, err := json.MarshalIndent(files[name], "", "  ")
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		entry, err := archive.Create(name)
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		if _, err := entry.Write(payload); err != nil {
			_ = archive.Close()
			return nil, err
		}
	}
	logNames := make([]string, 0, len(logs))
	for name := range logs {
		logNames = append(logNames, name)
	}
	sort.Strings(logNames)
	for _, name := range logNames {
		entry, err := archive.Create(filepath.ToSlash(filepath.Join("logs", safeRecipeDiagnosticName(name)+".log")))
		if err != nil {
			_ = archive.Close()
			return nil, err
		}
		if _, err := entry.Write([]byte(boundedRecipeDiagnosticLog(logs[name], knownSecrets))); err != nil {
			_ = archive.Close()
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func sanitizeRecipeDiagnosticOperation(operation recipeops.Operation) recipeops.Operation {
	operation.RecipeSnapshot = sanitizeRecipeDiagnosticRecipe(operation.RecipeSnapshot)
	operation.Error = redactRecipeDiagnosticText(operation.Error)
	if operation.Progress != nil {
		copy := *operation.Progress
		copy.Message = redactRecipeDiagnosticText(copy.Message)
		copy.Stage = redactRecipeDiagnosticText(copy.Stage)
		operation.Progress = &copy
	}
	for index := range operation.Checks {
		operation.Checks[index].Detail = redactRecipeDiagnosticText(operation.Checks[index].Detail)
		for key, value := range operation.Checks[index].Values {
			operation.Checks[index].Values[key] = redactRecipeDiagnosticValue(key, value)
		}
	}
	if operation.Preflight != nil {
		copy := *operation.Preflight
		copy.Checks = append([]recipeops.CheckResult(nil), copy.Checks...)
		for index := range copy.Checks {
			copy.Checks[index].Detail = redactRecipeDiagnosticText(copy.Checks[index].Detail)
			for key, value := range copy.Checks[index].Values {
				copy.Checks[index].Values[key] = redactRecipeDiagnosticValue(key, value)
			}
		}
		operation.Preflight = &copy
	}
	return operation
}

func sanitizeRecipeDiagnosticRecipe(recipe localrecipes.Recipe) localrecipes.Recipe {
	recipe.Source.URL = redactRecipeDiagnosticText(recipe.Source.URL)
	recipe.SourceURL = redactRecipeDiagnosticText(recipe.SourceURL)
	recipe.Runtime.Environment = recipeDiagnosticEnvironmentKeys(recipe.Runtime.Environment)
	recipe.Runtime.Lifecycle.Build = sanitizeRecipeDiagnosticCommand(recipe.Runtime.Lifecycle.Build)
	recipe.Runtime.Lifecycle.Download = sanitizeRecipeDiagnosticCommand(recipe.Runtime.Lifecycle.Download)
	recipe.Runtime.Lifecycle.Start = sanitizeRecipeDiagnosticCommand(recipe.Runtime.Lifecycle.Start)
	recipe.Runtime.Lifecycle.Stop = sanitizeRecipeDiagnosticCommand(recipe.Runtime.Lifecycle.Stop)
	return recipe
}

func recipeDiagnosticEnvironmentKeys(environment map[string]string) map[string]string {
	if len(environment) == 0 {
		return nil
	}
	result := make(map[string]string, len(environment))
	for key := range environment {
		result[key] = "<redacted>"
	}
	return result
}

func sanitizeRecipeDiagnosticCommand(command localrecipes.Command) localrecipes.Command {
	command.Program = redactRecipeDiagnosticText(command.Program)
	command.Args = append([]string(nil), command.Args...)
	redactNext := false
	for index, argument := range command.Args {
		lower := strings.ToLower(strings.TrimSpace(argument))
		if redactNext {
			command.Args[index] = "<redacted>"
			redactNext = false
			continue
		}
		if recipeDiagnosticSecretKey(strings.TrimLeft(lower, "-")) {
			redactNext = true
		}
		command.Args[index] = redactRecipeDiagnosticText(argument)
	}
	return command
}

func redactRecipeDiagnosticValue(key, value string) string {
	if recipeDiagnosticSecretKey(key) {
		return "<redacted>"
	}
	return redactRecipeDiagnosticText(value)
}

func recipeDiagnosticSecretKey(key string) bool {
	key = strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(key)))
	for _, fragment := range []string{"token", "password", "passwd", "secret", "apikey", "accesskey", "privatekey", "credential"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func boundedRecipeDiagnosticLog(value string, knownSecrets []string) string {
	if len(value) > recipeDiagnosticLogLimit {
		value = value[len(value)-recipeDiagnosticLogLimit:]
		value = "[earlier output omitted]\n" + value
	}
	for _, secret := range knownSecrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	return redactRecipeDiagnosticTextLimit(value, 0)
}

func recipeDiagnosticSecretValues(recipe localrecipes.Recipe) []string {
	values := make([]string, 0, len(recipe.Runtime.Environment)+4)
	for _, value := range recipe.Runtime.Environment {
		if strings.TrimSpace(value) != "" {
			values = append(values, value)
		}
	}
	for _, command := range []localrecipes.Command{recipe.Runtime.Lifecycle.Build, recipe.Runtime.Lifecycle.Download, recipe.Runtime.Lifecycle.Start, recipe.Runtime.Lifecycle.Stop} {
		redactNext := false
		for _, argument := range command.Args {
			if redactNext && strings.TrimSpace(argument) != "" {
				values = append(values, argument)
				redactNext = false
				continue
			}
			redactNext = recipeDiagnosticSecretKey(strings.TrimLeft(strings.ToLower(strings.TrimSpace(argument)), "-"))
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

func safeRecipeDiagnosticName(value string) string {
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			result.WriteRune(character)
		} else {
			result.WriteByte('-')
		}
	}
	name := strings.Trim(result.String(), "-.")
	if name == "" {
		return "unknown"
	}
	return name
}

func (s *Server) recipeDiagnosticLogs(ctx context.Context, operation recipeops.Operation, recipe localrecipes.Recipe) map[string]string {
	logs := make(map[string]string)
	for _, resource := range operation.Resources {
		if resource.Kind != "container" && resource.Kind != "container-set" && resource.Kind != "compose-project" {
			continue
		}
		name := resource.Node + "-" + resource.ID
		if resource.Node == "" || resource.Node == localRecipeNodeName() {
			if s.eng == nil {
				continue
			}
			identities := []string{resource.ID}
			if resource.Kind != "container" {
				label := "cloudless.recipe.operation=" + resource.ID
				if resource.Kind == "compose-project" {
					label = "com.docker.compose.project=" + resource.ID
				}
				key, value, found := strings.Cut(label, "=")
				if !found {
					logs[name] = "Container discovery failed: invalid ownership label"
					continue
				}
				var err error
				identities, err = s.eng.ContainerNamesByLabel(ctx, key, value)
				if err != nil {
					logs[name] = "Container discovery failed: " + err.Error()
					continue
				}
			}
			for _, identity := range identities {
				logName := name
				if len(identities) > 1 || resource.Kind != "container" {
					logName += "-" + identity
				}
				if output, err := s.eng.LogsTail(ctx, identity, 200); err == nil {
					logs[logName] = output
				} else {
					logs[logName] = "Log collection failed: " + err.Error()
				}
			}
			continue
		}
		if resource.Locator == "" {
			continue
		}
		peer := recipePeer{Alias: resource.Locator, Name: resource.Node}
		home := filepath.Join(recipeCheckout(recipe), ".cloudless-home")
		environment := map[string]string{"HOME": home, "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
		const script = `kind="$1"; identity="$2"
case "$kind" in
  container) ids="$identity" ;;
  container-set) ids="$(docker ps -aq --filter "label=cloudless.recipe.operation=$identity" 2>/dev/null)" ;;
  compose-project) ids="$(docker ps -aq --filter "label=com.docker.compose.project=$identity" 2>/dev/null)" ;;
esac
for id in $ids; do
  printf '\n===== container %s =====\n' "$id"
  docker logs --tail 200 "$id" 2>&1 || true
done`
		output, err := recipeCommandOutput(recipeSSHCommand(ctx, recipeCheckout(recipe), environment, peer, "/bin/sh", "-c", script, "cloudless-diagnostics", resource.Kind, resource.ID))
		if err != nil {
			logs[name] = "Peer log collection failed: " + err.Error()
		} else {
			logs[name] = output
		}
	}
	return logs
}
