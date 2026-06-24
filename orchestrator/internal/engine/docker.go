package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
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
	if _, _, err := d.exec(ctx, "network", "inspect", name); err == nil {
		return nil
	}
	if _, errs, err := d.exec(ctx, "network", "create", name); err != nil {
		return fmt.Errorf("create network %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) ConnectNetwork(ctx context.Context, network, container string) error {
	_, errs, err := d.exec(ctx, "network", "connect", network, container)
	if err != nil {
		if strings.Contains(errs, "already exists") || strings.Contains(errs, "already connected") {
			return nil
		}
		return fmt.Errorf("connect %s to %s: %v: %s", container, network, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) HasAlias(ctx context.Context, container, alias string) (bool, error) {
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
	full := append([]string{"exec", container}, args...)
	if _, errs, err := d.exec(ctx, full...); err != nil {
		return fmt.Errorf("exec %s: %v: %s", container, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) Pull(ctx context.Context, image string) error {
	_, errs, err := d.exec(ctx, "pull", image)
	if err != nil {
		return fmt.Errorf("pull %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	return nil
}

// PullStream runs `docker pull` and calls onLine for each output line. In
// non-TTY mode docker emits discrete per-layer status lines (e.g. "<id>: Pull
// complete"), which the caller can parse for progress.
func (d *Docker) PullStream(ctx context.Context, image string, onLine func(string)) error {
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

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			onLine(line)
		}
	}
	if err := <-done; err != nil {
		return fmt.Errorf("pull %s: %w", image, err)
	}
	return nil
}

// Build runs `docker build -t image contextDir`, streaming output to onLine.
func (d *Docker) Build(ctx context.Context, image, contextDir string, onLine func(string)) error {
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
	if _, errs, err := d.exec(ctx, "rmi", "-f", image); err != nil {
		return fmt.Errorf("rmi %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) Run(ctx context.Context, spec RunSpec) (string, error) {
	args := []string{"run", "-d", "--name", spec.Name, "--restart", "unless-stopped"}
	if spec.GPUs != "" {
		args = append(args, "--gpus", spec.GPUs)
	}
	if spec.Network != "" {
		args = append(args, "--network", spec.Network)
		if spec.Network != "host" && spec.NetworkAlias != "" {
			args = append(args, "--network-alias", spec.NetworkAlias)
		}
	}
	// Host networking binds host ports directly; -p is invalid there.
	if spec.Network != "host" {
		for host, cont := range spec.Ports {
			args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", host, cont))
		}
	}
	for k, v := range spec.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	for host, cont := range spec.Volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", host, cont))
	}
	args = append(args, spec.Image)
	args = append(args, spec.Args...)

	out, errs, err := d.exec(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("run %s: %v: %s", spec.Image, err, strings.TrimSpace(errs))
	}
	return strings.TrimSpace(out), nil
}

func (d *Docker) Stop(ctx context.Context, name string) error {
	_, errs, err := d.exec(ctx, "stop", name)
	if err != nil {
		return fmt.Errorf("stop %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) Remove(ctx context.Context, name string) error {
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
	s := strings.TrimSpace(out)
	if i := strings.LastIndex(s, "@"); i >= 0 {
		return s[i+1:], nil // "repo@sha256:…" -> "sha256:…"
	}
	return "", nil
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
	id, _, err := d.exec(ctx, "inspect", name, "-f", "{{.Image}}")
	if err != nil {
		return "", nil // no such container
	}
	return d.ImageDigest(ctx, strings.TrimSpace(id))
}

// Output runs an arbitrary read-only `docker <args>` and returns stdout.
func (d *Docker) Output(ctx context.Context, args ...string) (string, error) {
	out, errs, err := d.exec(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("docker %v: %v: %s", args, err, strings.TrimSpace(errs))
	}
	return out, nil
}

// Logs returns a container's captured output (cloudflared prints its URL to stderr).
func (d *Docker) Logs(ctx context.Context, name string) (string, error) {
	out, errs, err := d.exec(ctx, "logs", name)
	if err != nil {
		return "", fmt.Errorf("logs %s: %v: %s", name, err, strings.TrimSpace(errs))
	}
	return out + errs, nil
}

func (d *Docker) Find(ctx context.Context, name string) (*Container, error) {
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
