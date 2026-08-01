package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/modelcache"
	"github.com/cloudless/orchestrator/internal/recipeops"
)

// ReviewedRecipeDockerSpec is the complete identity attached to one Docker
// invocation made by an exact, package-reviewed recipe lifecycle script.
// Callers do not confer trust: the root broker reopens the durable operation
// journal and the pinned checkout before admitting the request.
type ReviewedRecipeDockerSpec struct {
	OperationID    string            `json:"operationId"`
	RecipeRevision string            `json:"recipeRevision"`
	WorkingDir     string            `json:"workingDir"`
	Args           []string          `json:"args"`
	Environment    map[string]string `json:"environment,omitempty"`
}

// ReviewedRecipePolicy independently authenticates the narrow legacy
// source-scripts-v1 adapter. It deliberately does not authorize arbitrary
// user-authored recipes or provide a general Docker CLI escape hatch.
type ReviewedRecipePolicy struct {
	StateDir    string
	RuntimeRoot string
}

func (p ReviewedRecipePolicy) Authorize(spec ReviewedRecipeDockerSpec) error {
	_, err := p.authenticate(spec)
	return err
}

func (p ReviewedRecipePolicy) authenticate(spec ReviewedRecipeDockerSpec) (recipeops.Operation, error) {
	stateDir := filepath.Clean(strings.TrimSpace(p.StateDir))
	runtimeRoot := filepath.Clean(strings.TrimSpace(p.RuntimeRoot))
	if !filepath.IsAbs(stateDir) || !filepath.IsAbs(runtimeRoot) {
		return recipeops.Operation{}, errors.New("reviewed recipe policy roots must be absolute")
	}
	store, err := recipeops.Open(stateDir)
	if err != nil {
		return recipeops.Operation{}, fmt.Errorf("open recipe operation journal: %w", err)
	}
	operation, ok := store.Get(strings.TrimSpace(spec.OperationID))
	if !ok {
		return recipeops.Operation{}, errors.New("recipe operation is not registered")
	}
	if operation.RecipeRevision == "" ||
		operation.RecipeRevision != strings.TrimSpace(spec.RecipeRevision) {
		return recipeops.Operation{}, errors.New("recipe operation revision does not match")
	}
	revision, err := recipeops.RecipeRevision(operation.RecipeSnapshot)
	if err != nil || revision != operation.RecipeRevision {
		return recipeops.Operation{}, errors.New("recipe operation snapshot is invalid")
	}
	if _, ok := localrecipes.ReviewedProfile(operation.RecipeSnapshot); !ok {
		return recipeops.Operation{}, errors.New("recipe is not an exact signed Cloudless profile")
	}
	if operation.Terminal() && operation.Phase != recipeops.PhaseActive {
		return recipeops.Operation{}, errors.New("recipe operation is no longer executable")
	}

	checkout := filepath.Join(runtimeRoot, operation.RecipeID)
	if operation.Kind == recipeops.KindCheck {
		checkout = filepath.Join(stateDir, "recipe-checks", operation.ID)
	}
	if err := requireResolvedContainedPath(spec.WorkingDir, checkout); err != nil {
		return recipeops.Operation{}, fmt.Errorf("invalid recipe working directory: %w", err)
	}
	if err := verifyReviewedCheckout(operation.RecipeSnapshot, checkout); err != nil {
		return recipeops.Operation{}, err
	}
	if err := authorizeReviewedEnvironment(operation, spec.Environment); err != nil {
		return recipeops.Operation{}, err
	}
	if err := authorizeReviewedDockerArgs(operation, checkout, stateDir, spec.Args); err != nil {
		return recipeops.Operation{}, err
	}
	return operation, nil
}

