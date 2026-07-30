package engine

import (
	"reflect"
	"strings"
	"testing"
)

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

func TestRunArgsSupportsEntrypointOverride(t *testing.T) {
	args := strings.Join(runArgs(RunSpec{Name: "ray", Image: "vllm", EntryPoint: "/bin/bash", Args: []string{"-lc", "ray start"}}), " ")
	if !strings.Contains(args, "--entrypoint /bin/bash vllm -lc ray start") {
		t.Fatalf("entrypoint override missing or misplaced: %q", args)
	}
}

func TestRunArgsDeterministic(t *testing.T) {
	spec := RunSpec{
		Name: "x", Image: "img",
		Env:   map[string]string{"B": "2", "A": "1", "C": "3"},
		Ports: map[int]int{9000: 90, 8000: 80},
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

func TestGenericNVIDIAUsesDockerGPURequest(t *testing.T) {
	t.Setenv("CLOUDLESS_PLATFORM", "generic")
	args := strings.Join(runArgs(RunSpec{Name: "x", Image: "img", GPUs: "all"}), " ")
	if !strings.Contains(args, "--gpus all") {
		t.Fatalf("generic args do not use Docker GPU request: %s", args)
	}
}
