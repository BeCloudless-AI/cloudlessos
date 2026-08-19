# MiaLabs DeepSeek V4 Flash DSpark 2x compatibility image

This is a minimal derivative of Anemll's immutable DSpark vLLM 0.1.1 image.
It applies the hotfix set used by MiaAI-Lab's two-DGX-Spark profile at upstream
revision `3c9576c52ab71d89e22fe4621e0d32300a59039a`.

It also updates only `linux-libc-dev` to Ubuntu Jammy's fixed
`5.15.0-187.197` package. This removes the five actionable CRITICAL findings
reported against the older headers without replacing CUDA, vLLM, Python, or
the rest of the validated inference stack.

The Dockerfile verifies every downloaded patch before applying it. The Issue
#21 encoder patch remains embedded at
`/opt/cloudless-hotfix-encoding-dsv4-issue21.py` because its target comes from
the model snapshot and is installed only when the recipe starts. All other
patches are baked into the image and apply equally on the coordinator and
worker.

The withdrawn Issue #31/#34 V2 `thinking_token_budget` hook is intentionally
absent because MiaAI-Lab found that it caused a major long-context decode
slowdown. The Issue #26 cache correction is the current v2 patch, which lets
sliding-window KV shorten the common prefix hit to prevent corrupted output.
The current stop-string guard is included as well, keeping client-provided stop
strings dormant while the model is inside `<think>` and re-enabling them after
`</think>`.

Build and publish on Linux ARM64:

```sh
docker build --pull \
  -t ghcr.io/samuelcardillo/cloudless-deepseek-v4-flash-dspark-2x:1.0.3 .
docker push ghcr.io/samuelcardillo/cloudless-deepseek-v4-flash-dspark-2x:1.0.3
```

The recipe must use the pushed manifest digest, never the mutable tag.
