#!/usr/bin/env bash
# Show which registry/namespace docker is logged into (username only, never the secret).
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/whoami-registry.sh
python3 - <<'PY'
import json, base64, os
path = os.path.expanduser("~/.docker/config.json")
try:
    d = json.load(open(path))
except Exception as e:
    print("no docker config at", path, "->", e); raise SystemExit
auths = d.get("auths", {})
if not auths:
    print("no auths recorded (maybe a credential helper:", d.get("credsStore") or d.get("credHelpers"), ")")
for reg, info in auths.items():
    a = info.get("auth", "")
    user = ""
    if a:
        try:
            user = base64.b64decode(a).decode("utf-8", "ignore").split(":", 1)[0]
        except Exception:
            user = "<helper>"
    print("registry:", reg, " user:", user or "<via helper>")
PY
