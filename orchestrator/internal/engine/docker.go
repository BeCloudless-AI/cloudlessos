package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func (d *Docker) GPUInfo(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=name,memory.used,memory.total,utilization.gpu,driver_version",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nvidia-smi failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *Docker) Pull(ctx context.Context, image string) error {
	_, errs, err := d.exec(ctx, "pull", image)
	if err != nil {
		return fmt.Errorf("pull %s: %v: %s", image, err, strings.TrimSpace(errs))
	}
	return nil
}

func (d *Docker) Run(ctx context.Context, spec RunSpec) (string, error) {
	args := []string{"run", "-d", "--name", spec.Name, "--restart", "unless-stopped"}
	if spec.GPUs != "" {
		args = append(args, "--gpus", spec.GPUs)
	}
	for host, cont := range spec.Ports {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", host, cont))
	}
	for k, v := range spec.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	for host, cont := range spec.Volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", host, cont))
	}
	args = append(args, spec.Image)

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