func authorizeReviewedEnvironment(operation recipeops.Operation, environment map[string]string) error {
	runtimeName, err := recipeops.RuntimeName(operation)
	if err != nil {
		return err
	}
	for key, value := range environment {
		switch key {
		case "COMPOSE_PROJECT_NAME":
			if value != runtimeName {
				return errors.New("reviewed recipe Compose project identity does not match")
			}
		case "COMPOSE_DISABLE_ENV_FILE":
			if value != "" && value != "0" && value != "1" {
				return errors.New("reviewed recipe Compose environment control is invalid")
			}
		case "NODE_RANK":
			if value != "0" && value != "1" {
				return errors.New("reviewed recipe node rank is invalid")
			}
		case "HEADLESS":
			if value != "" && value != "1" {
				return errors.New("reviewed recipe headless flag is invalid")
			}
		default:
			return fmt.Errorf("reviewed recipe environment key %q is not admitted", key)
		}
	}
	return nil
}

func verifyReviewedCheckout(recipe localrecipes.Recipe, checkout string) error {
	if len(recipe.Source.Files) == 0 {
		return errors.New("reviewed recipe has no signed file inventory")
	}
	head, err := reviewedGitOutput(checkout, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve reviewed recipe checkout: %w", err)
	}
	if strings.TrimSpace(string(head)) != recipe.Source.Revision {
		return errors.New("reviewed recipe checkout is not at its signed revision")
	}
	for name, expected := range recipe.Source.Files {
		clean := filepath.Clean(name)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return errors.New("reviewed recipe file inventory is invalid")
		}
		payload, err := reviewedGitOutput(checkout, "show", recipe.Source.Revision+":"+filepath.ToSlash(clean))
		if err != nil {
			return fmt.Errorf("read signed recipe object %s: %w", name, err)
		}
		sum := sha256.Sum256(payload)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(expected)) {
			return fmt.Errorf("reviewed recipe file changed: %s", name)
		}
	}
	return nil
}

func reviewedGitOutput(checkout string, args ...string) ([]byte, error) {
	// cloudless-docker is a root broker while validation checkouts are owned by
	// the restricted orchestrator account. Trust only the exact checkout path
	// already derived from the authenticated operation; do not weaken Git's
	// ownership protection globally.
	commandArgs := append([]string{"-c", "core.pager=cat", "-c", "safe.directory=" + checkout, "-C", checkout}, args...)
	command := exec.Command("/usr/bin/git", commandArgs...)
	command.Env = []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/nonexistent",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	}
	var output, stderr boundedRecipeOutput
	command.Stdout, command.Stderr = &output, &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, stderr.String())
	}
	return append([]byte(nil), output.buffer.Bytes()...), nil
}

func authorizeReviewedDockerArgs(operation recipeops.Operation, checkout, stateDir string, args []string) error {
	if len(args) == 0 || len(args) > 256 {
		return errors.New("reviewed recipe Docker command is empty or too large")
	}
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') || len(arg) > 8192 {
			return errors.New("reviewed recipe Docker argument is invalid")
		}
		lower := strings.ToLower(strings.TrimSpace(arg))
		switch {
		case lower == "--privileged",
			strings.HasPrefix(lower, "--privileged="),
			lower == "--pid=host",
			lower == "--userns=host",
			lower == "--uts=host",
			lower == "--cgroupns=host",
			lower == "--cap-add",
			strings.HasPrefix(lower, "--cap-add="),
			lower == "--security-opt",
			strings.HasPrefix(lower, "--security-opt="),
			lower == "--mount",
			strings.HasPrefix(lower, "--mount="),
			strings.Contains(lower, "/var/run/docker.sock"),
			strings.Contains(lower, "/run/docker.sock"):
			return fmt.Errorf("reviewed recipe Docker argument is forbidden: %s", arg)
		}
	}

	switch args[0] {
	case "compose":
		return authorizeReviewedCompose(operation, checkout, args[1:])
	case "build":
		return authorizeReviewedBuild(operation, checkout, args[1:])
	case "run", "create":
		return authorizeReviewedRun(operation, stateDir, args)
	case "volume":
		return authorizeReviewedVolume(args[1:])
	case "rm":
		return authorizeReviewedContainerRemoval(args[1:])
	case "container":
		return authorizeReviewedContainerQuery(args[1:])
	case "image":
		return authorizeReviewedImageQuery(operation, args[1:])
	default:
		return fmt.Errorf("reviewed recipe Docker command %q is not admitted", args[0])
	}
}

