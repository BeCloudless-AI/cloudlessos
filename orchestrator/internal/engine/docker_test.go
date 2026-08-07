package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDockerRunCreatesManagedNetworkBeforeContainer(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands")
	networkPath := filepath.Join(dir, "network")
	binary := filepath.Join(dir, "docker")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = network ] && [ "$2" = inspect ]; then
  test -f %q
  exit $?
fi
if [ "$1" = network ] && [ "$2" = create ]; then
  : > %q
  printf 'cloudless\n'
  exit 0
fi
if [ "$1" = run ]; then
  if [ ! -f %q ]; then
    printf 'network cloudless not found\n' >&2
    exit 125
  fi
  printf 'container-id\n'
  exit 0
fi
exit 1
`, logPath, networkPath, networkPath, networkPath)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := (&Docker{bin: binary}).Run(context.Background(), RunSpec{
		Name: "cloudless-test", Image: "cloudless/runtime:test", Network: "cloudless",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "container-id" {
		t.Fatalf("container id = %q", id)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(commands)), "\n")
	if len(lines) != 3 || lines[0] != "network inspect cloudless" || lines[1] != "network create cloudless" || !strings.HasPrefix(lines[2], "run ") {
		t.Fatalf("docker commands = %#v", lines)
	}
}

func TestDockerRunRecoversWhenManagedNetworkDisappears(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands")
	networkPath := filepath.Join(dir, "network")
	failedPath := filepath.Join(dir, "failed-once")
	if err := os.WriteFile(networkPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "docker")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %q
if [ "$1" = network ] && [ "$2" = inspect ]; then
  test -f %q
  exit $?
fi
if [ "$1" = network ] && [ "$2" = create ]; then
  : > %q
  exit 0
fi
if [ "$1" = run ]; then
  if [ ! -f %q ]; then
    : > %q
    rm -f %q
    printf 'docker: Error response from daemon: failed to set up container networking: network cloudless not found\n' >&2
    exit 125
  fi
  test -f %q || exit 125
  printf 'recovered-container-id\n'
  exit 0
fi
exit 1
`, logPath, networkPath, networkPath, failedPath, failedPath, networkPath, networkPath)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := (&Docker{bin: binary}).Run(context.Background(), RunSpec{
		Name: "cloudless-test", Image: "cloudless/runtime:test", Network: "cloudless",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "recovered-container-id" {
		t.Fatalf("container id = %q", id)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(commands), "run "); got != 2 {
		t.Fatalf("run attempts = %d, commands:\n%s", got, commands)
	}
}

func TestEnsureNetworkAcceptsConcurrentCreator(t *testing.T) {
	dir := t.TempDir()
	networkPath := filepath.Join(dir, "network")
	binary := filepath.Join(dir, "docker")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = network ] && [ "$2" = inspect ]; then
  test -f %q
  exit $?
fi
if [ "$1" = network ] && [ "$2" = create ]; then
  : > %q
  printf 'network already exists\n' >&2
  exit 1
fi
exit 1
`, networkPath, networkPath)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&Docker{bin: binary}).EnsureNetwork(context.Background(), "cloudless"); err != nil {
		t.Fatalf("concurrent network creation was not accepted: %v", err)
	}
}

func TestPullStreamUsesStructuredDockerByteProgress(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/images/create" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("fromImage"); got != "cloudless/runtime" {
			t.Errorf("fromImage = %q", got)
		}
		if got := r.URL.Query().Get("tag"); got != "test" {
			t.Errorf("tag = %q", got)
		}
		if got := r.Header.Get("X-Registry-Auth"); got != "e30=" {
			t.Errorf("registry auth = %q", got)
		}
		_, _ = w.Write([]byte("{\"status\":\"Downloading\",\"id\":\"layer123\",\"progressDetail\":{\"current\":25,\"total\":100}}\n"))
		_, _ = w.Write([]byte("{\"status\":\"Pull complete\",\"id\":\"layer123\"}\n"))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	t.Setenv("DOCKER_HOST", "unix://"+socket)

	var lines []string
	err = (&Docker{bin: filepath.Join(t.TempDir(), "must-not-run")}).PullStream(context.Background(), "cloudless/runtime:test", func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"layer123: Downloading 25B/100B", "layer123: Pull complete"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("progress lines = %#v, want %#v", lines, want)
	}
}

func TestDockerPullQueryPreservesRegistryPortsAndDigests(t *testing.T) {
	for image, want := range map[string][2]string{
		"alpine/socat:latest":                                     {"alpine/socat", "latest"},
		"registry.test:5000/team/runtime:v1":                      {"registry.test:5000/team/runtime", "v1"},
		"ghcr.io/team/runtime@sha256:abcdef":                      {"ghcr.io/team/runtime", "sha256:abcdef"},
		"registry.test:5000/team/runtime@sha256:0123456789abcdef": {"registry.test:5000/team/runtime", "sha256:0123456789abcdef"},
	} {
		query := dockerPullQuery(image)
		if got := [2]string{query.Get("fromImage"), query.Get("tag")}; got != want {
			t.Errorf("dockerPullQuery(%q) = %#v, want %#v", image, got, want)
		}
	}
}

func TestTypedRuntimeQueriesRejectUnmanagedAuthority(t *testing.T) {
	docker := &Docker{bin: filepath.Join(t.TempDir(), "must-not-run")}
	ctx := context.Background()
	if _, err := docker.HermesConfigValue(ctx, "cloudless-hermes", "security.admin_token"); err == nil {
		t.Fatal("unadmitted Hermes key was accepted")
	}
	if _, err := docker.ContainerNamesByLabel(ctx, "other.owner", "value"); err == nil {
		t.Fatal("foreign ownership label was accepted")
	}
	if _, err := docker.ContainerNamesByAncestor(ctx, "--privileged"); err == nil {
		t.Fatal("invalid ancestor image was accepted")
	}
	if _, err := docker.LogsTail(ctx, "foreign-container", 200); err == nil {
		t.Fatal("foreign container logs were accepted")
	}
	if _, err := docker.LogsTail(ctx, "cloudless-app", 1001); err == nil {
		t.Fatal("unbounded log tail was accepted")
	}
}

func TestInspectImageParsesBoundedMetadata(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s\n' '[{"Id":"sha256:abc","Architecture":"arm64","Size":4096,"Config":{"Entrypoint":["vllm","serve"]}}]'
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := (&Docker{bin: binary}).InspectImage(context.Background(), "cloudless/runtime:test")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "sha256:abc" || info.Architecture != "arm64" || info.Size != 4096 ||
		!reflect.DeepEqual(info.EntryPoint, []string{"vllm", "serve"}) {
		t.Fatalf("image metadata = %#v", info)
	}
}

func TestPullStreamReturnsBoundedDockerFailureDetail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(dir, "missing-docker.sock"))
	binary := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s\n' '58aacae73b54: Download failed, retrying (1/5): unexpected EOF'
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	err := (&Docker{bin: binary}).PullStream(context.Background(), "cloudless/runtime:test", func(line string) {
		lines = append(lines, line)
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("pull error = %v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "unexpected EOF") {
		t.Fatalf("streamed lines = %#v", lines)
	}
}

func TestAdmittedImageExportPathUsesDiskBackedCloudlessStorage(t *testing.T) {
	if !admittedImageExportPath("/var/lib/cloudless/image-transfers/cloudless-image-123.tar") {
		t.Fatal("disk-backed Cloudless transfer path was rejected")
	}
	for _, path := range []string{
		"/run/cloudless/transfers/cloudless-image-123.tar",
		"/var/lib/cloudless/image-transfers/not-a-transfer.tar",
		"/var/lib/cloudless/image-transfers/../cloudless-image-123.tar",
	} {
		if admittedImageExportPath(path) {
			t.Fatalf("unsafe or obsolete image transfer path was admitted: %s", path)
		}
	}
}

func TestShellSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a b c", []string{"a", "b", "c"}},
		{"--model  Qwen/Qwen2.5  --max-len 32768", []string{"--model", "Qwen/Qwen2.5", "--max-len", "32768"}},
		{`--name "my model"`, []string{"--name", "my model"}},
		{"--name 'my model'", []string{"--name", "my model"}},
		{"line1\nline2\tline3", []string{"line1", "line2", "line3"}},
		{`--arg "a \"quoted\" b"`, []string{"--arg", `a "quoted" b`}},
		{`--empty ""`, []string{"--empty", ""}},
	}
	for _, c := range cases {
		got := ShellSplit(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ShellSplit(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestShellJoinSplitRoundTrip(t *testing.T) {
	argvs := [][]string{
		{"vllm", "--served-model-name", "cloudless", "--gpu-memory-utilization", "0.5"},
		{"--name", "a b c", "--flag"},
		{"--empty", "", "--next"},
		{`a"b`, "c'd"},
	}
	for _, argv := range argvs {
		if got := ShellSplit(ShellJoin(argv)); !reflect.DeepEqual(got, argv) {
			t.Errorf("round-trip %#v -> %q -> %#v", argv, ShellJoin(argv), got)
		}
	}
}

