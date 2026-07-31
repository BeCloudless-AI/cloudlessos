#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DOC="$ROOT/docs/BACKUP_AND_INCIDENT_RESPONSE.md"

bash "$ROOT/distro/scripts/test-backup.sh"
for required in \
  "Exposed or stolen API key" \
  "Recipe or container compromise" \
  "Update/signing concern" \
  "Spark cluster identity or peer compromise" \
  "/var/backups/cloudless"; do
  grep -Fq "$required" "$DOC" || {
    echo "Incident-response guide is missing: $required" >&2
    exit 1
  }
done
echo "Cloudless incident-response rehearsal passed."