func authorizeReviewedCompose(operation recipeops.Operation, checkout string, args []string) error {
	allowedAction := false
	seenAction := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--file", "--env-file":
			if seenAction {
				return errors.New("reviewed compose global option appears after its action")
			}
			if i+1 >= len(args) {
				return errors.New("reviewed compose path is missing")
			}
			i++
			path := args[i]
			if !filepath.IsAbs(path) {
				path = filepath.Join(checkout, path)
			}
			if err := requireContainedPath(path, checkout); err != nil {
				return errors.New("reviewed compose path escapes its checkout")
			}
			if args[i-1] != "--env-file" {
				relative, _ := filepath.Rel(checkout, filepath.Clean(path))
				if _, ok := operation.RecipeSnapshot.Source.Files[filepath.ToSlash(relative)]; !ok {
					return errors.New("compose file is not in the signed recipe inventory")
				}
			}
		case "config", "up", "down", "ps", "logs":
			if seenAction {
				return errors.New("reviewed compose contains multiple actions")
			}
			seenAction = true
			allowedAction = true
		case "-d", "--detach", "--quiet":
			if !seenAction {
				return errors.New("reviewed compose action flag appears before its action")
			}
		default:
			if strings.HasPrefix(args[i], "--tail=") && seenAction {
				continue
			}
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("reviewed compose option %q is not admitted", args[i])
			}
			if !seenAction {
				return fmt.Errorf("reviewed compose token %q is not admitted", args[i])
			}
		}
	}
	if !allowedAction {
		return errors.New("reviewed compose action is not admitted")
	}
	return nil
}

func authorizeReviewedVolume(args []string) error {
	if len(args) != 2 || args[0] != "create" || !strings.HasPrefix(args[1], "cloudless-") {
		return errors.New("reviewed recipe may only create a Cloudless-owned named volume")
	}
	return nil
}

func authorizeReviewedContainerRemoval(args []string) error {
	if len(args) != 2 || args[0] != "-f" || !strings.HasPrefix(args[1], "cloudless-") {
		return errors.New("reviewed recipe may only remove its Cloudless-owned container")
	}
	return nil
}

func authorizeReviewedContainerQuery(args []string) error {
	if len(args) != 2 || args[0] != "inspect" || !strings.HasPrefix(args[1], "cloudless-") {
		return errors.New("reviewed recipe may only inspect its Cloudless-owned container")
	}
	return nil
}

func authorizeReviewedImageQuery(operation recipeops.Operation, args []string) error {
	if len(args) != 4 || args[0] != "inspect" || args[2] != "--format" ||
		args[3] != "{{.Id}} {{.Size}}" {
		return errors.New("reviewed recipe image query is not admitted")
	}
	allowed := map[string]struct{}{operation.RecipeSnapshot.Engine.Image: {}}
	if operation.PreparedImageReference != "" {
		allowed[operation.PreparedImageReference] = struct{}{}
	}
	if _, ok := allowed[args[1]]; !ok {
		return errors.New("reviewed recipe image query differs from the authenticated operation")
	}
	return nil
}

func authorizeReviewedBuild(operation recipeops.Operation, checkout string, args []string) error {
	var dockerfile, contextDir string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f", "--file":
			if i+1 >= len(args) {
				return errors.New("reviewed Dockerfile path is missing")
			}
			i++
			dockerfile = args[i]
		case "-t", "--tag":
			if i+1 >= len(args) {
				return errors.New("reviewed build tag is missing")
			}
			i++
			if args[i] != operation.RecipeSnapshot.Engine.Image {
				return errors.New("reviewed build tag differs from the signed recipe image")
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("reviewed build option %q is not admitted", args[i])
			}
			if contextDir != "" {
				return errors.New("reviewed build contains multiple contexts")
			}
			contextDir = args[i]
		}
	}
	if dockerfile == "" || contextDir == "" {
		return errors.New("reviewed build requires a Dockerfile and context")
	}
	if !filepath.IsAbs(dockerfile) {
		dockerfile = filepath.Join(checkout, dockerfile)
	}
	if !filepath.IsAbs(contextDir) {
		contextDir = filepath.Join(checkout, contextDir)
	}
	if err := requireContainedPath(dockerfile, checkout); err != nil {
		return errors.New("reviewed Dockerfile escapes its checkout")
	}
	if err := requireContainedPath(contextDir, checkout); err != nil {
		return errors.New("reviewed build context escapes its checkout")
	}
	relative, _ := filepath.Rel(checkout, filepath.Clean(dockerfile))
	if _, ok := operation.RecipeSnapshot.Source.Files[filepath.ToSlash(relative)]; !ok {
		return errors.New("Dockerfile is not in the signed recipe inventory")
	}
	return nil
}

