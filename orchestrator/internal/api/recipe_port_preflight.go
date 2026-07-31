package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

var recipePortProcessPattern = regexp.MustCompile(`\(\("([^"]+)",pid=([0-9]+)`)

type recipePortPreflight struct {
	Port      int
	Node      string
	Listening bool
	Owner     string
	Raw       string
}

func parseRecipePortInspection(port int, node, output string) recipePortPreflight {
	output = strings.TrimSpace(output)
	result := recipePortPreflight{Port: port, Node: node, Listening: output != "", Raw: output}
	if match := recipePortProcessPattern.FindStringSubmatch(output); len(match) == 3 {
		result.Owner = match[1] + " (pid " + match[2] + ")"
	} else if output != "" {
		result.Owner = "listener identity unavailable"
	}
	return result
}

func inspectLocalRecipePort(ctx context.Context, port int, node string) (recipePortPreflight, error) {
	if port < 1 || port > 65535 {
		return recipePortPreflight{}, errors.New("port is outside the valid range")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	command := recipeLocalCommand(probeCtx, "/", nil, "ss", "-H", "-ltnp", "sport = :"+strconv.Itoa(port))
	output, err := recipeCommandOutput(command)
	if err != nil {
		return recipePortPreflight{}, fmt.Errorf("inspect TCP port %d: %w", port, err)
	}
	return parseRecipePortInspection(port, node, output), nil
}

func inspectPeerRecipePort(ctx context.Context, checkout string, env map[string]string, peer recipePeer, port int) (recipePortPreflight, error) {
	if port < 1 || port > 65535 {
		return recipePortPreflight{}, errors.New("port is outside the valid range")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	const probe = `ss -H -ltnp "sport = :$1" 2>/dev/null || true`
	output, err := recipeCommandOutput(recipeSSHCommand(probeCtx, checkout, env, peer,
		"/bin/sh", "-c", probe, "cloudless-port", strconv.Itoa(port)))
	if err != nil {
		return recipePortPreflight{}, fmt.Errorf("inspect %s TCP port %d: %w", peer.Name, port, err)
	}
	return parseRecipePortInspection(port, peer.Name, output), nil
}

func recipeOperationOwnsPort(operation recipeops.Operation, kind string, port int, node string) bool {
	want := strconv.Itoa(port)
	for _, resource := range operation.Resources {
		if resource.Kind == kind && resource.ID == want && resource.Node == node {
			return true
		}
	}
	return false
}

func (result recipePortPreflight) values(prefix string) map[string]string {
	values := map[string]string{
		prefix + "port":      strconv.Itoa(result.Port),
		prefix + "listening": strconv.FormatBool(result.Listening),
	}
	if result.Owner != "" {
		values[prefix+"owner"] = result.Owner
	}
	return values
}

func (s *Server) preflightRecipePorts(ctx context.Context, recipe localrecipes.Recipe, cluster sparkcluster.State, checkout string, env map[string]string) (map[string]string, error) {
	if recipe.Engine.ContainerPort == 8000 || recipe.Distributed.MasterPort == 8000 || recipe.Engine.ContainerPort == recipe.Distributed.MasterPort {
		return nil, errors.New("recipe private, rendezvous, and stable API ports must be distinct")
	}
	localNode := localRecipeNodeName()
	var active recipeops.Operation
	if s.recipeOps != nil {
		active, _ = s.recipeOps.ActiveForRecipe(recipe.ID)
	}
	values := map[string]string{}
	privatePort, err := inspectLocalRecipePort(ctx, recipe.Engine.ContainerPort, localNode)
	if err != nil {
		return nil, err
	}
	for key, value := range privatePort.values("local.private.") {
		values[key] = value
	}
	if privatePort.Listening && !recipeOperationOwnsPort(active, "private-port", recipe.Engine.ContainerPort, localNode) {
		return nil, fmt.Errorf("private engine port %d on %s is already owned by %s", recipe.Engine.ContainerPort, localNode, privatePort.Owner)
	}
	if recipe.Distributed.Nodes > 1 {
		rendezvous, err := inspectLocalRecipePort(ctx, recipe.Distributed.MasterPort, localNode)
		if err != nil {
			return nil, err
		}
		for key, value := range rendezvous.values("local.rendezvous.") {
			values[key] = value
		}
		if rendezvous.Listening && !recipeOperationOwnsPort(active, "rendezvous-port", recipe.Distributed.MasterPort, localNode) {
			return nil, fmt.Errorf("rendezvous port %d on %s is already owned by %s", recipe.Distributed.MasterPort, localNode, rendezvous.Owner)
		}
		peers, err := recipeDistributionPeers(recipe, cluster, env)
		if err != nil {
			return nil, err
		}
		for _, peer := range peers {
			for kind, port := range map[string]int{"private-port": recipe.Engine.ContainerPort, "rendezvous-port": recipe.Distributed.MasterPort} {
				observation, err := inspectPeerRecipePort(ctx, checkout, env, peer, port)
				if err != nil {
					return nil, err
				}
				prefix := "peer." + peer.Name + "." + strings.TrimSuffix(kind, "-port") + "."
				for key, value := range observation.values(prefix) {
					values[key] = value
				}
				if observation.Listening && !recipeOperationOwnsPort(active, kind, port, peer.Name) {
					return nil, fmt.Errorf("%s port %d on %s is already owned by %s", strings.TrimSuffix(kind, "-port"), port, peer.Name, observation.Owner)
				}
			}
		}
	}
	stable, err := inspectLocalRecipePort(ctx, 8000, localNode)
	if err != nil {
		return nil, err
	}
	for key, value := range stable.values("local.stable.") {
		values[key] = value
	}
	state := s.state.Get()
	if state.EngineUnloaded && stable.Listening {
		return nil, fmt.Errorf("stable API port 8000 is listening even though Cloudless records the engine as unloaded (%s)", stable.Owner)
	}
	if !state.EngineUnloaded && !stable.Listening {
		return nil, errors.New("Cloudless records an active engine but stable API port 8000 is not listening")
	}
	return values, nil
}
