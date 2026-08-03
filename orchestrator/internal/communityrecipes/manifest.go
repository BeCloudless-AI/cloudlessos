package communityrecipes

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	imageDigestPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,430}@sha256:[0-9a-fA-F]{64}$`)
	modelRevisionPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)
	licensePattern       = regexp.MustCompile(`^(Apache-2\.0|MIT|BSD-3-Clause|GPL-3\.0-only|GPL-3\.0-or-later|Proprietary)$`)
)

func mapField(parent map[string]any, key string) (map[string]any, error) {
	value, ok := parent[key].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return value, nil
}

func textField(parent map[string]any, key string) string {
	value, _ := parent[key].(string)
	return value
}

func numberField(parent map[string]any, key string) float64 {
	switch value := parent[key].(type) {
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case float64:
		return value
	case json.Number:
		result, _ := value.Float64()
		return result
	default:
		return 0
	}
}

func emptyCommand(value any) bool {
	command, ok := value.(map[string]any)
	if !ok || textField(command, "program") != "" {
		return false
	}
	arguments, ok := command["args"].([]any)
	return ok && len(arguments) == 0
}

// ValidateManagedManifest is the offline CLI safety preflight. The community
// service repeats a stricter full-schema validation and remains authoritative.
func ValidateManagedManifest(manifest map[string]any) error {
	encoded, err := json.Marshal(manifest)
	if err != nil || len(encoded) > MaxReleaseBytes {
		return errors.New("manifest is not valid JSON or exceeds 2 MiB")
	}
	manifestSchema := textField(manifest, "schema")
	if manifestSchema != ManifestSchema && manifestSchema != AdvancedManifestSchema {
		return errors.New("schema must be cloudless.recipe/v1 or cloudless.recipe/v2")
	}
	metadata, err := mapField(manifest, "metadata")
	if err != nil || len(textField(metadata, "name")) < 3 || len(textField(metadata, "description")) < 10 || textField(metadata, "version") == "" {
		return errors.New("metadata requires name, description and semantic version")
	}
	if !licensePattern.MatchString(textField(metadata, "license")) {
		return errors.New("metadata requires a supported recipe license")
	}
	if _, declared := metadata["minimumAcceleratorMemoryGB"]; declared {
		if memory := numberField(metadata, "minimumAcceleratorMemoryGB"); memory < 1 || memory > 4096 || memory != float64(int64(memory)) {
			return errors.New("metadata minimumAcceleratorMemoryGB must be a whole number from 1 to 4096")
		}
	}
	recipe, err := mapField(manifest, "recipe")
	if err != nil {
		return err
	}
	source, err := mapField(recipe, "source")
	if err != nil {
		return err
	}
	files, filesOK := source["files"].(map[string]any)
	if textField(source, "url") != "" || textField(source, "revision") != "" || !filesOK || len(files) != 0 {
		return errors.New("managed recipes cannot execute source repositories")
	}
	engine, err := mapField(recipe, "engine")
	if err != nil {
		return err
	}
	if strings.TrimSpace(textField(engine, "type")) == "" || !imageDigestPattern.MatchString(textField(engine, "image")) {
		return errors.New("container recipes require an engine type and immutable image digest")
	}
	if textField(engine, "servedModelName") != "cloudless" || numberField(engine, "containerPort") != 8890 || textField(engine, "apiPath") != "/v1" || textField(engine, "proxyHost") != "host.docker.internal" || textField(engine, "restartPolicy") != "no" {
		return errors.New("managed recipe changes the fixed Cloudless inference contract")
	}
	model, err := mapField(recipe, "model")
	if err != nil {
		return err
	}
	if !modelRevisionPattern.MatchString(textField(model, "revision")) {
		return errors.New("container recipes require a pinned model revision")
	}
	if dependencies, ok := model["dependencies"].([]any); ok {
		for _, item := range dependencies {
			dependency, ok := item.(map[string]any)
			if !ok || textField(dependency, "id") == "" || !modelRevisionPattern.MatchString(textField(dependency, "revision")) {
				return errors.New("additional models require an ID and immutable revision")
			}
		}
	}
	runtime, err := mapField(recipe, "runtime")
	adapter := textField(runtime, "adapter")
	if err != nil || (adapter != "managed-container-v1" && adapter != "advanced-container-v1") || textField(runtime, "workingDir") != "" {
		return errors.New("runtime must use managed-container-v1 or advanced-container-v1")
	}
	if adapter == "advanced-container-v1" && manifestSchema != AdvancedManifestSchema {
		return errors.New("advanced-container-v1 requires schema cloudless.recipe/v2")
	}
	distributed, err := mapField(recipe, "distributed")
	if err != nil {
		return err
	}
	nodes := numberField(distributed, "nodes")
	if adapter == "managed-container-v1" && (nodes != 1 || numberField(model, "tensorParallel") != 1 || numberField(model, "pipelineParallel") != 1) {
		return errors.New("managed-container-v1 requires one local node and one-way parallelism")
	}
	if adapter == "advanced-container-v1" && nodes > 1 {
		container, containerErr := mapField(runtime, "container")
		if nodes < 2 || nodes > 8 || numberField(model, "tensorParallel") != nodes || numberField(model, "pipelineParallel") != 1 ||
			textField(recipe, "platform") != "dgx-spark" || textField(distributed, "backend") != "nccl" || containerErr != nil ||
			textField(container, "ipc") != "host" || container["infiniband"] != true || runtime["buildOnce"] != true || runtime["downloadOnce"] != true {
			return errors.New("distributed advanced containers require 2-8 nodes, matching tensor parallelism, NCCL, host IPC, and InfiniBand")
		}
	}
	lifecycle, err := mapField(runtime, "lifecycle")
	if err != nil {
		return err
	}
	for _, phase := range []string{"build", "download", "start", "stop"} {
		if !emptyCommand(lifecycle[phase]) {
			return errors.New("managed recipes cannot provide host lifecycle commands")
		}
	}
	if adapter == "managed-container-v1" {
		if textField(engine, "entryPoint") != "" {
			return errors.New("managed-container-v1 cannot override the container entry point")
		}
		if command, declared := engine["command"]; declared {
			items, ok := command.([]any)
			if !ok || len(items) != 0 {
				return errors.New("managed-container-v1 cannot override the container command")
			}
		}
	}
	health, err := mapField(recipe, "health")
	if err != nil || textField(health, "scheme") != "http" || textField(health, "host") != "127.0.0.1" || numberField(health, "port") != 8890 {
		return errors.New("health probe must use the fixed loopback inference endpoint")
	}
	return nil
}
