#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMMIT=0123456789abcdef0123456789abcdef01234567
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

(
  cd "$ROOT/orchestrator"
  go run ./cmd/cloudless-trust-inventory -source-commit "$COMMIT"
) > "$work/one.json"
(
  cd "$ROOT/orchestrator"
  go run ./cmd/cloudless-trust-inventory -source-commit "$COMMIT"
) > "$work/two.json"
cmp -s "$work/one.json" "$work/two.json" || {
  echo "Trust inventory is not deterministic" >&2
  exit 1
}

python3 - "$work/one.json" "$COMMIT" <<'PY'
import json, re, sys
path, commit = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    doc = json.load(handle)
if doc.get("schema") != "cloudless.trust-inventory.v1" or doc.get("sourceCommit") != commit:
    raise SystemExit("trust inventory identity is invalid")
if doc.get("archiveKeyFingerprint") != "745BF7A97F64EB716DAF7677974145C2D867C99E":
    raise SystemExit("trust inventory uses the wrong archive identity")
recipes = doc.get("reviewedRecipes")
expected = {"deepseek-v4-flash-dspark-2x", "deepseek-v4-flash-dual-dspark-1m"}
if not isinstance(recipes, list) or {item.get("recipeId") for item in recipes} != expected:
    raise SystemExit("reviewed recipe inventory is incomplete")
profiles = doc.get("distributedCompatibility")
if not isinstance(profiles, list) or not profiles:
    raise SystemExit("distributed compatibility inventory is empty")
digest = re.compile(r"^sha256:[0-9a-f]{64}$")
for item in recipes + profiles:
    if not digest.match(str(item.get("metadataDigest", ""))):
        raise SystemExit("trust inventory contains an invalid metadata digest")
PY

echo "Reviewed recipe and compatibility trust inventory is deterministic and complete."
