#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECK="$ROOT/distro/scripts/verify-release-container-platforms.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cat > "$work/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
platform=""
while [ "$#" -gt 0 ]; do
    if [ "$1" = --platform ]; then platform="$2"; shift 2; continue; fi
    shift
done
if [ "${FAKE_DOCKER_REJECT:-}" = "$platform" ]; then
    echo "exec /usr/bin/bash: exec format error" >&2
    exit 255
fi
case "$platform" in
    linux/amd64) echo x86_64 ;;
    linux/arm64) echo aarch64 ;;
    *) echo "unexpected platform: $platform" >&2; exit 2 ;;
esac
EOF
chmod +x "$work/docker"

CLOUDLESS_DOCKER="$work/docker" bash "$CHECK" >/dev/null
if CLOUDLESS_DOCKER="$work/docker" FAKE_DOCKER_REJECT=linux/arm64 \
    bash "$CHECK" >"$work/output" 2>&1; then
    echo "Container-platform check accepted an unusable ARM64 runtime" >&2
    exit 1
fi
grep -Fq 'Docker cannot execute linux/arm64 containers' "$work/output"
grep -Fq 'tonistiigi/binfmt --install arm64' "$work/output"

echo "Release container-platform checks passed."
