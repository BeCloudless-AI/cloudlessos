package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

const recipeContractProbeProgram = `import http.server, json, sys
port = int(sys.argv[1])
health_path = sys.argv[2]
models_path = sys.argv[3]
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == health_path:
            body = b'{"status":"ok"}'
        elif self.path == models_path:
            body = json.dumps({"object":"list","data":[{"id":"cloudless","object":"model"}]}).encode()
        else:
            self.send_response(404); self.end_headers(); return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers(); self.wfile.write(body)
    def log_message(self, format, *args):
        pass
server = http.server.ThreadingHTTPServer(("0.0.0.0", port), Handler)
server.serve_forever()`

func renderedRecipeContractText(recipe localrecipes.Recipe, workdir string) (string, error) {
	parts := []string{recipe.Runtime.Lifecycle.Start.Program, strings.Join(recipe.Runtime.Lifecycle.Start.Args, " ")}
	if script, ok := recipeInterpreterScript(recipe.Runtime.Lifecycle.Start); ok {
		path := script
		if !filepath.IsAbs(path) {
			path = filepath.Join(workdir, path)
		}
		payload, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("read rendered start script: %w", err)
		}
		parts = append(parts, string(payload))
	}
	checkout := workdir
	for {
		if _, err := os.Stat(filepath.Join(checkout, ".env.dspark")); err == nil {
			break
		}
		parent := filepath.Dir(checkout)
		if parent == checkout {
			break
		}
		checkout = parent
	}
	for _, pattern := range []string{"docker-compose*.yml", "docker-compose*.yaml", "compose*.yml", "compose*.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(checkout, pattern))
		for _, match := range matches {
			payload, err := os.ReadFile(match)
			if err != nil {
				return "", fmt.Errorf("read rendered Compose contract: %w", err)
			}
			parts = append(parts, string(payload))
		}
	}
	return strings.Join(parts, "\n"), nil
}

func validateRenderedRecipeContract(recipe localrecipes.Recipe, workdir string) (map[string]string, error) {
	if recipe.Engine.ServedModelName != localrecipes.CloudlessModelAlias {
		return nil, fmt.Errorf("served model must be %q", localrecipes.CloudlessModelAlias)
	}
	if strings.TrimRight(recipe.Engine.APIPath, "/") != "/v1" {
		return nil, errors.New("recipe API path must preserve the OpenAI-compatible /v1 contract")
	}
	if recipe.Health.Port != recipe.Engine.ContainerPort {
		return nil, errors.New("health and engine ports must identify the same private runtime")
	}
	text, err := renderedRecipeContractText(recipe, workdir)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(text)
	port := strconv.Itoa(recipe.Engine.ContainerPort)
	if !strings.Contains(text, "ENGINE_PORT") && !strings.Contains(text, port) {
		return nil, errors.New("rendered start contract does not reference the Cloudless-managed private engine port")
	}
	if !strings.Contains(text, "SERVED_MODEL_NAME") && !strings.Contains(lower, localrecipes.CloudlessModelAlias) {
		return nil, errors.New("rendered start contract does not reference the required cloudless model alias")
	}
	if !strings.Contains(text, "VLLM_HOST") && !strings.Contains(text, "0.0.0.0") && !strings.Contains(lower, "host=0.0.0.0") {
		return nil, errors.New("rendered start contract does not prove a non-loopback bind address")
	}
	return map[string]string{
		"servedModel": localrecipes.CloudlessModelAlias, "privatePort": port,
		"bindAddress": "0.0.0.0", "apiPath": "/v1",
	}, nil
}

func reserveRecipeContractPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return port, listener.Close()
}

func probeRecipeContractResponse(ctx context.Context, address string, health bool) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("contract probe returned HTTP %d", response.StatusCode)
	}
	if !health {
		return engineModelsResponseError(response.Body)
	}
	return nil
}

func (s *Server) probePreparedRecipeContract(ctx context.Context, operationID, checkout, workdir string, env map[string]string, recipe localrecipes.Recipe, image string) (map[string]string, error) {
	values, err := validateRenderedRecipeContract(recipe, workdir)
	if err != nil {
		return nil, err
	}
	port, err := reserveRecipeContractPort()
	if err != nil {
		return nil, fmt.Errorf("reserve contract probe port: %w", err)
	}
	operationSuffix := strings.TrimPrefix(operationID, "rop-")
	if len(operationSuffix) > 8 {
		operationSuffix = operationSuffix[:8]
	}
	name := "cloudless-contract-probe-" + operationSuffix
	modelsPath := strings.TrimRight(recipe.Engine.APIPath, "/") + "/models"
	args := []string{"docker", "run", "-d", "--rm", "--name", name, "--network", "host", "--entrypoint", "python3", image,
		"-c", recipeContractProbeProgram, strconv.Itoa(port), recipe.Health.Path, modelsPath}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	containerID, err := recipeCommandOutput(recipeLocalCommand(probeCtx, checkout, env, args[0], args[1:]...))
	if err != nil {
		return nil, fmt.Errorf("start image-level API contract probe: %w", err)
	}
	resource := recipeops.Resource{Kind: "container", ID: strings.TrimSpace(containerID), Node: localRecipeNodeName()}
	if err := s.claimRecipeResource(operationID, resource); err != nil {
		_, _ = recipeCommandOutput(recipeLocalCommand(context.Background(), checkout, env, "docker", "rm", "-f", name))
		return nil, err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = recipeCommandOutput(recipeLocalCommand(cleanupCtx, checkout, env, "docker", "rm", "-f", name))
		// A successful --rm probe normally removes itself before cleanup runs.
		// Inspect absence instead of treating Docker's "no such container" as
		// a failed cleanup that would leave a false ownership claim behind.
		if _, inspectErr := recipeCommandOutput(recipeLocalCommand(cleanupCtx, checkout, env, "docker", "container", "inspect", name)); inspectErr != nil {
			_ = s.releaseRecipeResource(operationID, resource)
		}
	}()
	healthAddress := fmt.Sprintf("http://127.0.0.1:%d%s", port, recipe.Health.Path)
	modelsAddress := fmt.Sprintf("http://127.0.0.1:%d%s", port, modelsPath)
	var last error
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if last = probeRecipeContractResponse(probeCtx, healthAddress, true); last == nil {
			if last = probeRecipeContractResponse(probeCtx, modelsAddress, false); last == nil {
				values["imageProbe"] = "pass"
				values["image"] = image
				return values, nil
			}
		}
		select {
		case <-probeCtx.Done():
			return nil, errors.Join(last, probeCtx.Err())
		case <-time.After(150 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("prepared image did not satisfy the lightweight Cloudless API contract: %w", last)
}
