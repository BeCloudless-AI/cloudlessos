package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/engine"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

type recipeOperationInventory struct {
	Operation    recipeops.Operation     `json:"operation"`
	Observations []recipeops.Observation `json:"observations"`
}

type recipeResourceInspector struct {
	server     *Server
	recipe     localrecipes.Recipe
	localNode  string
	containers []engine.Container
	listed     bool
	peerEnv    map[string]string
}

func (i *recipeResourceInspector) Inspect(ctx context.Context, resource recipeops.Resource) recipeops.Observation {
	if resource.Node != "" && resource.Node != i.localNode {
		return i.inspectPeer(ctx, resource)
	}
	switch resource.Kind {
	case "checkout", "staging-checkout":
		info, err := os.Stat(resource.ID)
		if err == nil && info.IsDir() {
			return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: "directory exists"}
		}
		if os.IsNotExist(err) {
			return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "directory is absent"}
		}
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: cleanInventoryError(err)}
	case "image":
		if i.server.eng == nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "container engine is unavailable"}
		}
		digest, err := i.server.eng.ImageDigest(ctx, resource.ID)
		if err != nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: cleanInventoryError(err)}
		}
		if strings.TrimSpace(digest) == "" {
			return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "image is not present"}
		}
		return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: digest}
	case "container":
		return i.inspectContainer(ctx, resource.ID)
	case "container-set":
		if i.server.eng == nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "container engine is unavailable"}
		}
		ids, err := i.server.eng.ContainerNamesByLabel(ctx, "cloudless.recipe.operation", resource.ID)
		if err != nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: cleanInventoryError(err)}
		}
		if len(ids) == 0 {
			return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "no directly managed containers use this operation label"}
		}
		return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: fmt.Sprintf("%d labeled container(s)", len(ids))}
	case "compose-project":
		if i.server.eng == nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "container engine is unavailable"}
		}
		ids, err := i.server.eng.ContainerNamesByLabel(ctx, "com.docker.compose.project", resource.ID)
		if err != nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: cleanInventoryError(err)}
		}
		if len(ids) == 0 {
			return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "no containers use this Compose project"}
		}
		return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: fmt.Sprintf("%d container(s)", len(ids))}
	case "private-port", "stable-port", "rendezvous-port":
		return inspectRecipePort(ctx, resource.ID)
	case "process-set":
		return inspectLocalRecipeProcesses(resource.ID)
	case "model-cache":
		if i.server.eng == nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "container engine is unavailable"}
		}
		if recipeCachedModelReady(ctx, i.server.eng, i.recipe) {
			return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: "requested model revision is complete"}
		}
		return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "requested model revision is not complete"}
	case "model-staging":
		info, err := os.Lstat(resource.ID)
		if errors.Is(err, os.ErrNotExist) {
			return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "staging directory is absent"}
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "staging path is not a real directory"}
		}
		return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: "resumable staging directory exists"}
	default:
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "no read-only inspector is registered for this resource kind"}
	}
}

func (i *recipeResourceInspector) inspectPeer(ctx context.Context, resource recipeops.Resource) recipeops.Observation {
	if strings.TrimSpace(resource.Locator) == "" {
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "peer locator was not journaled"}
	}
	if i.peerEnv == nil {
		home := filepath.Join(recipeCheckout(i.recipe), ".cloudless-home")
		i.peerEnv = map[string]string{"HOME": home, "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	}
	peer := recipePeer{Alias: resource.Locator, Name: resource.Node}
	volume, _ := recipeCacheVolume(i.recipe)
	const probe = `kind="$1"; identity="$2"; cache_volume="$3"
case "$kind" in
	  checkout|staging-checkout) test -d "$identity" && echo present || echo missing ;;
  compose-project) ids="$(docker ps -aq --filter "label=com.docker.compose.project=$identity" 2>/dev/null)"; test -n "$ids" && printf 'present %s\n' "$(printf '%s\n' "$ids" | wc -l)" || echo missing ;;
  container) docker container inspect "$identity" >/dev/null 2>&1 && echo present || echo missing ;;
  container-set) ids="$(docker ps -aq --filter "label=cloudless.recipe.operation=$identity" 2>/dev/null)"; test -n "$ids" && printf 'present %s\n' "$(printf '%s\n' "$ids" | wc -l)" || echo missing ;;
  image) docker image inspect "$identity" >/dev/null 2>&1 && echo present || echo missing ;;
  private-port|stable-port|rendezvous-port) ss -H -ltn "sport = :$identity" 2>/dev/null | grep -q . && echo present || echo missing ;;
  process-set)
    count=0
    for environment in /proc/[0-9]*/environ; do
      if tr '\000' '\n' < "$environment" 2>/dev/null | grep -Fxq "CLOUDLESS_RECIPE_OPERATION_ID=$identity"; then count=$((count+1)); fi
    done
    test "$count" -gt 0 && printf 'present %s\n' "$count" || echo missing
    ;;
  model-staging) test -d "$identity" && echo present || echo missing ;;
  model-cache) test -d "$cache_volume" && echo cache-directory-only || echo missing ;;
  *) echo unknown ;;
esac`
	output, err := recipeCommandOutput(recipeSSHCommand(ctx, recipeCheckout(i.recipe), i.peerEnv, peer,
		"/bin/sh", "-c", probe, "cloudless-inventory", resource.Kind, resource.ID, volume))
	if err != nil {
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "peer probe failed: " + cleanInventoryError(err)}
	}
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "peer returned no inventory result"}
	}
	switch fields[0] {
	case "present":
		detail := "peer resource exists"
		if len(fields) > 1 {
			detail = fields[1] + " matching resource(s)"
		}
		return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: detail}
	case "missing":
		return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "peer resource is absent"}
	case "cache-directory-only":
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "peer cache directory exists; exact model revision requires the pre-switch content attestation"}
	default:
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "peer resource kind cannot be inspected without mutation"}
	}
}

