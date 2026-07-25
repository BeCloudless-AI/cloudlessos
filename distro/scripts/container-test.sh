#!/usr/bin/env bash
# Reproducible package validation for hosts that only have Docker available.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
    echo "container-test.sh must run as root inside the build container" >&2
    exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq file imagemagick librsvg2-bin python3-yaml >/dev/null
bash distro/scripts/test-packages.sh
bash distro/scripts/test-architectures.sh
python3 - <<'PY'
from pathlib import Path
import re
import subprocess
import yaml

config = yaml.safe_load(Path("distro/iso/autoinstall.yaml").read_text())
autoinstall = config["autoinstall"]
assert autoinstall["version"] == 1
assert {"network", "storage", "identity"} <= set(autoinstall["interactive-sections"])
assert any(snap["name"] == "chromium" for snap in autoinstall["snaps"])
assert len(autoinstall["late-commands"]) >= 5

# Cloudless packages are installed with dpkg during late-commands, so every
# dependency they add must already be present in the target package set.
installer_packages = set(autoinstall["packages"])
shell_deb = next(Path("distro/out/packages").glob("cloudless-shell_*.deb"))
depends = subprocess.check_output(
    ["dpkg-deb", "-f", str(shell_deb), "Depends"], text=True
).strip()
required = {
    re.split(r"[ (]", alternative.strip())[0]
    for clause in depends.split(",")
    for alternative in [clause.split("|")[0]]
    if alternative.strip()
}
missing = required - installer_packages
assert not missing, f"cloudless-shell dependencies missing from autoinstall packages: {sorted(missing)}"
print("Autoinstall YAML validation passed")
PY
