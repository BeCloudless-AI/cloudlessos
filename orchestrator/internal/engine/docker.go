package engine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cloudless/orchestrator/internal/platform"
)

// Docker implements Engine by shelling out to the docker CLI.
type Docker struct {
	bin string
}

// NewDocker returns a Docker engine that invokes the "docker" binary on PATH.
func NewDocker() *Docker {
	return &Docker{bin: "docker"}
}

func (d *Docker) exec(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, d.bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

func (d *Docker) Available(ctx context.Context) error {
	_, errs, err := d.exec(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("docker not available: %v: %s", err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) EnsureNetwork(ctx context.Context, name string) error {
	if err := validateManagedName("network", name); err != nil {
		return err
	}
	if _, _, err := d.exec(ctx, "network", "inspect", name); err == nil {
		return nil
	}
	if _, errs, err := d.exec(ctx, "network", "create", name); err != nil {
		return fmt.Errorf("create network %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) EnsureVolume(ctx context.Context, name string) error {
	if !managedVolumeName.MatchString(strings.TrimSpace(name)) {
		return fmt.Errorf("volume %q is outside the Cloudless namespace", name)
	}
	if _, _, err := d.exec(ctx, "volume", "inspect", name); err == nil {
		return nil
	}
	_, errs, err := d.exec(ctx, "volume", "create", name)
	if err != nil {
		return fmt.Errorf("create volume %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) VolumeMountpoint(ctx context.Context, name string) (string, error) {
	if !managedVolumeName.MatchString(strings.TrimSpace(name)) {
		return "", fmt.Errorf("volume %q is outside the Cloudless namespace", name)
	}
	out, errs, err := d.exec(ctx, "volume", "inspect", name, "--format", "{{.Mountpoint}}")
	if err != nil {
		return "", fmt.Errorf("inspect volume %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	mountpoint := strings.TrimSpace(out)
	if mountpoint == "" {
		return "", fmt.Errorf("volume %s has no mountpoint", name)
	}
	return mountpoint, nil
}

func (d *Docker) ListVolumes(ctx context.Context) ([]string, error) {
	out, errs, err := d.exec(ctx, "volume", "ls", "--filter", "name=^cloudless-", "--format", "{{.Name}}")
	if err != nil {
		return nil, fmt.Errorf("list volumes: %v: %s", err, strings.TrimSpace(errs))
	}
	var volumes []string
	for _, name := range strings.Fields(out) {
		if managedVolumeName.MatchString(name) {
			volumes = append(volumes, name)
		}
	}
	return volumes, nil
}

func (d *Docker) RemoveVolume(ctx context.Context, name string) error {
	if !managedVolumeName.MatchString(strings.TrimSpace(name)) {
		return fmt.Errorf("volume %q is outside the Cloudless namespace", name)
	}
	_, errs, err := d.exec(ctx, "volume", "rm", name)
	if err != nil {
		return fmt.Errorf("remove volume %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) ConnectNetwork(ctx context.Context, network, container string) error {
	if err := validateManagedName("network", network); err != nil {
		return err
	}
	if err := validateManagedName("container", container); err != nil {
		return err
	}
	_, errs, err := d.exec(ctx, "network", "connect", network, container)
	if err != nil {
		if strings.Contains(errs, "already exists") || strings.Contains(errs, "already connected") {
			return nil
		}
		return fmt.Errorf("connect %s to %s: %v: %s", container, network, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) ContainerEnvironment(ctx context.Context, container string) (map[string]string, error) {
	if err := validateManagedName("container", container); err != nil {
		return nil, err
	}
	out, errs, err := d.exec(ctx, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", container)
	if err != nil {
		return nil, fmt.Errorf("inspect environment %s: %v: %s", container, err, strings.TrimSpace(errs))
	}
	result := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if key, value, ok := strings.Cut(strings.TrimSpace(line), "="); ok && environmentName.MatchString(key) {
			result[key] = value
		}
	}
	return result, nil
}

func (d *Docker) HermesConfigValue(ctx context.Context, container, key string) (string, error) {
	if err := validateManagedName("container", container); err != nil {
		return "", err
	}
	if key != "model.context_length" && key != "model.max_tokens" {
		return "", errors.New("Hermes configuration key is not admitted")
	}
	out, errs, err := d.exec(ctx, "exec", container, "hermes", "config", "get", key)
	if err != nil {
		return "", fmt.Errorf("read Hermes configuration %s: %v: %s", key, err, strings.TrimSpace(errs))
	}
	return strings.TrimSpace(out), nil
}

func (d *Docker) HasNVIDIARuntime(ctx context.Context) (bool, error) {
	out, errs, err := d.exec(ctx, "info", "--format", "{{json .Runtimes}}")
	if err != nil {
		return false, fmt.Errorf("inspect Docker runtimes: %v: %s", err, strings.TrimSpace(errs))
	}
	return strings.Contains(strings.ToLower(out), `"nvidia"`), nil
}

func (d *Docker) ContainerNamesByLabel(ctx context.Context, key, value string) ([]string, error) {
	if key != "cloudless.recipe.operation" && key != "com.docker.compose.project" {
		return nil, errors.New("container label is not admitted")
	}
	if !labelValue.MatchString(value) {
		return nil, errors.New("container label value is invalid")
	}
	return d.containerNames(ctx, "label="+key+"="+value)
}

func (d *Docker) ContainerNamesByAncestor(ctx context.Context, image string) ([]string, error) {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return nil, errors.New("container image reference is invalid")
	}
	return d.containerNames(ctx, "ancestor="+image)
}

func (d *Docker) containerNames(ctx context.Context, filter string) ([]string, error) {
	out, errs, err := d.exec(ctx, "ps", "-a", "--filter", filter, "--format", "{{.Names}}")
	if err != nil {
		return nil, fmt.Errorf("discover managed containers: %v: %s", err, strings.TrimSpace(errs))
	}
	var result []string
	for _, name := range strings.Fields(out) {
		if err := validateManagedName("container", name); err != nil {
			return nil, fmt.Errorf("Docker returned an unmanaged container for %q", filter)
		}
		result = append(result, name)
	}
	return result, nil
}

func (d *Docker) LogsTail(ctx context.Context, container string, lines int) (string, error) {
	if err := validateManagedName("container", container); err != nil {
		return "", err
	}
	if lines < 1 || lines > 1000 {
		return "", errors.New("log tail is outside the allowed range")
	}
	out, errs, err := d.exec(ctx, "logs", "--tail", fmt.Sprintf("%d", lines), container)
	if err != nil {
		return "", fmt.Errorf("logs %s: %v: %s", container, err, strings.TrimSpace(errs))
	}
	return out + errs, nil
}

func (d *Docker) HasAlias(ctx context.Context, container, alias string) (bool, error) {
	if err := validateManagedName("container", container); err != nil {
		return false, err
	}
	if err := validateManagedName("network alias", alias); err != nil {
		return false, err
	}
	out, errs, err := d.exec(ctx, "inspect", "--format",
		"{{range .NetworkSettings.Networks}}{{range .Aliases}}{{.}} {{end}}{{end}}", container)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %v: %s", container, err, strings.TrimSpace(errs))
	}
	for _, a := range strings.Fields(out) {
		if a == alias {
			return true, nil
		}
	}
	return false, nil
}

func (d *Docker) Exec(ctx context.Context, container string, args ...string) error {
	if err := validateManagedName("container", container); err != nil {
		return err
	}
	full := append([]string{"exec", container}, args...)
	if _, errs, err := d.exec(ctx, full...); err != nil {
		return fmt.Errorf("exec %s: %v: %s", container, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) Pull(ctx context.Context, image string) error {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return errors.New("image reference is invalid")
	}
	_, errs, err := d.exec(ctx, "pull", image)
	if err != nil {
		return fmt.Errorf("pull %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	return nil
}

// PullStream uses Docker's structured daemon stream so callers receive actual
// per-layer byte progress. The CLI is retained as a compatibility fallback for
// nonstandard Docker installations whose local API socket is unavailable.
func (d *Docker) PullStream(ctx context.Context, image string, onLine func(string)) error {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return errors.New("image reference is invalid")
	}
	if err := d.pullStreamAPI(ctx, image, onLine); err == nil {
		return nil
	} else {
		var unavailable *dockerAPIUnavailableError
		if !errors.As(err, &unavailable) {
			return err
		}
	}
	return d.pullStreamCLI(ctx, image, onLine)
}

type dockerAPIUnavailableError struct{ err error }

func (e *dockerAPIUnavailableError) Error() string { return "Docker API unavailable: " + e.err.Error() }
func (e *dockerAPIUnavailableError) Unwrap() error { return e.err }

type dockerPullMessage struct {
	Status      string `json:"status"`
	Progress    string `json:"progress"`
	ID          string `json:"id"`
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
	ProgressDetail struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
}

func dockerSocketPath() string {
	if host := strings.TrimSpace(os.Getenv("DOCKER_HOST")); strings.HasPrefix(host, "unix://") {
		if path := strings.TrimPrefix(host, "unix://"); filepath.IsAbs(path) {
			return path
		}
	}
	return "/var/run/docker.sock"
}

func (d *Docker) pullStreamAPI(ctx context.Context, image string, onLine func(string)) error {
	socket := dockerSocketPath()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	query := dockerPullQuery(image)
	endpoint := "http://docker/images/create?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("prepare Docker image pull: %w", err)
	}
	// An empty registry-auth object is required by some Docker daemon versions
	// and is sufficient for the public, immutable images admitted by Cloudless.
	request.Header.Set("X-Registry-Auth", "e30=")
	response, err := client.Do(request)
	if err != nil {
		return &dockerAPIUnavailableError{err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return fmt.Errorf("pull %s: Docker API HTTP %d: %s", image, response.StatusCode, strings.TrimSpace(string(body)))
	}

	decoder := json.NewDecoder(response.Body)
	for {
		var message dockerPullMessage
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("pull %s progress stream: %w", image, err)
		}
		if message.Error != "" || message.ErrorDetail.Message != "" {
			detail := message.Error
			if detail == "" {
				detail = message.ErrorDetail.Message
			}
			return fmt.Errorf("pull %s: %s", image, detail)
		}
		line := strings.TrimSpace(message.Status)
		progress := strings.TrimSpace(message.Progress)
		if progress == "" && message.ProgressDetail.Total > 0 {
			progress = fmt.Sprintf("%dB/%dB", message.ProgressDetail.Current, message.ProgressDetail.Total)
		}
		if progress != "" {
			line = strings.TrimSpace(line + " " + progress)
		}
		if message.ID != "" {
			line = message.ID + ": " + line
		}
		if line != "" && onLine != nil {
			onLine(line)
		}
	}
}

func dockerPullQuery(image string) url.Values {
	name, tag := image, ""
	if at := strings.LastIndex(image, "@"); at > 0 {
		name, tag = image[:at], image[at+1:]
	} else if colon, slash := strings.LastIndex(image, ":"), strings.LastIndex(image, "/"); colon > slash {
		name, tag = image[:colon], image[colon+1:]
	}
	values := url.Values{"fromImage": []string{name}}
	if tag != "" {
		values.Set("tag", tag)
	}
	return values
}

func (d *Docker) pullStreamCLI(ctx context.Context, image string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, d.bin, "pull", image)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close() // unblocks the scanner with EOF
		done <- err
	}()

	var emitMu sync.Mutex
	emit := func(line string) {
		if onLine == nil {
			return
		}
		emitMu.Lock()
		onLine(line)
		emitMu.Unlock()
	}
	stopHeartbeat := make(chan struct{})
	defer close(stopHeartbeat)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				emit("Cloudless: Docker is still working on the current image layer")
			case <-stopHeartbeat:
				return
			}
		}
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	recent := make([]string, 0, 12)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			if len(recent) == cap(recent) {
				copy(recent, recent[1:])
				recent = recent[:len(recent)-1]
			}
			recent = append(recent, line)
			emit(line)
		}
	}
	if scanErr := sc.Err(); scanErr != nil {
		return fmt.Errorf("pull %s output: %w", image, scanErr)
	}
	if err := <-done; err != nil {
		detail := strings.Join(recent, "; ")
		if detail != "" {
			return fmt.Errorf("pull %s: %w: %s", image, err, detail)
		}
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

// Build runs `docker build -t image contextDir`, streaming output to onLine.
func (d *Docker) Build(ctx context.Context, image, contextDir string, onLine func(string)) error {
	if err := validateBuild(image, contextDir); err != nil {
		return fmt.Errorf("build policy: %w", err)
	}
	cmd := exec.CommandContext(ctx, d.bin, "build", "-t", image, contextDir)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("build %s: %w", image, err)
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		done <- err
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && onLine != nil {
			onLine(line)
		}
	}
	if err := <-done; err != nil {
		return fmt.Errorf("build %s: %w", image, err)
	}
	return nil
}

func (d *Docker) RemoveImage(ctx context.Context, image string) error {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return errors.New("image reference is invalid")
	}
	if _, errs, err := d.exec(ctx, "rmi", "-f", image); err != nil {
		return fmt.Errorf("rmi %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) InspectImage(ctx context.Context, image string) (ImageInfo, error) {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return ImageInfo{}, errors.New("container image reference is invalid")
	}
	out, errs, err := d.exec(ctx, "image", "inspect", image)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("inspect image %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	var records []struct {
		ID           string   `json:"Id"`
		OS           string   `json:"Os"`
		Architecture string   `json:"Architecture"`
		Size         int64    `json:"Size"`
		RepoDigests  []string `json:"RepoDigests"`
		Config       struct {
			EntryPoint []string `json:"Entrypoint"`
			Command    []string `json:"Cmd"`
		} `json:"Config"`
	}
	if err := json.Unmarshal([]byte(out), &records); err != nil || len(records) != 1 {
		return ImageInfo{}, fmt.Errorf("inspect image %s returned invalid metadata", image)
	}
	return ImageInfo{
		ID: records[0].ID, OS: records[0].OS, Architecture: records[0].Architecture,
		Size: records[0].Size, EntryPoint: records[0].Config.EntryPoint,
		Command: records[0].Config.Command, RepoDigests: records[0].RepoDigests,
	}, nil
}

func (d *Docker) TagImage(ctx context.Context, source, target string) error {
	if !imageReference.MatchString(strings.TrimSpace(source)) ||
		!imageReference.MatchString(strings.TrimSpace(target)) {
		return errors.New("container image reference is invalid")
	}
	if _, errs, err := d.exec(ctx, "tag", source, target); err != nil {
		return fmt.Errorf("tag image %s as %s: %v: %s", source, target, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) RemoteImageManifest(ctx context.Context, image string) (string, error) {
	return d.remoteImageMetadata(ctx, image, "{{json .Manifest}}")
}

func (d *Docker) RemoteImageConfig(ctx context.Context, image string) (string, error) {
	return d.remoteImageMetadata(ctx, image, "{{json .Image}}")
}

func (d *Docker) remoteImageMetadata(ctx context.Context, image, format string) (string, error) {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return "", errors.New("container image reference is invalid")
	}
	out, errs, err := d.exec(ctx, "buildx", "imagetools", "inspect", image, "--format", format)
	if err != nil {
		return "", fmt.Errorf("inspect remote image %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	return strings.TrimSpace(out), nil
}

func (d *Docker) ExportImage(ctx context.Context, image, destination string) error {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return errors.New("container image reference is invalid")
	}
	destination = filepath.Clean(strings.TrimSpace(destination))
	if !admittedImageExportPath(destination) {
		return errors.New("image export destination is outside Cloudless transfer staging")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o2770); err != nil {
		return fmt.Errorf("create image export directory: %w", err)
	}
	// The deterministic sidecar name lets the unprivileged orchestrator report
	// archive byte progress while this privileged broker writes the export.
	temporaryName := destination + ".partial"
	_ = os.Remove(temporaryName)
	temporary, err := os.OpenFile(temporaryName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create image export: %w", err)
	}
	defer os.Remove(temporaryName)
	command := exec.CommandContext(ctx, d.bin, "image", "save", image)
	command.Stdout = temporary
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("export image %s: %w: %s", image, err, strings.TrimSpace(stderr.String()))
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	directoryInfo, err := os.Stat(filepath.Dir(destination))
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("inspect image export directory: %w", err)
	}
	directoryStat, ok := directoryInfo.Sys().(*syscall.Stat_t)
	if !ok {
		_ = temporary.Close()
		return errors.New("image export directory ownership is unavailable")
	}
	// cloudless-engine runs as root while cloudlessd deliberately does not.
	// Hand the archive to the transfer directory's trusted group so the
	// orchestrator can stream it without making the image world-readable.
	if err := temporary.Chown(-1, int(directoryStat.Gid)); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set image export ownership: %w", err)
	}
	if err := temporary.Chmod(0o640); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("publish image export: %w", err)
	}
	return nil
}

func admittedImageExportPath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Base(path) == "." {
		return false
	}
	for _, root := range []string{"/var/lib/cloudless/image-transfers"} {
		relative, err := filepath.Rel(filepath.Clean(root), path)
		if err == nil && relative != "." && relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) &&
			strings.HasPrefix(filepath.Base(path), "cloudless-image-") {
			return true
		}
	}
	return false
}

func (d *Docker) ListImageDigests(ctx context.Context) ([]ImageDigestRef, error) {
	out, errs, err := d.exec(ctx, "image", "ls", "--digests", "--format", "{{.Repository}}|{{.Tag}}|{{.Digest}}")
	if err != nil {
		return nil, fmt.Errorf("list image digests: %v: %s", err, strings.TrimSpace(errs))
	}
	var result []ImageDigestRef
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 3 || !imageReference.MatchString(parts[0]) {
			continue
		}
		result = append(result, ImageDigestRef{Repository: parts[0], Tag: parts[1], Digest: parts[2]})
	}
	return result, nil
}

// runArgs builds the full `docker run …` argument list for a spec. Map-derived
// flags (ports/env/volumes) are emitted in sorted order so the command is
// deterministic — important for the editable command preview in the UI.
func runArgs(spec RunSpec) []string {
	restartPolicy := strings.TrimSpace(spec.RestartPolicy)
	switch restartPolicy {
	case "no", "on-failure", "unless-stopped":
	case "":
		restartPolicy = "unless-stopped"
	default:
		// RunSpec values may originate in imported manifests. Never pass an
		// arbitrary restart-policy value through to the Docker CLI.
		restartPolicy = "unless-stopped"
	}
	args := []string{"run", "-d", "--name", spec.Name, "--restart", restartPolicy}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	if spec.ReadOnly {
		args = append(args, "--read-only")
	}
	if spec.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", spec.PidsLimit))
	}
	if strings.TrimSpace(spec.ShmSize) != "" {
		args = append(args, "--shm-size", strings.TrimSpace(spec.ShmSize))
	}
	for _, capability := range sortedNonEmpty(spec.CapDrop) {
		args = append(args, "--cap-drop", capability)
	}
	for _, capability := range sortedNonEmpty(spec.CapAdd) {
		args = append(args, "--cap-add", capability)
	}
	for _, option := range sortedNonEmpty(spec.SecurityOpts) {
		args = append(args, "--security-opt", option)
	}
	for _, mount := range sortedNonEmpty(spec.Tmpfs) {
		args = append(args, "--tmpfs", mount)
	}
	if spec.GPUs != "" {
		if platform.GPUContainerMode() == platform.GPUCDI {
			for _, gpu := range strings.Split(spec.GPUs, ",") {
				gpu = strings.TrimSpace(gpu)
				if gpu != "" {
					args = append(args, "--device", "nvidia.com/gpu="+gpu)
				}
			}
		} else {
			args = append(args, "--gpus", spec.GPUs)
		}
	}
	for _, device := range sortedNonEmpty(spec.Devices) {
		args = append(args, "--device", device)
	}
	if spec.Network != "" {
		args = append(args, "--network", spec.Network)
		if spec.Network != "host" && spec.NetworkAlias != "" {
			args = append(args, "--network-alias", spec.NetworkAlias)
		}
	}
	if spec.IPC != "" {
		args = append(args, "--ipc", spec.IPC)
	}
	if strings.TrimSpace(spec.Memory) != "" {
		args = append(args, "--memory", strings.TrimSpace(spec.Memory))
	}
	if strings.TrimSpace(spec.MemorySwap) != "" {
		args = append(args, "--memory-swap", strings.TrimSpace(spec.MemorySwap))
	}
	for _, limit := range spec.Ulimits {
		if strings.TrimSpace(limit) != "" {
			args = append(args, "--ulimit", limit)
		}
	}
	for _, host := range spec.ExtraHosts {
		if strings.TrimSpace(host) != "" {
			args = append(args, "--add-host", host)
		}
	}
	// Host networking binds host ports directly; -p is invalid there.
	if spec.Network != "host" {
		hosts := make([]int, 0, len(spec.Ports))
		for host := range spec.Ports {
			hosts = append(hosts, host)
		}
		sort.Ints(hosts)
		for _, host := range hosts {
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", host, spec.Ports[host]))
		}
	}
	for _, k := range sortedKeys(spec.Env) {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, spec.Env[k]))
	}
	for _, k := range sortedKeys(spec.Labels) {
		if strings.TrimSpace(k) != "" {
			args = append(args, "--label", fmt.Sprintf("%s=%s", k, spec.Labels[k]))
		}
	}
	for _, h := range sortedKeys(spec.Volumes) {
		args = append(args, "-v", fmt.Sprintf("%s:%s", h, spec.Volumes[h]))
	}
	for _, source := range sortedKeys(spec.SecretFiles) {
		args = append(args, "--mount", fmt.Sprintf("type=bind,src=%s,dst=%s,readonly", source, spec.SecretFiles[source]))
	}
	if strings.TrimSpace(spec.EntryPoint) != "" {
		args = append(args, "--entrypoint", spec.EntryPoint)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Args...)
	return args
}

