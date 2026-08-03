package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	managedRuntimeName = regexp.MustCompile(`^cloudless(?:-[a-z0-9][a-z0-9_.-]{0,118})?$`)
	managedVolumeName  = regexp.MustCompile(`^cloudless-[a-z0-9][a-z0-9_.-]{0,118}$`)
	imageReference     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]{0,511}$`)
	environmentName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	ulimitValue        = regexp.MustCompile(`^(memlock|stack)=-?[0-9]+(?::-?[0-9]+)?$`)
	sizeValue          = regexp.MustCompile(`^[1-9][0-9]*(?:[kKmMgG])?$`)
	labelValue         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,191}$`)
	capabilityName     = regexp.MustCompile(`^(?:ALL|[A-Z][A-Z0-9_]{0,63})$`)
	unprivilegedUser   = regexp.MustCompile(`^[1-9][0-9]*:[1-9][0-9]*$`)
)

// ValidateRunSpec is the container-broker admission policy. It rejects host
// authority that Docker would otherwise make available to a compromised API
// daemon, while retaining the bounded runtime features Cloudless owns.
func ValidateRunSpec(spec RunSpec) error {
	if !managedRuntimeName.MatchString(spec.Name) {
		return fmt.Errorf("container name %q is outside the Cloudless namespace", spec.Name)
	}
	if !imageReference.MatchString(strings.TrimSpace(spec.Image)) {
		return errors.New("container image reference is invalid")
	}
	if err := validateNetwork(spec); err != nil {
		return err
	}
	for host, container := range spec.Ports {
		if host < 1 || host > 65535 || container < 1 || container > 65535 {
			return errors.New("container port is outside the valid range")
		}
	}
	for source, target := range spec.Volumes {
		if err := validateVolume(source, target); err != nil {
			return err
		}
	}
	for source, target := range spec.SecretFiles {
		if err := validateSecretFile(source, target); err != nil {
			return err
		}
	}
	for name := range spec.Env {
		if !environmentName.MatchString(name) {
			return fmt.Errorf("environment name %q is invalid", name)
		}
	}
	for _, option := range spec.SecurityOpts {
		if strings.TrimSpace(option) != "no-new-privileges:true" {
			return fmt.Errorf("security option %q is not allowed", option)
		}
	}
	for _, capability := range append(append([]string{}, spec.CapAdd...), spec.CapDrop...) {
		if !capabilityName.MatchString(strings.TrimSpace(capability)) {
			return fmt.Errorf("container capability %q is invalid", capability)
		}
	}
	for _, limit := range spec.Ulimits {
		if !ulimitValue.MatchString(strings.TrimSpace(limit)) {
			return fmt.Errorf("ulimit %q is not allowed", limit)
		}
	}
	if spec.IPC != "" && spec.IPC != "host" && spec.IPC != "private" && spec.IPC != "shareable" {
		return fmt.Errorf("IPC mode %q is not allowed", spec.IPC)
	}
	if spec.PidsLimit < 0 || spec.PidsLimit > 262144 {
		return errors.New("container process limit is invalid")
	}
	if spec.ShmSize != "" && !sizeValue.MatchString(strings.TrimSpace(spec.ShmSize)) {
		return errors.New("shared-memory size is invalid")
	}
	for _, tmpfs := range spec.Tmpfs {
		target := strings.SplitN(strings.TrimSpace(tmpfs), ":", 2)[0]
		if target != "/tmp" && target != "/run" {
			return fmt.Errorf("tmpfs target %q is not allowed", target)
		}
	}
	for _, device := range spec.Devices {
		if strings.TrimSpace(device) != "/dev/infiniband:/dev/infiniband" {
			return fmt.Errorf("container device %q is not allowed", device)
		}
	}
	for _, host := range spec.ExtraHosts {
		if strings.TrimSpace(host) != "host.docker.internal:host-gateway" {
			return fmt.Errorf("extra host %q is not allowed", host)
		}
	}
	if strings.ContainsRune(spec.EntryPoint, '\x00') {
		return errors.New("entrypoint contains an invalid byte")
	}
	if spec.User != "" && spec.User != "0" && !unprivilegedUser.MatchString(spec.User) {
		return errors.New("container user is not allowed")
	}
	for _, arg := range spec.Args {
		if strings.ContainsRune(arg, '\x00') {
			return errors.New("container argument contains an invalid byte")
		}
	}
	return nil
}

const HuggingFaceTokenContainerPath = "/run/secrets/cloudless-huggingface-token"

