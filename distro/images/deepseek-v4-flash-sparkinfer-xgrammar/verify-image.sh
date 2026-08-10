#!/usr/bin/env bash
set -euo pipefail

image="${1:?usage: verify-image.sh IMAGE}"

docker run --rm -i --network none --read-only \
  --cap-drop ALL --security-opt no-new-privileges:true \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=64m \
  --pids-limit 256 --memory 4g --memory-swap 4g \
  --entrypoint /opt/runtime-venv/bin/python "$image" - <<'PY'
import importlib.metadata as metadata

import xgrammar
from xgrammar import normalize_tool_choice
from vllm.tool_parsers.structural_tag_registry import get_model_structural_tag

version = metadata.version("xgrammar")
assert version == "0.2.1"
assert callable(normalize_tool_choice)
assert callable(get_model_structural_tag)

tools = [{
    "type": "function",
    "function": {
        "name": "get_weather",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"],
        },
    },
}]
functions, builtins, choice = normalize_tool_choice(tools, "required")
assert len(functions) == 1
assert not builtins
assert choice == "required"
print(f"xgrammar={version}; vLLM tool parser import and normalization passed")
PY
