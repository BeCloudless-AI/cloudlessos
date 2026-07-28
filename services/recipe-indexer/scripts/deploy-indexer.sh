#!/usr/bin/env bash
# Idempotent first-time setup and subsequent deployment for the Cloudless Recipe Indexer.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

D1_NAME=cloudless-recipes
R2_NAME=cloudless-recipes
QUEUE_NAME=cloudless-recipe-indexer
DLQ_NAME=cloudless-recipe-indexer-dead
PUBLIC_URL=https://recipes.becloudless.ai
NODE_VERSION=v24.18.0
SECRETS_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/cloudless"
SECRETS_FILE="$SECRETS_DIR/recipe-indexer.env"
DISCOVER=false
FIRST_SETUP=false
ROTATE_SECRETS=false

say() { printf '\n==> %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"; }
bootstrap_node() {
  local machine node_arch archive base temporary checksum
  machine="$(uname -m)"
  case "$machine" in
    x86_64|amd64) node_arch=x64 ;;
    aarch64|arm64) node_arch=arm64 ;;
    *) die "Automatic Node.js setup does not support architecture: $machine" ;;
  esac
  archive="node-${NODE_VERSION}-linux-${node_arch}.tar.xz"
  base="${XDG_DATA_HOME:-$HOME/.local/share}/cloudless/node-${NODE_VERSION}-linux-${node_arch}"
  if [ ! -x "$base/bin/node" ]; then
    say "Node.js is missing; installing a private ${NODE_VERSION} runtime"
    need curl
    need tar
    need sha256sum
    temporary="$(mktemp -d)"
    curl -fsSLo "$temporary/$archive" "https://nodejs.org/dist/${NODE_VERSION}/$archive"
    curl -fsSLo "$temporary/SHASUMS256.txt" "https://nodejs.org/dist/${NODE_VERSION}/SHASUMS256.txt"
    checksum="$(grep -E "[[:space:]]${archive}$" "$temporary/SHASUMS256.txt" || true)"
    [ -n "$checksum" ] || { rm -rf "$temporary"; die "Node.js published no checksum for $archive"; }
    printf '%s\n' "$checksum" > "$temporary/CHECKSUM"
    (cd "$temporary" && sha256sum -c CHECKSUM)
    install -d -m 0755 "$(dirname "$base")" "$base"
    tar -xJf "$temporary/$archive" --strip-components=1 -C "$base"
    rm -rf "$temporary"
  fi
  export PATH="$base/bin:$PATH"
}
listed_resource() {
  local value="$1" wanted="$2"
  printf '%s\n' "$value" | node -e '
    const wanted=process.argv[1]; let raw="";
    process.stdin.on("data", c => raw += c); process.stdin.on("end", () => {
      process.exit(raw.split(/[^A-Za-z0-9_.-]+/).includes(wanted) ? 0 : 1);
    });
  ' "$wanted"
}

for arg in "$@"; do
  case "$arg" in
    --discover) DISCOVER=true ;;
    --rotate-secrets) ROTATE_SECRETS=true ;;
    --help|-h)
      printf 'Usage: %s [--discover] [--rotate-secrets]\n' "$0"
      exit 0
      ;;
    *) die "Unknown option: $arg" ;;
  esac
done

need curl
NODE_MAJOR="$(node -p 'Number(process.versions.node.split(".")[0])' 2>/dev/null || printf '0')"
if [ "$NODE_MAJOR" -lt 22 ] || ! command -v npm >/dev/null 2>&1; then bootstrap_node; fi
need node
need npm
if [ "${CLOUDLESS_RUNTIME_ONLY:-0}" = "1" ]; then
  printf 'Node %s and npm %s are ready.\n' "$(node --version)" "$(npm --version)"
  exit 0
fi

say "Installing the pinned deployment tools"
npm ci

say "Testing the Recipe Indexer before touching Cloudflare"
npm test
npm run check

say "Checking Cloudflare authentication"
WHOAMI="$(npx wrangler whoami 2>&1 || true)"
if printf '%s' "$WHOAMI" | grep -qi 'not authenticated'; then
  printf '%s\n' "Wrangler will open Cloudflare once for authorization."
  npx wrangler login