func authorizeReviewedRun(operation recipeops.Operation, stateDir string, args []string) error {
	image := ""
	name := ""
	network := ""
	contractRuntimeOptions := false
	infinibandDevice := false
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-v", "--volume":
			if i+1 >= len(args) {
				return errors.New("reviewed volume is missing")
			}
			i++
			mount := args[i]
			source := strings.SplitN(mount, ":", 2)[0]
			if !strings.HasPrefix(source, "cloudless-") && !admittedReviewedModelCache(source) && !admittedReviewedSecretMount(mount, stateDir) {
				return errors.New("reviewed recipe may mount only Cloudless-owned model storage")
			}
		case "-e", "--env", "--entrypoint", "--label":
			if i+1 >= len(args) {
				return fmt.Errorf("reviewed run option %s is missing a value", arg)
			}
			i++
		case "--name":
			if i+1 >= len(args) {
				return errors.New("reviewed run name is missing")
			}
			i++
			name = args[i]
			if !strings.HasPrefix(name, "cloudless-") {
				return errors.New("reviewed recipe container name is not Cloudless-owned")
			}
		case "--network":
			if i+1 >= len(args) {
				return errors.New("reviewed run network is missing")
			}
			i++
			network = args[i]
			if network != "host" {
				return errors.New("reviewed recipe run network is not admitted")
			}
		case "--ipc", "--gpus", "--ulimit":
			if i+1 >= len(args) {
				return fmt.Errorf("reviewed run option %s is missing a value", arg)
			}
			i++
			value := args[i]
			if (arg == "--ipc" && value != "host") ||
				(arg == "--gpus" && value != "all") ||
				(arg == "--ulimit" && value != "memlock=-1") {
				return fmt.Errorf("reviewed run option %s=%s is not admitted", arg, value)
			}
			contractRuntimeOptions = true
		case "--device":
			if i+1 >= len(args) {
				return errors.New("reviewed run device is missing")
			}
			i++
			if args[i] != "/dev/infiniband:/dev/infiniband" {
				return errors.New("reviewed recipe run device is not admitted")
			}
			infinibandDevice = true
			contractRuntimeOptions = true
		default:
			if strings.HasPrefix(arg, "-") {
				switch {
				case arg == "--rm", arg == "-i", arg == "--interactive", arg == "-d", arg == "--detach":
					continue
				case strings.HasPrefix(arg, "--entrypoint="),
					strings.HasPrefix(arg, "--label="),
					strings.HasPrefix(arg, "--env="):
					continue
				default:
					return fmt.Errorf("reviewed run option %q is not admitted", arg)
				}
			}
			image = arg
			i = len(args)
		}
	}
	if image == "" {
		return errors.New("reviewed recipe run image is missing")
	}
	allowed := map[string]struct{}{operation.RecipeSnapshot.Engine.Image: {}}
	if operation.PreparedImageReference != "" {
		allowed[operation.PreparedImageReference] = struct{}{}
	}
	if operation.PreparedImageDigest != "" {
		allowed[operation.PreparedImageDigest] = struct{}{}
	}
	if _, ok := allowed[image]; !ok {
		return errors.New("reviewed recipe run image differs from the authenticated operation")
	}
	boundedProbe := strings.HasPrefix(name, "cloudless-contract-probe-") || strings.HasPrefix(name, "cloudless-nccl-probe-")
	if (network == "host" || contractRuntimeOptions) && !boundedProbe {
		return errors.New("host networking is reserved for a bounded Cloudless runtime probe")
	}
	if infinibandDevice && !strings.HasPrefix(name, "cloudless-nccl-probe-") {
		return errors.New("InfiniBand device access is reserved for the bounded Cloudless NCCL probe")
	}
	return nil
}

