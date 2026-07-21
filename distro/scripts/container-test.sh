#!/usr/bin/env bash
# Reproducible package validation for hosts that only have Docker available.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "container-test.sh must run as root inside the build container" >&2
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq imagemagick librsvg2-bin python3-yaml >/dev/null
bash distro/scripts/test-packages.sh
python3 - <<'PY'
from pathlib import Path
import yaml

config = yaml.safe_load(Path("distro/iso/autoinstall.yaml").read_text())
autoinstall = config["autoinstall"]
assert autoinstall["version"] == 1
assert {"network", "storage", "identity"} <= set(autoinstall["interactive-sections"])
assert any(snap["name"] == "chromium" for snap in autoinstall["snaps"])
assert len(autoinstall["late-commands"]) >= 5
print("Autoinstall YAML validation passed")
PY