// RunArguments returns the validated Docker argv for a managed container.
// Distributed recipe launchers use the same deterministic renderer when the
// exact spec must be sent through Cloudless's restricted peer SSH channel.
func RunArguments(spec RunSpec) ([]string, error) {
	if err := ValidateRunSpec(spec); err != nil {
		return nil, err
	}
	if err := validateGPURequest(spec.GPUs); err != nil {
		return nil, err
	}
	return runArgs(spec), nil
}

func transientArgs(spec RunSpec) []string {
	full := runArgs(spec)
	return append([]string{"run", "--rm", "--name", spec.Name}, full[6:]...)
}

func transientName() (string, error) {
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate transient container name: %w", err)
	}
	return "cloudless-transient-" + hex.EncodeToString(suffix[:]), nil
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sortedNonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

// PreviewParts renders the `docker run …` command for display, split into the
// orchestrator-managed prefix (flags + image) and the container command (spec.Args).
// The UI shows the prefix read-only and lets the user edit just the command.
func PreviewParts(spec RunSpec) (prefix, command string) {
	full := runArgs(spec)
	pre := full[:len(full)-len(spec.Args)]
	return "docker " + ShellJoin(pre), ShellJoin(spec.Args)
}

func (d *Docker) Run(ctx context.Context, spec RunSpec) (string, error) {
	if err := ValidateRunSpec(spec); err != nil {
		return "", fmt.Errorf("container policy: %w", err)
	}
	if err := validateGPURequest(spec.GPUs); err != nil {
		return "", fmt.Errorf("container policy: %w", err)
	}
	out, errs, err := d.exec(ctx, runArgs(spec)...)
	if err != nil {
		return "", fmt.Errorf("run %s: %v: %s", spec.Image, err, strings.TrimSpace(errs))
	}
	return strings.TrimSpace(out), nil
}