func TestExplicitImageDigestPreservesRequestedManifest(t *testing.T) {
	const digest = "sha256:d4a984cdeb9846ef0d433d80e8fff55d527fa84f91c0eec2c62d9dea7ab26426"
	if got := explicitImageDigest("lmsysorg/sglang@" + digest); got != digest {
		t.Fatalf("explicit digest=%q, want %q", got, digest)
	}
	if got := explicitImageDigest("lmsysorg/sglang:latest-cu130"); got != "" {
		t.Fatalf("tag-only reference returned digest %q", got)
	}
}

func TestPreviewPartsSplitsCommandFromScaffold(t *testing.T) {
	spec := RunSpec{
		Name:         "cloudless-vllm",
		Image:        "vllm/vllm-openai:latest",
		Ports:        map[int]int{8000: 8000},
		Volumes:      map[string]string{"cloudless-hf": "/root/.cache/huggingface"},
		GPUs:         "all",
		Network:      "cloudless",
		NetworkAlias: "cloudless-ai",
		Args:         []string{"Qwen/Qwen2.5-1.5B-Instruct", "--served-model-name", "cloudless"},
	}
	prefix, command := PreviewParts(spec)
	if !strings.HasPrefix(prefix, "docker run -d --name cloudless-vllm") {
		t.Fatalf("prefix start = %q", prefix)
	}
	if !strings.Contains(prefix, "vllm/vllm-openai:latest") {
		t.Fatalf("prefix missing image: %q", prefix)
	}
	if strings.Contains(prefix, "--served-model-name") {
		t.Fatalf("prefix leaked the container command: %q", prefix)
	}
	if command != "Qwen/Qwen2.5-1.5B-Instruct --served-model-name cloudless" {
		t.Fatalf("command = %q", command)
	}
}

