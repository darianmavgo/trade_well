#!/bin/bash
# ==============================================================================
# deploy_gae/setup_turso.sh
# Puts the Turso connection into the config object (gs://<bucket>/config/env).
# Changes only that object (the previous copy is kept as config/env.prev). The token is read from the TURSO_AUTH_TOKEN environment variable
# or a hidden prompt; it is never put on a command line or printed.
#
#   deploy_gae/setup_turso.sh [--url libsql://<db>-<org>.turso.io] [--dry-run]
#
# Create the token with the Turso CLI, then run this:
#   turso db tokens create <db>
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
URL="${TURSO_DATABASE_URL:-libsql://tradeorchestrator-darianhickman.aws-us-east-2.turso.io}"
DRY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --url) URL="${2:?--url needs a value}"; shift 2 ;;
    --dry-run) DRY=1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
case "$URL" in libsql://*|https://*) ;; *) echo "TURSO URL must start with libsql:// or https://" >&2; exit 2 ;; esac

TOKEN="${TURSO_AUTH_TOKEN:-}"
if [ -z "$TOKEN" ]; then
  [ "$DRY" = "1" ] || { read -r -s -p "Turso auth token (hidden): " TOKEN; echo; }
fi
[ "$DRY" = "1" ] || [ -n "$TOKEN" ] || { echo "no token given" >&2; exit 1; }

if [ "$DRY" = "1" ]; then
  echo "[dry-run] would set in the config object: TURSO_DATABASE_URL=$URL, TURSO_AUTH_TOKEN=(hidden)"
  exit 0
fi

WORK="$(mktemp)"; chmod 600 "$WORK"; trap 'rm -f "$WORK"' EXIT
"$SCRIPT_DIR/config.sh" pull "$WORK" >/dev/null
set_kv() {
  local k="$1" v="$2" tmp; tmp="$(mktemp)"; chmod 600 "$tmp"
  grep -v "^[[:space:]]*$k=" "$WORK" >"$tmp" || true
  [ -s "$tmp" ] && [ -n "$(tail -c1 "$tmp")" ] && printf '\n' >>"$tmp"
  printf '%s=%s\n' "$k" "$v" >>"$tmp"
  cat "$tmp" >"$WORK"; rm -f "$tmp"
}
set_kv TURSO_DATABASE_URL "$URL"
set_kv TURSO_AUTH_TOKEN "$TOKEN"
"$SCRIPT_DIR/config.sh" push "$WORK"
echo "Set TURSO_DATABASE_URL and TURSO_AUTH_TOKEN (hidden)."
echo "Next: deploy_gae/turso_check.sh   then   deploy_gae/deploy.sh"
