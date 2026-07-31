#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/packages"
for arch in amd64 arm64; do
    for package in cloudless-orchestrator cloudless-shell cloudless-branding cloudless-hardware cloudless-firstboot cloudless-updater; do
        printf '%s\n' "$package-$arch" > "$TMP/packages/${package}_9.9.9_${arch}.deb"
    done
done
printf 'example.invalid/module v1.2.3 h1:test\n' > "$TMP/go.sum"
CLOUDLESS_SBOM_PACKAGE_DIR="$TMP/packages" CLOUDLESS_SBOM_GO_SUM="$TMP/go.sum" CLOUDLESS_SOURCE_COMMIT=0123456789abcdef0123456789abcdef01234567 \
    python3 "$ROOT/distro/scripts/generate-sbom.py" 9.9.9 "$TMP/cloudless.spdx.json"
CLOUDLESS_SBOM_PACKAGE_DIR="$TMP/packages" CLOUDLESS_SBOM_GO_SUM="$TMP/go.sum" CLOUDLESS_SOURCE_COMMIT=0123456789abcdef0123456789abcdef01234567 \
    python3 "$ROOT/distro/scripts/generate-sbom.py" 9.9.9 "$TMP/cloudless-repeat.spdx.json"
cmp "$TMP/cloudless.spdx.json" "$TMP/cloudless-repeat.spdx.json"
python3 - "$TMP/cloudless.spdx.json" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    data = json.load(handle)
assert data["spdxVersion"] == "SPDX-2.3"
assert len(data["documentDescribes"]) == 12
assert any(item["name"] == "example.invalid/module" for item in data["packages"])
assert all(item.get("checksums") for item in data["packages"] if item["name"].startswith("cloudless-"))
PY
rm "$TMP/packages/cloudless-updater_9.9.9_arm64.deb"
if CLOUDLESS_SBOM_PACKAGE_DIR="$TMP/packages" CLOUDLESS_SBOM_GO_SUM="$TMP/go.sum" CLOUDLESS_SOURCE_COMMIT=0123456789abcdef0123456789abcdef01234567 \
    python3 "$ROOT/distro/scripts/generate-sbom.py" 9.9.9 "$TMP/incomplete.spdx.json" >/dev/null 2>&1; then
    echo "SBOM generator accepted an incomplete architecture/package matrix" >&2
    exit 1
fi
bash "$ROOT/distro/scripts/scan-vulnerabilities.sh" --check
