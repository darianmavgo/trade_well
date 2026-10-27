#!/bin/bash
# ==============================================================================
# deploy_gae/turso_check.sh
# Read-only: reads TURSO_DATABASE_URL and TURSO_AUTH_TOKEN from the config object
# (gs://<bucket>/config/env), runs `SELECT 1` over Turso's HTTP API and prints the
# table names. Writes nothing. The token is passed to curl on stdin, never on the
# command line, and is never printed.
# ==============================================================================
set -euo pipefail

if ! command -v gcloud >/dev/null 2>&1 && [ -f "$HOME/google-cloud-sdk/bin/gcloud" ]; then
  export PATH="$HOME/google-cloud-sdk/bin:$PATH"
fi
PROJECT_ID="${GCP_PROJECT_ID:-trade-orchestrator-508722}"
BUCKET_NAME="${GCS_BUCKET_NAME:-trade-orchestrator-data-$PROJECT_ID}"

cfg="$(gcloud storage cat "gs://${BUCKET_NAME}/config/env" --project="$PROJECT_ID" 2>/dev/null || true)"
url="$(printf '%s\n' "$cfg" | sed -n 's/^TURSO_DATABASE_URL=//p' | head -1)"
tok="$(printf '%s\n' "$cfg" | sed -n 's/^TURSO_AUTH_TOKEN=//p' | head -1)"
if [ -z "$url" ] || [ -z "$tok" ]; then
  echo "❌ TURSO_DATABASE_URL / TURSO_AUTH_TOKEN are not both in gs://${BUCKET_NAME}/config/env" >&2
  echo "   Set them with: deploy_gae/setup_turso.sh" >&2
  exit 1
fi
host="${url#libsql://}"; host="${host#https://}"; host="${host%%/*}"

query() { # SQL -> JSON on stdout
  printf 'header = "Authorization: Bearer %s"\n' "$tok" | curl -sS -K - --max-time 30 \
    -H 'Content-Type: application/json' \
    -d "{\"requests\":[{\"type\":\"execute\",\"stmt\":{\"sql\":\"$1\"}},{\"type\":\"close\"}]}" \
    "https://${host}/v2/pipeline"
}

if ! out="$(query 'SELECT 1')" || ! printf '%s' "$out" | grep -q '"type":"ok"'; then
  echo "❌ Turso at ${host} did not answer SELECT 1 (bad URL or token?)" >&2
  exit 1
fi
echo "✅ Turso reachable: ${host}"
if command -v python3 >/dev/null 2>&1; then
  query "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name" | python3 -c '
import json, sys
r = json.load(sys.stdin)["results"][0]["response"]["result"]
names = [row[0]["value"] for row in r["rows"]]
print("   %d table(s): %s" % (len(names), ", ".join(names) or "(none; the database is empty)"))'
fi
