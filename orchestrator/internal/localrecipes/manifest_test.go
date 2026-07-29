package localrecipes

import "testing"

func TestParseNativeManifest(t *testing.T) {
	document := []byte(`schema: cloudless.recipe/v1
metadata:
  name: Test recipe
  description: Native Cloudless recipe
recipe:
  name: Test recipe
  description: Native Cloudless recipe
  platform: dgx-spark
  source: {url: "", revision: ""}
  engine:
    type: vllm
    image: cloudless/test:1
    servedModelName: test
    containerPort: 8888
    apiPath: /v1
    proxyHost: host.docker.internal
    restartPolicy: unless-stopped
  model:
    id: example/test
    revision: main
    quantization: none
    dtype: auto
    kvCacheDtype: auto
    maxContext: 32768
    maxSequences: 1
    gpuMemoryUtilization: 0.8
    tensorParallel: 1
    pipelineParallel: 1
  distributed:
    nodes: 1
    backend: nccl
    masterPort: 25000
    interface: auto
    hca: auto
    workerAlias: cloudless-worker
  runtime:
    adapter: source-scripts-v1
    workingDir: .
    timeoutMinutes: 60
    prerequisites: [bash]
    lifecycle:
      start: {program: bash, args: [start.sh]}
      stop: {program: bash, args: [stop.sh]}
  health:
    scheme: http
    host: 127.0.0.1
    port: 8888
    path: /health
    timeoutSeconds: 60
    intervalSeconds: 2
`)
	_, parsed, err := ParseNativeManifest(document)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Name != "Test recipe" || parsed.Engine.Image != "cloudless/test:1" {
		t.Fatalf("unexpected recipe: %#v", parsed)
	}
}
