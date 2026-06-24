#!/usr/bin/env bash
# Verify the App Launcher (categories + pinning) and the Cloudless Assistant.
# Run from WSL Ubuntu:  bash /mnt/d/Cloudless/scripts/launcher-assistant-check.sh
set -uo pipefail
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"
export GOFLAGS=-buildvcs=false
export CLOUDLESS_STATE_DIR="$(mktemp -d)/state"   # isolated so pin writes don't leak
# Run on a separate port with provisioning OFF so we never disturb the user's
# primary daemon (which owns :8765 and its containers).
export CLOUDLESS_ADDR="127.0.0.1:8799"
export CLOUDLESS_NO_PROVISION=1
P=8799
BIN=/tmp/cl-la
( cd /mnt/d/Cloudless/orchestrator && go build -o "$BIN" ./cmd/cloudlessd ) || exit 1
pkill -x cl-la 2>/dev/null || true
sg docker -c "$BIN" >/tmp/cl-la.log 2>&1 &
trap 'pkill -x cl-la 2>/dev/null || true' EXIT
for i in $(seq 1 40); do curl -sf localhost:$P/api/health >/dev/null 2>&1 && break; sleep 0.5; done

echo "catalog category : $(curl -s localhost:$P/api/catalog | grep -oc '\"category\":' || true) apps tagged"
echo "comfyui tagline  : $(curl -s localhost:$P/api/catalog | grep -oE '\"tagline\":\"[^\"]*\"' | head -1)"
echo "pins (default)   : $(curl -s localhost:$P/api/pins)"
echo "pin comfyui      : $(curl -s -X POST localhost:$P/api/apps/comfyui/pin)"
echo "pins after pin   : $(curl -s localhost:$P/api/pins)"
echo "unpin comfyui    : $(curl -s -X POST localhost:$P/api/apps/comfyui/pin)"
echo "pin engine(vllm) : $(curl -s -o /dev/null -w '%{http_code}' -X POST localhost:$P/api/apps/vllm/pin) (expect 404 â€” engines not launchable)"

echo "==> assistant chat (engine likely cold â†’ warmup path):"
curl -s -N -X POST localhost:$P/api/assistant/chat -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"I want to build images"}]}' | head -3

echo "catalog long/ex  : long=$(curl -s localhost:$P/api/catalog | grep -oc '\"long\":') examples=$(curl -s localhost:$P/api/catalog | grep -oc '\"examples\":')"
echo "UI markers       : $(curl -s localhost:$P/ | grep -oE 'id=\"launcher\"|id=\"lp-detail\"|renderAppDetail|selectApp|class=\"ask\"|id=\"asst-backdrop\"|id=\"asst-exp\"|openAsst\(true\)|setAsstFull' | sort -u | tr '\n' ' ')"
echo "no OWUI chat link: $(curl -s localhost:$P/ | grep -oc 'localhost:3000' || true) (expect 0)"
echo "owui tunnel  : $(curl -s localhost:$P/api/apps/open-webui/tunnel)"
echo "comfy tunnel : $(curl -s localhost:$P/api/apps/comfyui/tunnel)"
echo "hermes tunnel: $(curl -s localhost:$P/api/apps/hermes/tunnel)"
echo "tunnel UI    : $(curl -s localhost:$P/ | grep -oE 'id=\"tun-toggle\"|tunRender|share online|/tunnel' | sort -u | tr '\n' ' ')"
echo "owui lan     : $(curl -s localhost:$P/api/apps/open-webui/lan)"
echo "hermes lan   : $(curl -s localhost:$P/api/apps/hermes/lan)"
echo "lan UI       : $(curl -s localhost:$P/ | grep -oE 'id=\"lan-toggle\"|lanRender|local network|/lan' | sort -u | tr '\n' ' ')"
echo "localnet dflt: $(curl -s localhost:$P/api/network/local) (expect enabled:true default)"
printf 'localnet off : '; curl -s -X POST localhost:$P/api/network/local -H 'Content-Type: application/json' -d '{"enable":false}'; echo
echo "localnet now : $(curl -s localhost:$P/api/network/local) (expect enabled:false persisted)"
echo "onboarding UI: $(curl -s localhost:$P/ | grep -oE 'id=\"ob-lan\"|net-opt|/api/network/local|Local network' | sort -u | tr '\n' ' ')"
echo "ai-toolkit   : in-catalog=$(curl -s localhost:$P/api/catalog | grep -oc '\"id\":\"ai-toolkit\"') image=$(curl -s localhost:$P/api/catalog | grep -oE 'ostris/aitoolkit:latest' | head -1)"
echo "aitk lan     : $(curl -s localhost:$P/api/apps/ai-toolkit/lan)"
echo "unsloth      : in-catalog=$(curl -s localhost:$P/api/catalog | grep -oc '\"id\":\"unsloth\"') image=$(curl -s localhost:$P/api/catalog | grep -oE 'unsloth/unsloth:latest' | head -1) cat=$(curl -s localhost:$P/api/catalog | grep -oE 'Training & fine-tuning' | head -1)"
echo "unsloth lan  : $(curl -s localhost:$P/api/apps/unsloth/lan)"
echo "net status   : $(curl -s localhost:$P/api/network/status) (dev box has internet)"
echo "net UI       : $(curl -s localhost:$P/ | grep -oE 'id=\"mi-net\"|id=\"net-pop\"|refreshNetStatus|NET_INFO' | sort -u | tr '\n' ' ')"
echo "old chat-btn gone: $(curl -s localhost:$P/ | grep -oc 'chat-btn' || true) (expect 0)"
echo "update (n/i) : $(curl -s localhost:$P/api/apps/open-webui/update) (not pulled here -> installed:false)"
echo "update (build): $(curl -s localhost:$P/api/apps/openclaw/update)"
echo "update UI    : $(curl -s localhost:$P/ | grep -oE 'loadUpdate|id=\"upd-block\"|renderUpdate|/update' | sort -u | tr '\n' ' ')"
echo "LAUNCHER+ASSISTANT CHECK DONE"