else
  printf '%s\n' "$WHOAMI"
fi

say "Ensuring the D1 database exists"
D1_JSON="$(npx wrangler d1 list --json)"
D1_ID="$(printf '%s' "$D1_JSON" | node -e '
  let raw=""; process.stdin.on("data", c => raw += c); process.stdin.on("end", () => {
    const rows=JSON.parse(raw); const row=rows.find(x => x.name === "cloudless-recipes");
    if (row) process.stdout.write(row.uuid || row.id || "");
  });
')"
if [ -z "$D1_ID" ]; then
  npx wrangler d1 create "$D1_NAME" --location apac --update-config=false
  D1_JSON="$(npx wrangler d1 list --json)"
  D1_ID="$(printf '%s' "$D1_JSON" | node -e '
    let raw=""; process.stdin.on("data", c => raw += c); process.stdin.on("end", () => {
      const rows=JSON.parse(raw); const row=rows.find(x => x.name === "cloudless-recipes");
      if (row) process.stdout.write(row.uuid || row.id || "");
    });
  ')"
  FIRST_SETUP=true
fi
[ -n "$D1_ID" ] || die "Cloudflare created no visible $D1_NAME D1 database."
node scripts/set-d1-id.mjs "$D1_ID"

say "Ensuring R2 and Queue resources exist"
R2_LIST="$(npx wrangler r2 bucket list)"
if ! listed_resource "$R2_LIST" "$R2_NAME"; then
  npx wrangler r2 bucket create "$R2_NAME" --update-config=false
  FIRST_SETUP=true
fi
QUEUE_LIST="$(npx wrangler queues list)"
if ! listed_resource "$QUEUE_LIST" "$QUEUE_NAME"; then
  npx wrangler queues create "$QUEUE_NAME"
  FIRST_SETUP=true
fi
QUEUE_LIST="$(npx wrangler queues list)"
if ! listed_resource "$QUEUE_LIST" "$DLQ_NAME"; then
  npx wrangler queues create "$DLQ_NAME"
  FIRST_SETUP=true
fi

say "Ensuring signing and administration secrets exist"
install -d -m 0700 "$SECRETS_DIR"
SECRET_LIST="$(npx wrangler secret list --format json 2>/dev/null || printf '[]')"
has_secret() {
  printf '%s' "$SECRET_LIST" | node -e '
    const wanted=process.argv[1]; let raw="";
    process.stdin.on("data", c => raw += c); process.stdin.on("end", () => {
      const rows=JSON.parse(raw || "[]"); process.exit(rows.some(x => (x.name || x) === wanted) ? 0 : 1);
    });
  ' "$1"
}
dotenv_value() {
  local key="$1"
  awk -v wanted="$key" '
    index($0, "=") > 1 && substr($0, 1, index($0, "=") - 1) == wanted {
      value = substr($0, index($0, "=") + 1)
      sub(/\r$/, "", value)
      print value
      exit
    }
  ' "$SECRETS_FILE"
}
valid_github_token() {
  [[ "$1" =~ ^github_pat_[A-Za-z0-9_]+$ || "$1" =~ ^ghp_[A-Za-z0-9]+$ ]]
}
github_token_works() {
  curl -fsS --max-time 20 \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer $1" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/search/code?q=filename%3Asparkrun.yaml&per_page=1" \
    >/dev/null 2>&1
}
if $ROTATE_SECRETS; then
  rm -f "$SECRETS_FILE"
elif [ ! -s "$SECRETS_FILE" ] && has_secret CATALOG_SIGNING_PRIVATE_KEY; then
  die "Cloudflare already has signing secrets, but the local recovery file is missing. Restore $SECRETS_FILE from backup or rerun intentionally with --rotate-secrets."
fi
if [ ! -s "$SECRETS_FILE" ]; then
  npm run keys:generate --silent |
    grep -E '^(CATALOG_SIGNING_PRIVATE_KEY|CATALOG_SIGNING_PUBLIC_KEY|ADMIN_TOKEN)=' \
      > "$SECRETS_FILE"
  chmod 0600 "$SECRETS_FILE"
  FIRST_SETUP=true
  printf 'Generated the persistent local recovery copy at %s\n' "$SECRETS_FILE"
fi

