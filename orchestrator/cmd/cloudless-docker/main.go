//go:build linux

// Command cloudless-docker is the narrow compatibility frontend installed in
// reviewed recipe PATHs. It is not Docker and cannot reach docker.sock: every
// invocation is authenticated and policy-checked by cloudless-engine.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudless/orchestrator/internal/engine"
)

func main() {
	operationID := strings.TrimSpace(os.Getenv("CLOUDLESS_RECIPE_OPERATION_ID"))
	revision := strings.TrimSpace(os.Getenv("CLOUDLESS_RECIPE_REVISION"))
	if operationID == "" || revision == "" || len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "cloudless-docker requires an authenticated recipe operation")
		os.Exit(2)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloudless-docker: determine working directory:", err)
		os.Exit(1)
	}
	environment := make(map[string]string)
	for _, key := range []string{"COMPOSE_PROJECT_NAME", "COMPOSE_DISABLE_ENV_FILE", "NODE_RANK", "HEADLESS"} {
		if value, ok := os.LookupEnv(key); ok {
			environment[key] = value
		}
	}
	output, err := engine.NewBrokerClient(os.Getenv("CLOUDLESS_ENGINE_SOCKET")).ReviewedRecipeDocker(
		context.Background(),
		engine.ReviewedRecipeDockerSpec{
			OperationID: operationID, RecipeRevision: revision, WorkingDir: workingDir,
			Args: os.Args[1:], Environment: environment,
		},
	)
	if output != "" {
		fmt.Println(output)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloudless-docker:", err)
		os.Exit(1)
	}
}