func (d *Docker) RunTransient(ctx context.Context, spec RunSpec) (string, error) {
	if spec.Name == "" {
		name, err := transientName()
		if err != nil {
			return "", err
		}
		spec.Name = name
	}
	if err := ValidateRunSpec(spec); err != nil {
		return "", fmt.Errorf("container policy: %w", err)
	}
	if err := validateGPURequest(spec.GPUs); err != nil {
		return "", fmt.Errorf("container policy: %w", err)
	}
	out, errs, err := d.exec(ctx, transientArgs(spec)...)
	if err != nil {
		return "", fmt.Errorf("run transient %s: %v: %s", spec.Image, err, strings.TrimSpace(errs))
	}
	return strings.TrimSpace(out), nil
}

// ShellJoin renders argv as a single space-separated line, quoting any token that
// contains whitespace or quotes so it round-trips through ShellSplit.
func ShellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n\"'") {
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

// ShellSplit tokenizes a command line into argv, honoring single/double quotes and
// backslash-escaped double quotes. Whitespace (incl. newlines) separates tokens.
func ShellSplit(s string) []string {
	var out []string
	var cur []rune
	inTok := false
	var quote rune
	esc := false
	flush := func() {
		if inTok {
			out = append(out, string(cur))
			cur = cur[:0]
			inTok = false
		}
	}
	for _, r := range s {
		if esc {
			cur = append(cur, r)
			esc = false
			inTok = true
			continue
		}
		if quote != 0 {
			switch {
			case r == '\\' && quote == '"':
				esc = true
			case r == quote:
				quote = 0
			default:
				cur = append(cur, r)
			}
			inTok = true
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			inTok = true
		case ' ', '\t', '\n', '\r':
			flush()
		default:
			cur = append(cur, r)
			inTok = true
		}
	}
	flush()
	return out
}

func (d *Docker) Stop(ctx context.Context, name string) error {
	if err := validateManagedName("container", name); err != nil {
		return err
	}
	_, errs, err := d.exec(ctx, "stop", name)
	if err != nil {
		return fmt.Errorf("stop %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) Remove(ctx context.Context, name string) error {
	if err := validateManagedName("container", name); err != nil {
		return err
	}
	_, errs, err := d.exec(ctx, "rm", "-f", name)
	if err != nil {
		return fmt.Errorf("remove %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return nil
}

// psLine matches `docker ps --format '{{json .}}'` output.
type psLine struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Ports  string `json:"Ports"`
}

func (d *Docker) List(ctx context.Context) ([]Container, error) {
	out, errs, err := d.exec(ctx, "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("ps: %v: %s", err, strings.TrimSpace(errs))
	}
	cs := []Container{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p psLine
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue
		}
		cs = append(cs, Container{
			ID:     p.ID,
			Name:   p.Names,
			Image:  p.Image,
			State:  p.State,
			Status: p.Status,
			Ports:  p.Ports,
		})
	}
	return cs, nil
}

// ImageDigest returns the repo digest of a locally-pulled image (e.g. "sha256:…"),
// or "" if the image isn't present or has no repo digest (locally-built images).
func (d *Docker) ImageDigest(ctx context.Context, image string) (string, error) {
	out, _, err := d.exec(ctx, "image", "inspect", image, "-f", "{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}")
	if err != nil {
		return "", nil // not pulled
	}
	// Inspecting an explicit repo@sha256 reference proves that exact manifest
	// is present. RepoDigests may contain several aliases and Docker does not
	// guarantee the requested digest is index 0, so preserve the identity the
	// caller actually inspected instead of returning an unrelated older alias.
	if digest := explicitImageDigest(image); digest != "" {
		return digest, nil
	}
	s := strings.TrimSpace(out)
	if i := strings.LastIndex(s, "@"); i >= 0 {
		return s[i+1:], nil // "repo@sha256:…" -> "sha256:…"
	}
	return "", nil
}

func explicitImageDigest(image string) string {
	if at := strings.LastIndex(image, "@sha256:"); at >= 0 {
		return image[at+1:]
	}
	return ""
}

// RemoteDigest returns the digest the registry currently serves for image's tag,
// without pulling, via `docker buildx imagetools inspect`.
func (d *Docker) RemoteDigest(ctx context.Context, image string) (string, error) {
	out, errs, err := d.exec(ctx, "buildx", "imagetools", "inspect", image)
	if err != nil {
		return "", fmt.Errorf("remote inspect %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Digest:"); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", nil
}

// ContainerImageDigest resolves a container's image to its repo digest ("sha256:…"),
// so we can tell which exact (possibly digest-pinned) image it's running.
func (d *Docker) ContainerImageDigest(ctx context.Context, name string) (string, error) {
	if err := validateManagedName("container", name); err != nil {
		return "", err
	}
	id, _, err := d.exec(ctx, "inspect", name, "-f", "{{.Image}}")
	if err != nil {
		return "", nil // no such container
	}
	return d.ImageDigest(ctx, strings.TrimSpace(id))
}

// Logs returns a container's captured output (cloudflared prints its URL to stderr).
func (d *Docker) Logs(ctx context.Context, name string) (string, error) {
	if err := validateManagedName("container", name); err != nil {
		return "", err
	}
	out, errs, err := d.exec(ctx, "logs", name)
	if err != nil {
		return "", fmt.Errorf("logs %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return out + errs, nil
}

func parseDockerByteValue(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "0B" {
		return 0, nil
	}
	index := 0
	for index < len(value) && (value[index] == '.' || value[index] >= '0' && value[index] <= '9') {
		index++
	}
	if index == 0 {
		return 0, fmt.Errorf("invalid byte value %q", value)
	}
	number, err := strconv.ParseFloat(value[:index], 64)
	if err != nil {
		return 0, err
	}
	unit := strings.ToUpper(strings.TrimSpace(value[index:]))
	multipliers := map[string]float64{"B": 1, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "TIB": 1 << 40}
	multiplier, ok := multipliers[unit]
	if !ok {
		return 0, fmt.Errorf("invalid byte unit %q", unit)
	}
	return int64(number * multiplier), nil
}

// ContainerIO returns Docker's direct network counters for one managed
// container. These counters remain useful even when installers redraw or omit
// their console progress lines.
func (d *Docker) ContainerIO(ctx context.Context, name string) (ContainerIO, error) {
	if err := validateManagedName("container", name); err != nil {
		return ContainerIO{}, err
	}
	out, errs, err := d.exec(ctx, "stats", "--no-stream", "--format", "{{.NetIO}}", name)
	if err != nil {
		return ContainerIO{}, fmt.Errorf("inspect network IO %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	parts := strings.Split(strings.TrimSpace(out), "/")
	if len(parts) != 2 {
		return ContainerIO{}, fmt.Errorf("unexpected network IO for %s", name)
	}
	received, err := parseDockerByteValue(parts[0])
	if err != nil {
		return ContainerIO{}, err
	}
	sent, err := parseDockerByteValue(parts[1])
	if err != nil {
		return ContainerIO{}, err
	}
	return ContainerIO{ReceivedBytes: received, SentBytes: sent}, nil
}

func (d *Docker) Find(ctx context.Context, name string) (*Container, error) {
	if err := validateManagedName("container", name); err != nil {
		return nil, err
	}
	cs, err := d.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range cs {
		if cs[i].Name == name {
			return &cs[i], nil
		}
	}
	return nil, nil
}