func TestRunArgsIncludesClusterHostGateway(t *testing.T) {
	args := strings.Join(runArgs(RunSpec{Name: "proxy", Image: "alpine/socat", ExtraHosts: []string{"host.docker.internal:host-gateway"}}), " ")
	if !strings.Contains(args, "--add-host host.docker.internal:host-gateway") {
		t.Fatalf("cluster proxy args = %q", args)
	}
}

func TestRunArgsSupportsCloudlessOwnedRestartPolicy(t *testing.T) {
	defaults := strings.Join(runArgs(RunSpec{Name: "app", Image: "app"}), " ")
	if !strings.Contains(defaults, "--restart unless-stopped") {
		t.Fatalf("default app restart policy changed: %q", defaults)
	}
	inference := strings.Join(runArgs(RunSpec{Name: "model", Image: "vllm", RestartPolicy: "no"}), " ")
	if !strings.Contains(inference, "--restart no") {
		t.Fatalf("explicit inference restart policy missing: %q", inference)
	}
	untrusted := strings.Join(runArgs(RunSpec{Name: "bad", Image: "app", RestartPolicy: "always --privileged"}), " ")
	if strings.Contains(untrusted, "always") || !strings.Contains(untrusted, "--restart unless-stopped") {
		t.Fatalf("untrusted restart policy passed through: %q", untrusted)
	}
}

func TestRunArgsSupportsEntrypointOverride(t *testing.T) {
	args := strings.Join(runArgs(RunSpec{Name: "ray", Image: "vllm", EntryPoint: "/bin/bash", Args: []string{"-lc", "ray start"}}), " ")
	if !strings.Contains(args, "--entrypoint /bin/bash vllm -lc ray start") {
		t.Fatalf("entrypoint override missing or misplaced: %q", args)
	}
}

func TestRunArgsMountsSecretsWithoutEmbeddingTheirValue(t *testing.T) {
	args := strings.Join(runArgs(RunSpec{
		Name: "cloudless-model", Image: "vllm",
		Env:         map[string]string{"HF_TOKEN_PATH": HuggingFaceTokenContainerPath},
		SecretFiles: map[string]string{"/var/lib/cloudless/huggingface-token": HuggingFaceTokenContainerPath},
	}), " ")
	if !strings.Contains(args, "--mount type=bind,src=/var/lib/cloudless/huggingface-token,dst=/run/secrets/cloudless-huggingface-token,readonly") {
		t.Fatalf("protected secret mount missing: %q", args)
	}
	if !strings.Contains(args, "-e HF_TOKEN_PATH=/run/secrets/cloudless-huggingface-token") || strings.Contains(args, "HF_TOKEN=") {
		t.Fatalf("credential environment is unsafe: %q", args)
	}
}

func TestTransientArgsRemoveRestartAndDetachedMode(t *testing.T) {
	args := transientArgs(RunSpec{
		Name:       "cloudless-helper",
		Image:      "busybox",
		User:       "0",
		EntryPoint: "sh",
		Args:       []string{"-c", "echo ready"},
	})
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "run --rm --name cloudless-helper --user 0") {
		t.Fatalf("transient args start = %q", joined)
	}
	if strings.Contains(joined, " -d ") || strings.Contains(joined, "--restart") {
		t.Fatalf("transient args retain detached lifecycle flags: %q", joined)
	}
	if !strings.Contains(joined, "--entrypoint sh busybox -c echo ready") {
		t.Fatalf("transient args lost structured command: %q", joined)
	}
}

