package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cloudless/orchestrator/internal/jobs"
	"github.com/cloudless/orchestrator/internal/localrecipes"
	"github.com/cloudless/orchestrator/internal/sparkcluster"
)

const sparkRunInstalledPath = "/opt/cloudless/sparkrun/bin/sparkrun"

const sparkRunManagedCluster = "cloudless-managed"

const sparkRunLocalIdentity = "/var/lib/cloudless/sparkrun/id_ed25519"

var sparkRunUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (s *Server) sparkRunBinary() (string, error) {
	if strings.TrimSpace(s.sparkRunPath) != "" {
		return s.sparkRunPath, nil
	}
	if configured := strings.TrimSpace(os.Getenv("CLOUDLESS_SPARKRUN_PATH")); configured != "" {
		return configured, nil
	}
	if info, err := os.Stat(sparkRunInstalledPath); err == nil && !info.IsDir() {
		return sparkRunInstalledPath, nil
	}
	if found, err := exec.LookPath("sparkrun"); err == nil {
		return found, nil
	}
	return "", errors.New("the pinned SparkRun provider is not installed")
}

func commandOutput(ctx context.Context, environment map[string]string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = commandEnv(environment)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	text := strings.TrimSpace(output.String())
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, errors.New(text)
	}
	return text, nil
}

func (s *Server) verifySparkRunProvider(ctx context.Context) (string, error) {
	binary, err := s.sparkRunBinary()
	if err != nil {
		return "", err
	}
	output, err := commandOutput(ctx, nil, binary, "--version")
	if err != nil {
		return "", fmt.Errorf("check SparkRun provider: %w", err)
	}
	if !strings.Contains(output, localrecipes.SparkRunProviderVersion) {
		return "", fmt.Errorf("SparkRun provider %s is required; found %s", localrecipes.SparkRunProviderVersion, output)
	}
	return binary, nil
}

func writeSparkRunDocument(dir string, data []byte) (string, error) {
	if len(data) == 0 || len(data) > 2<<20 {
		return "", errors.New("SparkRun recipe must be between 1 byte and 2 MB")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Server) validateSparkRunProvider(ctx context.Context, data []byte) (string, error) {
	binary, err := s.verifySparkRunProvider(ctx)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "cloudless-sparkrun-validate-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	path, err := writeSparkRunDocument(dir, data)
	if err != nil {
		return "", err
	}
	output, err := commandOutput(ctx, nil, binary, "recipe", "validate", path)
	if err != nil {
		return output, fmt.Errorf("SparkRun rejected this recipe: %w", err)
	}
	return output, nil
}

func primarySparkRunUser() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("CLOUDLESS_SPARKRUN_USER")); configured != "" {
		if !sparkRunUserPattern.MatchString(configured) {
			return "", errors.New("CLOUDLESS_SPARKRUN_USER is invalid")
		}
		return configured, nil
	}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return "", err
	}
	bestName, bestUID := "", int(^uint(0)>>1)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[0] == "cloudless" || !sparkRunUserPattern.MatchString(fields[0]) {
			continue
		}
		uid, parseErr := strconv.Atoi(fields[2])
		if parseErr == nil && uid >= 1000 && uid < 65534 && uid < bestUID {
			bestName, bestUID = fields[0], uid
		}
	}
	if bestName == "" {
		return "", errors.New("Cloudless could not identify the DGX Spark owner account")
	}
	return bestName, nil
}

func sparkRunHosts(recipe localrecipes.Recipe, cluster sparkcluster.State) ([]string, string, error) {
	nodes := recipe.Distributed.Nodes
	if nodes > 1 && (!cluster.Configured || !cluster.Healthy || len(cluster.Nodes) < nodes-1) {
		return nil, "", fmt.Errorf("this recipe requires %d healthy DGX Sparks", nodes)
	}
	username, err := primarySparkRunUser()
	if nodes > 1 {
		username = strings.TrimSpace(cluster.Nodes[0].Username)
	}
	if err != nil && username == "" {
		return nil, "", err
	}
	if username == "" {
		return nil, "", errors.New("the Spark cluster has no SSH user")
	}
	hosts := []string{"127.0.0.1"}
	if nodes <= 1 {
		return hosts, username, nil
	}
	for _, node := range cluster.Nodes[:nodes-1] {
		if strings.TrimSpace(node.Username) != username {
			return nil, "", errors.New("SparkRun requires the selected cluster nodes to use the same SSH user")
		}
		if !node.Healthy {
			return nil, "", fmt.Errorf("Spark %s is not healthy", node.Name)
		}
		hosts = append(hosts, node.Host)
	}
	return hosts, username, nil
}

func sparkRunEnvironment(checkout string) (map[string]string, error) {
	home := filepath.Join("/var/lib/cloudless", "sparkrun")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	return map[string]string{
		"HOME":                      home,
		"PATH":                      filepath.Dir(sparkRunInstalledPath) + ":" + os.Getenv("PATH"),
		"HF_HOME":                   filepath.Join(home, "huggingface"),
		"CLOUDLESS_SPARKRUN_RECIPE": filepath.Join(checkout, "recipe.yaml"),
	}, nil
}

