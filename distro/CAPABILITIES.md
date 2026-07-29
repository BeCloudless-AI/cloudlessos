# CloudlessOS capabilities and platform-locked features

CloudlessOS uses one source tree, version, signed repository, and release
pipeline for AMD64 computers and ARM64 DGX Spark systems. Differences are
expressed as backend-evaluated capabilities rather than maintained as product
forks.

## Authority

`orchestrator/internal/capabilities` is the single feature authority. Its
snapshot includes:

- detected platform and architecture;
- the CloudlessOS version injected into the production binary;
- the installed DGX OS version, when applicable;
- an availability verdict and reason for every registered feature.

Clients read `GET /api/capabilities`. `GET /api/system` includes the same
snapshot. The browser exposes `hasCapability(id)` for rendering, but hiding a
control is never authorization: the corresponding backend handler must call
`requireCapability`.

For example, generic Ubuntu NVIDIA driver installation requires
`generic-nvidia-driver-management`. That capability is unavailable on DGX
Spark, and the API rejects a manually crafted driver-install request there.

## Add a feature

1. Add a stable identifier and requirement to the registry in
   `internal/capabilities/capabilities.go`.
2. In the backend operation, call `requireCapability` before changing state.
3. In the interface, render the feature only when `hasCapability(id)` is true.
4. Add evaluator, API-denial, and interface tests.

A requirement may combine:

```go
capabilities.Requirement{
    Platforms:           []string{"dgx-spark"},
    Architectures:       []string{"arm64"},
    MinCloudlessVersion: "0.4.0",
    MinDGXOSVersion:     "7.6.0",
    RequiredFeatures:    []string{"nvidia-cdi"},
}
```

The evaluator fails closed when a version is absent, malformed, or too old.
Development builds do not satisfy a requirement for the corresponding final
release.

## Lock an application recipe

Catalog applications support the same requirement fields:

```go
App{
    ID:                  "spark-cluster",
    Platforms:           []string{"dgx-spark"},
    Architectures:       []string{"arm64"},
    MinCloudlessVersion: "0.4.0",
    MinDGXOSVersion:     "7.6.0",
    RequiredFeatures:    []string{"nvidia-cdi"},
}
```

Unsupported recipes are omitted by `catalog.All` and rejected by
`catalog.Get`. This protects install, start, settings, update, sharing, and
other application API paths even if a caller fabricates a request.

When only one architecture variant is platform-specific, use
`ArchPlatforms`. Cloudless currently uses this to ensure the ARM64 vLLM,
SGLang, and ComfyUI recipes qualified for DGX Spark are not exposed on an
unrelated generic ARM64 computer, while their AMD64 recipes remain available.

## When code must not ship to another platform

Capability gates are appropriate for normal product differences. A feature
that is very large, licensed separately, or must not exist on another system
should live in a dedicated package such as `cloudless-dgx-features`.
`install-dgx-spark.sh` would install it, and the updater would include it only
after detecting DGX Spark. The shared API should still advertise and enforce a
capability so package presence alone never grants access accidentally.

Do not create a separate GUI branch or reuse the CloudlessOS version number to
mean different source on different platforms. The signed release records one
Git commit and builds both architectures together.

## Custom engine boundary

Locally registered vLLM/SGLang images are not signed catalog applications and are never treated
as Cloudless-verified. They inherit a supported base engine contract but remain user-supplied
code. Registration verifies that the Docker image exists locally; activation still must pass the
normal OpenAI readiness contract.

Do not use custom registration to bypass a platform-locked product feature. Custom engines are
currently local-only, receive no cluster distribution capability, and do not enter the signed
update pipeline. See [`../docs/CUSTOM_ENGINES.md`](../docs/CUSTOM_ENGINES.md).