func (i *recipeResourceInspector) inspectContainer(ctx context.Context, identity string) recipeops.Observation {
	if i.server.eng == nil {
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "container engine is unavailable"}
	}
	if !i.listed {
		containers, err := i.server.eng.List(ctx)
		if err != nil {
			return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: cleanInventoryError(err)}
		}
		i.containers, i.listed = containers, true
	}
	identity = strings.TrimPrefix(identity, "/")
	for _, container := range i.containers {
		idMatch := container.ID == identity || strings.HasPrefix(container.ID, identity) || strings.HasPrefix(identity, container.ID)
		if idMatch || strings.TrimPrefix(container.Name, "/") == identity {
			return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: container.State + " · " + container.Name}
		}
	}
	return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "container is absent"}
}

func inspectRecipePort(ctx context.Context, rawPort string) recipeops.Observation {
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "journal contains an invalid port"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", net.JoinHostPort("127.0.0.1", rawPort))
	if err == nil {
		_ = connection.Close()
		return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: "TCP listener accepts connections"}
	}
	if probeCtx.Err() != nil {
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: "TCP probe timed out"}
	}
	return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "no TCP listener accepted the probe"}
}

func inspectLocalRecipeProcesses(operationID string) recipeops.Observation {
	needle := []byte("CLOUDLESS_RECIPE_OPERATION_ID=" + operationID)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return recipeops.Observation{Presence: recipeops.PresenceUnknown, Detail: cleanInventoryError(err)}
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		environment, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			continue
		}
		for _, value := range strings.Split(string(environment), "\x00") {
			if string(needle) == value {
				count++
				break
			}
		}
	}
	if count == 0 {
		return recipeops.Observation{Presence: recipeops.PresenceMissing, Detail: "no native processes carry the operation identity"}
	}
	return recipeops.Observation{Presence: recipeops.PresencePresent, Detail: fmt.Sprintf("%d native process(es)", count)}
}

func cleanInventoryError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 240 {
		message = message[:237] + "..."
	}
	return message
}

func (s *Server) localRecipeInventory(w http.ResponseWriter, r *http.Request) {
	recipe, ok, err := s.recipes.GetAny(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "local recipe not found"})
		return
	}
	if s.recipeOpsErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recipe operation journal is unavailable: " + s.recipeOpsErr.Error()})
		return
	}
	if s.recipeOps == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recipe operation journal is unavailable"})
		return
	}
	wantedOperation := strings.TrimSpace(r.URL.Query().Get("operation"))
	inspector := &recipeResourceInspector{server: s, recipe: recipe, localNode: localRecipeNodeName()}
	result := make([]recipeOperationInventory, 0)
	for _, operation := range s.recipeOps.List() {
		if operation.RecipeID != recipe.ID || (wantedOperation != "" && operation.ID != wantedOperation) {
			continue
		}
		result = append(result, recipeOperationInventory{
			Operation: operation, Observations: recipeops.Inventory(r.Context(), operation, inspector),
		})
	}
	if wantedOperation != "" && len(result) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "recipe operation not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipe": recipe.ID, "inventories": result})
}