func admittedReviewedSecretMount(mount, stateDir string) bool {
	parts := strings.Split(mount, ":")
	if len(parts) != 3 || parts[0] != filepath.Join(filepath.Clean(stateDir), "huggingface-token") ||
		parts[1] != HuggingFaceTokenContainerPath || parts[2] != "ro" {
		return false
	}
	info, err := os.Lstat(parts[0])
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0o077 == 0
}

func requireContainedPath(path, root string) error {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return errors.New("path must be absolute")
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("path is outside the admitted root")
	}
	return nil
}

func requireResolvedContainedPath(path, root string) error {
	if err := requireContainedPath(path, root); err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return fmt.Errorf("resolve admitted root: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve admitted path: %w", err)
	}
	return requireContainedPath(resolvedPath, resolvedRoot)
}

// ReviewedRecipeDockerRunner executes an already authenticated compatibility
// command. Policy authorization always happens immediately before exec so a
// stale client-side decision cannot cross the privilege boundary.
type ReviewedRecipeDockerRunner struct {
	Policy       ReviewedRecipePolicy
	DockerBinary string
}

func (r ReviewedRecipeDockerRunner) Execute(ctx context.Context, spec ReviewedRecipeDockerSpec) (string, error) {
	operation, err := r.Policy.authenticate(spec)
	if err != nil {
		return "", err
	}
	binary := strings.TrimSpace(r.DockerBinary)
	if binary == "" {
		binary = "/usr/bin/docker"
	}
	if !filepath.IsAbs(binary) {
		return "", errors.New("Docker compatibility executable must be absolute")
	}
	if len(spec.Args) > 0 && spec.Args[0] == "compose" {
		if err := validateReviewedCompose(ctx, binary, spec, operation); err != nil {
			return "", err
		}
	}
	command := reviewedDockerCommand(ctx, binary, spec, spec.Args)
	var output boundedRecipeOutput
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return output.String(), fmt.Errorf("reviewed recipe Docker command failed: %w: %s", err, output.String())
	}
	return output.String(), nil
}

func reviewedDockerCommand(
	ctx context.Context,
	binary string,
	spec ReviewedRecipeDockerSpec,
	args []string,
) *exec.Cmd {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = filepath.Clean(spec.WorkingDir)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/var/lib/cloudless"}
	for _, key := range []string{"COMPOSE_PROJECT_NAME", "COMPOSE_DISABLE_ENV_FILE", "NODE_RANK", "HEADLESS"} {
		if value, ok := spec.Environment[key]; ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	return command
}

func validateReviewedCompose(
	ctx context.Context,
	binary string,
	spec ReviewedRecipeDockerSpec,
	operation recipeops.Operation,
) error {
	configArgs := []string{"compose"}
	for i := 1; i < len(spec.Args); i++ {
		switch spec.Args[i] {
		case "-f", "--file", "--env-file":
			if i+1 >= len(spec.Args) {
				return errors.New("reviewed compose path is missing")
			}
			configArgs = append(configArgs, spec.Args[i], spec.Args[i+1])
			i++
		case "config", "up", "down", "ps", "logs":
			i = len(spec.Args)
		default:
			if !strings.HasPrefix(spec.Args[i], "-") {
				return fmt.Errorf("reviewed compose global token %q is not admitted", spec.Args[i])
			}
		}
	}
	configArgs = append(configArgs, "config", "--format", "json")
	command := reviewedDockerCommand(ctx, binary, spec, configArgs)
	var output, stderr boundedRecipeOutput
	command.Stdout, command.Stderr = &output, &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("render reviewed compose plan: %w: %s", err, stderr.String())
	}
	return validateReviewedComposePlan(output.buffer.Bytes(), operation)
}