CATALOG_SIGNING_PRIVATE_KEY="$(dotenv_value CATALOG_SIGNING_PRIVATE_KEY)"
CATALOG_SIGNING_PUBLIC_KEY="$(dotenv_value CATALOG_SIGNING_PUBLIC_KEY)"
ADMIN_TOKEN="$(dotenv_value ADMIN_TOKEN)"
[[ "$CATALOG_SIGNING_PRIVATE_KEY" =~ ^[A-Za-z0-9+/]+={0,2}$ ]] || die "The saved catalog private key is malformed."
[[ "$CATALOG_SIGNING_PUBLIC_KEY" =~ ^[A-Za-z0-9+/]+={0,2}$ ]] || die "The saved catalog public key is malformed."
[[ "$ADMIN_TOKEN" =~ ^[A-Za-z0-9_-]{32,}$ ]] || die "The saved administration token is malformed."

SAVED_GITHUB_TOKEN="$(dotenv_value GITHUB_TOKEN || true)"
if [ -n "$SAVED_GITHUB_TOKEN" ] && { ! valid_github_token "$SAVED_GITHUB_TOKEN" || ! github_token_works "$SAVED_GITHUB_TOKEN"; }; then
  temporary_secrets="$(mktemp)"
  grep -v '^GITHUB_TOKEN=' "$SECRETS_FILE" > "$temporary_secrets"
  install -m 0600 "$temporary_secrets" "$SECRETS_FILE"
  rm -f "$temporary_secrets"
  SAVED_GITHUB_TOKEN=""
  printf 'Removed an invalid or expired GitHub token from the local secrets file.\n' >&2
fi
if [ -z "$SAVED_GITHUB_TOKEN" ] && ! has_secret GITHUB_TOKEN; then
  GITHUB_TOKEN=""
  if command -v gh >/dev/null 2>&1; then GITHUB_TOKEN="$(gh auth token 2>/dev/null || true)"; fi
  while ! valid_github_token "$GITHUB_TOKEN" || ! github_token_works "$GITHUB_TOKEN"; do
    printf 'Paste only the GitHub token (it will stay hidden): ' >&2
    read -r -s GITHUB_TOKEN
    GITHUB_TOKEN="${GITHUB_TOKEN//$'\r'/}"
    printf '\n' >&2
    if ! valid_github_token "$GITHUB_TOKEN" || ! github_token_works "$GITHUB_TOKEN"; then
      printf 'That token was malformed, expired, or unable to use GitHub public code search. Try again.\n' >&2
      GITHUB_TOKEN=""
    fi
  done
  printf 'GITHUB_TOKEN=%s\n' "$GITHUB_TOKEN" >> "$SECRETS_FILE"
  unset GITHUB_TOKEN
  chmod 0600 "$SECRETS_FILE"
  FIRST_SETUP=true
fi

say "Migrating D1 and deploying the Worker"
npm run db:migrate:remote
npm run deploy -- --secrets-file "$SECRETS_FILE"

say "Waiting for the public endpoint"
READY=false
for _ in $(seq 1 30); do
  if HEALTH="$(curl -fsS --max-time 5 "$PUBLIC_URL/health" 2>/dev/null)"; then
    READY=true
    break
  fi
  sleep 4
done

if $READY; then
  SEQUENCE="$(printf '%s' "$HEALTH" | node -e '
    let raw=""; process.stdin.on("data", c => raw += c); process.stdin.on("end", () => {
      try { process.stdout.write(String(JSON.parse(raw).catalogSequence || 0)); } catch { process.stdout.write("0"); }
    });
  ')"
  if $DISCOVER || $FIRST_SETUP || [ "$SEQUENCE" = "0" ]; then
    say "Starting recipe discovery on Cloudflare"
    curl -fsS -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
      "$PUBLIC_URL/internal/discover"
    printf '\n'
  fi
  say "Recipe Indexer is deployed"
  printf 'Health:  %s/health\nCatalog: %s/v1/recipes\n' "$PUBLIC_URL" "$PUBLIC_URL"
else
  say "Deployment succeeded; Cloudflare is still preparing DNS/TLS"
  printf 'Run this same command again after a few minutes to verify and start discovery.\n'
fi