func validateSecretFile(source, target string) error {
	source, target = strings.TrimSpace(source), strings.TrimSpace(target)
	expectedSource := filepath.Join(envPath("CLOUDLESS_STATE_DIR", "/var/lib/cloudless"), "huggingface-token")
	if source != expectedSource || target != HuggingFaceTokenContainerPath {
		return errors.New("container secret is outside the Cloudless credential boundary")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect container secret: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("container secret must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("container secret must be owner-only")
	}
	return nil
}

func validateNetwork(spec RunSpec) error {
	switch spec.Network {
	case "", "host", "none", "bridge":
	default:
		if !managedRuntimeName.MatchString(spec.Network) {
			return fmt.Errorf("network %q is outside the Cloudless namespace", spec.Network)
		}
	}
	if spec.NetworkAlias != "" && !managedRuntimeName.MatchString(spec.NetworkAlias) {
		return fmt.Errorf("network alias %q is outside the Cloudless namespace", spec.NetworkAlias)
	}
	return nil
}

func validateVolume(source, target string) error {
	source = strings.TrimSpace(source)
	target = strings.TrimSpace(target)
	if source == "" || target == "" || strings.ContainsRune(source+target, '\x00') {
		return errors.New("container volume is invalid")
	}
	targetPath := strings.SplitN(target, ":", 2)[0]
	if !filepath.IsAbs(targetPath) || filepath.Clean(targetPath) != targetPath {
		return fmt.Errorf("container mount target %q is invalid", target)
	}
	if targetPath == "/var/run/docker.sock" || targetPath == "/run/docker.sock" {
		return errors.New("mounting the Docker socket is forbidden")
	}
	if managedVolumeName.MatchString(source) {
		return nil
	}
	if !filepath.IsAbs(source) {
		return fmt.Errorf("volume source %q is not a managed volume or absolute path", source)
	}
	clean := filepath.Clean(source)
	if clean != source {
		return fmt.Errorf("volume source %q is not canonical", source)
	}
	for _, root := range allowedHostRoots() {
		if pathWithin(clean, root) {
			if err := rejectSymlinkComponents(root, clean); err != nil {
				return fmt.Errorf("volume source %q: %w", source, err)
			}
			return nil
		}
	}
	return fmt.Errorf("host volume %q is outside Cloudless-owned storage", source)
}

func rejectSymlinkComponents(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	current := root
	parts := []string{"."}
	if relative != "." {
		parts = append(parts, strings.Split(relative, string(filepath.Separator))...)
	}
	for _, part := range parts {
		if part != "." {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("must not traverse a symlink")
		}
	}
	return nil
}

func validateBuild(image, contextDir string) error {
	if !imageReference.MatchString(strings.TrimSpace(image)) {
		return errors.New("build image reference is invalid")
	}
	clean := filepath.Clean(strings.TrimSpace(contextDir))
	if !strings.HasPrefix(filepath.Base(clean), "cloudless-build-") {
		return errors.New("build context is outside a Cloudless materialization directory")
	}
	roots := []string{
		filepath.Clean(os.TempDir()),
		filepath.Join(envPath("CLOUDLESS_STATE_DIR", "/var/lib/cloudless"), "builds"),
	}
	var root string
	for _, candidate := range roots {
		if pathWithin(clean, candidate) && clean != candidate {
			root = candidate
			break
		}
	}
	if root == "" {
		return errors.New("build context is outside a Cloudless materialization directory")
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return fmt.Errorf("inspect build context: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("build context must be a real directory")
	}
	return rejectSymlinkComponents(root, clean)
}

func allowedHostRoots() []string {
	roots := []string{
		envPath("CLOUDLESS_STATE_DIR", "/var/lib/cloudless"),
		envPath("CLOUDLESS_HOME", "/home/cloudless/Cloudless"),
		filepath.Join(envPath("CLOUDLESS_DESKTOP_HOME", "/home/cloudless"), "Downloads"),
	}
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		if absolute, err := filepath.Abs(root); err == nil {
			result = append(result, filepath.Clean(absolute))
		}
	}
	return result
}

func envPath(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateManagedName(kind, name string) error {
	if !managedRuntimeName.MatchString(strings.TrimSpace(name)) {
		return fmt.Errorf("%s %q is outside the Cloudless namespace", kind, name)
	}
	return nil
}

func validateGPURequest(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return nil
	}
	for _, item := range strings.Split(value, ",") {
		index, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || index < 0 || index > 255 {
			return errors.New("GPU selection is invalid")
		}
	}
	return nil
}