func validateReviewedComposePlan(payload []byte, operation recipeops.Operation) error {
	var document struct {
		Services map[string]map[string]any `json:"services"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil || len(document.Services) == 0 {
		return errors.New("reviewed compose plan is invalid")
	}
	allowedImages := map[string]struct{}{operation.RecipeSnapshot.Engine.Image: {}}
	if operation.PreparedImageReference != "" {
		allowedImages[operation.PreparedImageReference] = struct{}{}
	}
	if operation.PreparedImageDigest != "" {
		allowedImages[operation.PreparedImageDigest] = struct{}{}
	}
	for serviceName, service := range document.Services {
		image, _ := service["image"].(string)
		if _, ok := allowedImages[image]; !ok {
			return fmt.Errorf("reviewed compose service %s uses an unauthenticated image", serviceName)
		}
		for _, key := range []string{"privileged", "build", "secrets", "configs"} {
			if value, exists := service[key]; exists && value != nil && value != false {
				return fmt.Errorf("reviewed compose service %s uses forbidden %s access", serviceName, key)
			}
		}
		for _, key := range []string{"pid", "userns_mode", "uts"} {
			if value, _ := service[key].(string); strings.EqualFold(value, "host") {
				return fmt.Errorf("reviewed compose service %s uses forbidden host %s", serviceName, key)
			}
		}
		if values, ok := service["cap_add"].([]any); ok && len(values) > 0 {
			return fmt.Errorf("reviewed compose service %s adds Linux capabilities", serviceName)
		}
		if err := validateReviewedComposeVolumes(serviceName, service["volumes"]); err != nil {
			return err
		}
		if err := validateReviewedComposeDevices(serviceName, service["devices"]); err != nil {
			return err
		}
	}
	return nil
}

func validateReviewedComposeVolumes(serviceName string, raw any) error {
	if raw == nil {
		return nil
	}
	volumes, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("reviewed compose service %s has invalid volumes", serviceName)
	}
	for _, rawVolume := range volumes {
		volume, ok := rawVolume.(map[string]any)
		if !ok {
			return fmt.Errorf("reviewed compose service %s has an invalid volume", serviceName)
		}
		kind, _ := volume["type"].(string)
		source, _ := volume["source"].(string)
		if kind == "volume" && strings.HasPrefix(source, "cloudless-") {
			continue
		}
		if kind != "bind" || !admittedReviewedModelCache(source) {
			return fmt.Errorf("reviewed compose service %s uses a non-Cloudless volume", serviceName)
		}
	}
	return nil
}

func admittedReviewedModelCache(source string) bool {
	source = filepath.Clean(strings.TrimSpace(source))
	root := filepath.Clean(modelcache.Root())
	if !filepath.IsAbs(source) || !pathWithin(source, root) {
		return false
	}
	if err := requireResolvedContainedPath(source, root); err != nil {
		return false
	}
	return true
}

func validateReviewedComposeDevices(serviceName string, raw any) error {
	if raw == nil {
		return nil
	}
	devices, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("reviewed compose service %s has invalid devices", serviceName)
	}
	for _, rawDevice := range devices {
		device, ok := rawDevice.(map[string]any)
		if !ok {
			return fmt.Errorf("reviewed compose service %s has an invalid device", serviceName)
		}
		source, _ := device["source"].(string)
		target, _ := device["target"].(string)
		if source != "/dev/infiniband" || target != "/dev/infiniband" {
			return fmt.Errorf("reviewed compose service %s uses a forbidden device", serviceName)
		}
	}
	return nil
}

type boundedRecipeOutput struct {
	buffer bytes.Buffer
}

func (w *boundedRecipeOutput) Write(payload []byte) (int, error) {
	const limit = 16 << 20
	if w.buffer.Len()+len(payload) > limit {
		remaining := limit - w.buffer.Len()
		if remaining > 0 {
			_, _ = w.buffer.Write(payload[:remaining])
		}
		return 0, errors.New("reviewed recipe Docker output exceeded 16 MiB")
	}
	return w.buffer.Write(payload)
}

func (w *boundedRecipeOutput) String() string {
	return strings.TrimSpace(w.buffer.String())
}
