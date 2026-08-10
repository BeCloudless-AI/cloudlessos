# DeepSeek V4 Flash SparkInfer XGrammar compatibility image

This image is a minimal derivative of the immutable one-DGX-Spark image used by
the community recipe. It changes only the XGrammar package in
`/opt/runtime-venv`, pinning the minimum version required by the bundled vLLM
source. The Docker build fails unless `normalize_tool_choice` can be imported.

Build on Linux ARM64:

```sh
docker build --pull -t ghcr.io/samuelcardillo/cloudless-deepseek-v4-flash-sparkinfer:1.0.1 .
```

The published recipe must reference the resulting immutable manifest digest,
not this mutable tag.

Before publishing, verify the image-level dependency contract and then run the
three live API checks against the loaded model:

```sh
./verify-image.sh ghcr.io/samuelcardillo/cloudless-deepseek-v4-flash-sparkinfer:1.0.1
./smoke-test.sh http://127.0.0.1:8890
```