func TestRunArgsDeterministic(t *testing.T) {
	spec := RunSpec{
		Name: "x", Image: "img",
		Env:    map[string]string{"B": "2", "A": "1", "C": "3"},
		Labels: map[string]string{"z": "last", "a": "first"},
		Ports:  map[int]int{9000: 90, 8000: 80},
	}
	first := strings.Join(runArgs(spec), " ")
	for i := 0; i < 20; i++ {
		if got := strings.Join(runArgs(spec), " "); got != first {
			t.Fatalf("runArgs not deterministic:\n%s\n%s", first, got)
		}
	}
	// env sorted A,B,C and ports sorted 8000 before 9000
	if !strings.Contains(first, "-e A=1 -e B=2 -e C=3") {
		t.Fatalf("env not sorted: %s", first)
	}
	if !strings.Contains(first, "--label a=first --label z=last") {
		t.Fatalf("labels not sorted: %s", first)
	}
	if strings.Index(first, "8000") > strings.Index(first, "9000") {
		t.Fatalf("ports not sorted: %s", first)
	}
}

func TestDGXSparkUsesCDIGPUDevice(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "dgx-spark")
	args := strings.Join(runArgs(RunSpec{Name: "x", Image: "img", GPUs: "all"}), " ")
	if !strings.Contains(args, "--device nvidia.com/gpu=all") {
		t.Fatalf("DGX Spark args do not use CDI: %s", args)
	}
	if strings.Contains(args, "--gpus") {
		t.Fatalf("DGX Spark args leaked legacy GPU request: %s", args)
	}
}

func TestInferenceRuntimeLimits(t *testing.T) {
	args := strings.Join(runArgs(RunSpec{
		Name: "x", Image: "img", IPC: "host",
		Ulimits: []string{"memlock=-1", "stack=67108864"},
	}), " ")
	if !strings.Contains(args, "--ipc host") ||
		!strings.Contains(args, "--ulimit memlock=-1") ||
		!strings.Contains(args, "--ulimit stack=67108864") {
		t.Fatalf("inference runtime flags missing: %s", args)
	}
}

func TestRunArgsSupportsBoundedInfiniBandDevice(t *testing.T) {
	spec := RunSpec{Name: "cloudless-recipe", Image: "runtime", Devices: []string{"/dev/infiniband:/dev/infiniband"}}
	if err := ValidateRunSpec(spec); err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(runArgs(spec), " "); !strings.Contains(args, "--device /dev/infiniband:/dev/infiniband") {
		t.Fatalf("InfiniBand device missing: %s", args)
	}
	spec.Devices = []string{"/dev/sda:/dev/sda"}
	if err := ValidateRunSpec(spec); err == nil {
		t.Fatal("arbitrary host device was accepted")
	}
}

func TestRunArgsSupportsConstrainedRecipeSandbox(t *testing.T) {
	args := strings.Join(runArgs(RunSpec{
		Name: "cloudless-recipe", Image: "runtime@sha256:abc", ReadOnly: true,
		CapDrop: []string{"NET_RAW", "ALL"}, SecurityOpts: []string{"no-new-privileges:true"},
		Tmpfs:     []string{"/run:rw,nosuid,size=64m", "/tmp:rw,nosuid,size=16g"},
		PidsLimit: 8192, ShmSize: "16g", Memory: "100g", MemorySwap: "100g",
	}), " ")
	for _, required := range []string{
		"--read-only", "--pids-limit 8192", "--shm-size 16g", "--memory 100g", "--memory-swap 100g",
		"--cap-drop ALL", "--cap-drop NET_RAW",
		"--security-opt no-new-privileges:true",
		"--tmpfs /run:rw,nosuid,size=64m", "--tmpfs /tmp:rw,nosuid,size=16g",
	} {
		if !strings.Contains(args, required) {
			t.Fatalf("sandbox args %q are missing %q", args, required)
		}
	}
}

func TestRunArgsSupportsAdvancedRecipeCapabilities(t *testing.T) {
	args := strings.Join(runArgs(RunSpec{Name: "cloudless-advanced", Image: "img", CapAdd: []string{"SYS_NICE", "IPC_LOCK"}}), " ")
	if !strings.Contains(args, "--cap-add IPC_LOCK") || !strings.Contains(args, "--cap-add SYS_NICE") {
		t.Fatalf("advanced capability arguments missing: %s", args)
	}
}

func TestGenericNVIDIAUsesDockerGPURequest(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	args := strings.Join(runArgs(RunSpec{Name: "x", Image: "img", GPUs: "all"}), " ")
	if !strings.Contains(args, "--gpus all") {
		t.Fatalf("generic args do not use Docker GPU request: %s", args)
	}
}
