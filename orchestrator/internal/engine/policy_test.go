package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSpecPolicyAcceptsManagedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_STATE_DIR", root)
	config := filepath.Join(root, "apps", "open-webui")
	if err := os.MkdirAll(config, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := RunSpec{
		Name: "cloudless-open-webui", Image: "ghcr.io/open-webui/open-webui@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Ports: map[int]int{3000: 8080}, Network: "cloudless", NetworkAlias: "cloudless-ai",
		Volumes: map[string]string{config: "/app/backend/data", "cloudless-hf": "/root/.cache/huggingface"},
		GPUs:    "all", IPC: "host", Ulimits: []string{"memlock=-1", "stack=67108864"},
		SecurityOpts: []string{"no-new-privileges:true"}, ExtraHosts: []string{"host.docker.internal:host-gateway"},
	}
	if err := ValidateRunSpec(spec); err != nil {
		t.Fatal(err)
	}
}

func TestRunSpecPolicyAllowsOnlyProtectedHuggingFaceSecret(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_STATE_DIR", root)
	token := filepath.Join(root, "huggingface-token")
	if err := os.WriteFile(token, []byte("hf_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := RunSpec{
		Name: "cloudless-model", Image: "example/image:1",
		SecretFiles: map[string]string{token: HuggingFaceTokenContainerPath},
	}
	if err := ValidateRunSpec(spec); err != nil {
		t.Fatalf("protected token rejected: %v", err)
	}

	if err := os.Chmod(token, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRunSpec(spec); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("weak token mode error = %v", err)
	}
	if err := os.Chmod(token, 0o600); err != nil {
		t.Fatal(err)
	}
	spec.SecretFiles = map[string]string{token: "/tmp/token"}
	if err := ValidateRunSpec(spec); err == nil || !strings.Contains(err.Error(), "credential boundary") {
		t.Fatalf("arbitrary secret target error = %v", err)
	}
}

func TestRunSpecPolicyRejectsHostAuthority(t *testing.T) {
	base := RunSpec{Name: "cloudless-test", Image: "example/image:1"}
	tests := []struct {
		name string
		edit func(*RunSpec)
		want string
	}{
		{"unmanaged name", func(s *RunSpec) { s.Name = "postgres" }, "namespace"},
		{"root mount", func(s *RunSpec) { s.Volumes = map[string]string{"/": "/host"} }, "outside"},
		{"docker socket source", func(s *RunSpec) { s.Volumes = map[string]string{"/var/run/docker.sock": "/sock"} }, "outside"},
		{"docker socket target", func(s *RunSpec) { s.Volumes = map[string]string{"cloudless-data": "/var/run/docker.sock"} }, "forbidden"},
		{"unmanaged volume", func(s *RunSpec) { s.Volumes = map[string]string{"postgres-data": "/data"} }, "managed volume"},
		{"unmanaged network", func(s *RunSpec) { s.Network = "host-services" }, "namespace"},
		{"host alias", func(s *RunSpec) { s.ExtraHosts = []string{"metadata:169.254.169.254"} }, "not allowed"},
		{"security override", func(s *RunSpec) { s.SecurityOpts = []string{"apparmor=unconfined"} }, "not allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := base
			tt.edit(&spec)
			err := ValidateRunSpec(spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error=%v want substring %q", err, tt.want)
			}
		})
	}
}

func TestRunSpecPolicyAllowsOnlyExplicitRootMaintenanceUser(t *testing.T) {
	spec := RunSpec{Name: "cloudless-helper", Image: "busybox", User: "0"}
	if err := ValidateRunSpec(spec); err != nil {
		t.Fatalf("root maintenance helper rejected: %v", err)
	}
	spec.User = "1000"
	if err := ValidateRunSpec(spec); err == nil {
		t.Fatal("arbitrary container user accepted")
	}
}

func TestRunSpecPolicyRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_STATE_DIR", root)
	link := filepath.Join(root, "escape")
	if err := os.Symlink("/", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := ValidateRunSpec(RunSpec{Name: "cloudless-test", Image: "example/image:1", Volumes: map[string]string{link: "/data"}})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
}

func TestRunSpecPolicyRejectsNestedSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLOUDLESS_STATE_DIR", root)
	parent := filepath.Join(root, "apps")
	if err := os.Symlink("/", parent); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := ValidateRunSpec(RunSpec{Name: "cloudless-test", Image: "example/image:1", Volumes: map[string]string{filepath.Join(parent, "etc"): "/data"}})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error=%v", err)
	}
}

func TestBuildPolicyAcceptsOnlyMaterializedContexts(t *testing.T) {
	contextDir, err := os.MkdirTemp("", "cloudless-build-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(contextDir)
	if err := validateBuild("cloudless-local:latest", contextDir); err != nil {
		t.Fatal(err)
	}
	if err := validateBuild("cloudless-local:latest", t.TempDir()); err == nil {
		t.Fatal("ordinary temporary directory was accepted as a build context")
	}
	if err := validateBuild("cloudless-local:latest", "/"); err == nil {
		t.Fatal("host root was accepted as a build context")
	}
}

func TestBuildPolicyAcceptsSharedBrokerStaging(t *testing.T) {
	state := t.TempDir()
	t.Setenv("CLOUDLESS_STATE_DIR", state)
	buildRoot := filepath.Join(state, "builds")
	if err := os.MkdirAll(buildRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	contextDir, err := os.MkdirTemp(buildRoot, "cloudless-build-")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBuild("cloudless-local:latest", contextDir); err != nil {
		t.Fatal(err)
	}
}

func TestManagedMutationNames(t *testing.T) {
	if err := validateManagedName("container", "cloudless-model"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model", "docker", "cloudless-", "cloudless-../../root"} {
		if validateManagedName("container", name) == nil {
			t.Fatalf("name %q was accepted", name)
		}
	}
}

func TestGPUSelectionPolicy(t *testing.T) {
	for _, value := range []string{"", "all", "0", "0,1,7"} {
		if err := validateGPURequest(value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"-1", "256", "0,root", "device=all"} {
		if validateGPURequest(value) == nil {
			t.Fatalf("%q was accepted", value)
		}
	}
}