func ensureSparkRunSSH(username string) error {
	account, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("look up Spark owner: %w", err)
	}
	identity, clusterKnownHosts := sparkcluster.SSHIdentityPaths()
	publicKey, err := os.ReadFile(sparkRunLocalIdentity + ".pub")
	if err != nil {
		return errors.New("the local SparkRun SSH identity is not installed; restart cloudless-sparkrun.service")
	}
	key := strings.TrimSpace(string(publicKey))
	authorized, readErr := os.ReadFile(filepath.Join(account.HomeDir, ".ssh", "authorized_keys"))
	if readErr != nil || !strings.Contains("\n"+string(authorized)+"\n", "\n"+key+"\n") {
		return errors.New("the local SparkRun SSH identity is not authorized; restart cloudless-sparkrun.service")
	}
	home := filepath.Join("/var/lib/cloudless", "sparkrun")
	providerSSH := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(providerSSH, 0o700); err != nil {
		return err
	}
	localKnown := filepath.Join(providerSSH, "known_hosts")
	config := "Host 127.0.0.1 localhost\n  IdentityFile " + sparkRunLocalIdentity + "\n  UserKnownHostsFile " + localKnown + "\n  StrictHostKeyChecking accept-new\n  IdentitiesOnly yes\n\nHost *\n  IdentityFile " + identity + "\n  UserKnownHostsFile " + clusterKnownHosts + "\n  StrictHostKeyChecking yes\n  IdentitiesOnly yes\n  BatchMode yes\n  ConnectTimeout 10\n"
	return os.WriteFile(filepath.Join(providerSSH, "config"), []byte(config), 0o600)
}

func configureSparkRunCluster(ctx context.Context, binary string, recipe localrecipes.Recipe, cluster sparkcluster.State, environment map[string]string) ([]string, error) {
	hosts, username, err := sparkRunHosts(recipe, cluster)
	if err != nil {
		return nil, err
	}
	if err := ensureSparkRunSSH(username); err != nil {
		return nil, err
	}
	args := []string{"cluster", "update", sparkRunManagedCluster, "--hosts", strings.Join(hosts, ","), "--user", username}
	if _, err := commandOutput(ctx, environment, binary, args...); err != nil {
		args = []string{"cluster", "create", sparkRunManagedCluster, "--hosts", strings.Join(hosts, ","), "--user", username, "--cache-dir", environment["HF_HOME"]}
		if _, createErr := commandOutput(ctx, environment, binary, args...); createErr != nil {
			return nil, fmt.Errorf("configure SparkRun cluster: %w", createErr)
		}
	}
	return []string{"--cluster", sparkRunManagedCluster}, nil
}

func prepareSparkRunProviderRecipe(recipe localrecipes.Recipe, checkout string) (string, map[string]string, error) {
	if recipe.Runtime.SparkRun == nil || recipe.Runtime.SparkRun.Document == "" {
		return "", nil, errors.New("the original SparkRun recipe is unavailable")
	}
	path, err := writeSparkRunDocument(checkout, []byte(recipe.Runtime.SparkRun.Document))
	if err != nil {
		return "", nil, err
	}
	environment, err := sparkRunEnvironment(checkout)
	return path, environment, err
}

func (s *Server) dryRunSparkRunProvider(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe, cluster sparkcluster.State, path, checkout string, environment map[string]string, progressDone, progressTotal int) error {
	binary, err := s.verifySparkRunProvider(ctx)
	if err != nil {
		return err
	}
	target, err := configureSparkRunCluster(ctx, binary, recipe, cluster, environment)
	if err != nil {
		return err
	}
	args := append([]string{"run", path}, target...)
	args = append(args, "--dry-run", "--no-follow")
	job.Progress("validating", "SparkRun is validating the exact recipe against this cluster...", progressDone, progressTotal)
	return runRecipeCommand(ctx, job, "validating", "SparkRun dry run", checkout, environment, binary, args...)
}

func (s *Server) startSparkRunProvider(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe, cluster sparkcluster.State, path, checkout string, environment map[string]string) error {
	binary, err := s.verifySparkRunProvider(ctx)
	if err != nil {
		return err
	}
	target, err := configureSparkRunCluster(ctx, binary, recipe, cluster, environment)
	if err != nil {
		return err
	}
	diagnostics := filepath.Join(checkout, "diagnostics.ndjson")
	args := append([]string{"run", path}, target...)
	args = append(args, "--no-follow", "--ensure", "--collect-diagnostics", diagnostics)
	return runRecipeCommand(ctx, job, "starting", "SparkRun", checkout, environment, binary, args...)
}

func (s *Server) stopSparkRunProvider(ctx context.Context, job *jobs.Job, recipe localrecipes.Recipe, cluster sparkcluster.State, path, checkout string, environment map[string]string) error {
	binary, err := s.verifySparkRunProvider(ctx)
	if err != nil {
		return err
	}
	target, err := configureSparkRunCluster(ctx, binary, recipe, cluster, environment)
	if err != nil && recipe.Distributed.Nodes > 1 {
		// A disconnected peer must not prevent cleanup of whatever remains.
		target = nil
	}
	args := append([]string{"stop", path}, target...)
	return runRecipeCommand(ctx, job, "stopping", "SparkRun", checkout, environment, binary, args...)
}
