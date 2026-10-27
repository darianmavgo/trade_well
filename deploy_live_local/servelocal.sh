#!/usr/bin/env bash
# =============================================================================
# servelocal.sh: run `orchestrator serve` on this machine exactly as App
# Engine runs it, against the LIVE config object, the LIVE Schwab token and the
# LIVE Turso database, with air hot reload. Only the HTTP endpoint
# (http://localhost:PORT) is local.
#
#   deploy_live_local/servelocal.sh [--port N] [--no-reload] [--fresh] [--dry-run]
#
#   config    gs://<bucket>/config/env, read by the app itself at startup (edit it with
#             deploy_gae/config.sh). It carries TURSO_DATABASE_URL and
#             TURSO_AUTH_TOKEN, so the app talks to the same Turso database as GAE.
#             Nothing from your shell or a .env is passed in: a variable already in the
#             environment beats the object and would silently override a live value.
#   runtime   the GAE_* / GOOGLE_CLOUD_PROJECT / PORT variables App Engine injects, so
#             runtimeenv.Detect() reports GAE and the app uses Turso, not a local file.
#   token     gs://<bucket>/schwab-token/shared.json is copied to SCHWAB_TOKEN_PATH when
#             missing; then the app's own tokensync pulls newer and pushes refreshes. A
#             refresh here updates the token GAE uses.
#   database  Turso. There is no replica to restore and nothing to push back: every read
#             and write goes to the live database, from here and from GAE alike. Only the
#             working folder (bundled models, market file, token) is local, under APP_FOLDER.
#   --fresh   delete that working folder first; the app places the bundled models again.
#   --dry-run print the plan (variable names only, no values); run nothing.
#
# BUILD   deploy_live_local/build.sh: `go mod vendor` when stale, then
#         `GOFLAGS=-mod=vendor go build ./cmd/orchestrator` (GAE's build).
# RELOAD  air rebuilds on changes in this repo and (through a watcher) in ../schwaber and
#         ../backtestgosqlite. --no-reload builds once and runs the binary.
#
# NOT the same as GAE: no HTTPS in front; no scheduler (trigger a job with
# ORCHESTRATOR_URL=http://localhost:8082 bin/orchestrator cron <job>); the Schwab OAuth
# callback stays https://mavgo.com/schwabcallback; your gcloud credentials (not the GAE
# service account) authorise GCS.
#
# WARNING: live Schwab credentials and the live database. /cron/submit_orders here
# places real orders and writes job runs to the live Turso database.
# Needs `gcloud auth application-default login`.
# =============================================================================

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$HERE/.." && pwd)"
BUILD="$REPO_DIR/deploy_live_local/build.sh"
APP_YAML="$HERE/app.yaml"
RUN_APP="$HERE/.run_app.sh"

PORT="${PORT:-8082}"
FRESH=0; DRY=0; RELOAD=1
while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="${2:?--port needs a value}"; shift 2 ;;
    [0-9]*) PORT="$1"; shift ;;
    --no-reload) RELOAD=0; shift ;;
    --fresh) FRESH=1; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
done

[ -f "$APP_YAML" ] || { echo "missing $APP_YAML" >&2; exit 1; }
[ -x "$BUILD" ] || { echo "missing $BUILD" >&2; exit 1; }

# --- 1. The live configuration object (names are printed, values never) -------
RUNTIME="$(awk '/^runtime:/ {print $2}' "$APP_YAML")" # e.g. go126
PROJECT="${GCP_PROJECT_ID:-trade-orchestrator-508722}"
BUCKET="${GCS_BUCKET:-trade-orchestrator-data-$PROJECT}"
if ! command -v gcloud >/dev/null && [ -x "$HOME/google-cloud-sdk/bin/gcloud" ]; then
  export PATH="$HOME/google-cloud-sdk/bin:$PATH"
fi
CONFIG_OBJ="gs://$BUCKET/config/env"
CONFIG="$(gcloud storage cat "$CONFIG_OBJ" --project="$PROJECT" 2>/dev/null || true)"
[ -n "$CONFIG" ] || { echo "cannot read $CONFIG_OBJ: run deploy_gae/config.sh and gcloud auth login" >&2; exit 1; }
get_cfg() { # value of one variable in the config object
  printf '%s\n' "$CONFIG" | awk -F= -v k="$1" '{ key=$1; gsub(/[[:space:]]/, "", key) } key==k { v=substr($0, index($0, "=")+1); gsub(/^[[:space:]]+|[[:space:]]+$/, "", v); gsub(/^["\x27]|["\x27]$/, "", v); print v }' | tail -n1
}
CONFIG_KEYS="$(printf '%s\n' "$CONFIG" | awk -F= '/^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=/ {gsub(/[[:space:]]/, "", $1); print $1}' | sort -u | tr '\n' ' ')"
APP_FOLDER="$(get_cfg APP_FOLDER)"
[ -n "$APP_FOLDER" ] || { echo "APP_FOLDER is missing in $CONFIG_OBJ" >&2; exit 1; }
TURSO_URL="$(get_cfg TURSO_DATABASE_URL)"
[ -n "$TURSO_URL" ] && [ -n "$(get_cfg TURSO_AUTH_TOKEN)" ] || {
  echo "TURSO_DATABASE_URL / TURSO_AUTH_TOKEN are missing in $CONFIG_OBJ: run deploy_gae/setup_turso.sh" >&2; exit 1; }
TOKEN_PATH="$(get_cfg SCHWAB_TOKEN_PATH)"
case "$APP_FOLDER" in
  /tmp/?*) ;;
  *) if [ "$FRESH" -eq 1 ]; then echo "refusing --fresh: APP_FOLDER '$APP_FOLDER' is not under /tmp" >&2; exit 1; fi ;;
esac

# --- 2. The environment the GAE runtime adds ---------------------------------
GAE_ENV_VARS=(
  "GAE_ENV=standard"
  "GAE_SERVICE=default"
  "GAE_VERSION=local-$(date +%Y%m%dt%H%M%S)"
  "GAE_INSTANCE=local-$(hostname -s)"
  "GAE_RUNTIME=$RUNTIME"
  "GAE_APPLICATION=e~$PROJECT"
  "GOOGLE_CLOUD_PROJECT=$PROJECT"
  "PORT=$PORT"
)

echo "servelocal: project=$PROJECT  runtime=$RUNTIME  port=$PORT  reload=$([ "$RELOAD" -eq 1 ] && echo air || echo off)"
echo "  database : LIVE Turso ${TURSO_URL} (from $CONFIG_OBJ)"
echo "  folder   : $APP_FOLDER (bundled models, market file, token; local only)"
echo "  config   : live $CONFIG_OBJ (the app loads it at startup)"
echo "  token    : live gs://$BUCKET/schwab-token/shared.json -> $TOKEN_PATH (then the app's tokensync)"
echo "  config variables : $CONFIG_KEYS"
echo "  GAE runtime vars : $(printf '%s ' "${GAE_ENV_VARS[@]%%=*}")"
echo "  not passed: your shell environment (it would override live values)"

if [ "$DRY" -eq 1 ]; then
  [ "$FRESH" -eq 1 ] && echo "[dry run] would delete $APP_FOLDER"
  echo "[dry run] would: build.sh (go mod vendor if stale; GOFLAGS=-mod=vendor go build ./cmd/orchestrator)"
  echo "[dry run] would write $RUN_APP and start $([ "$RELOAD" -eq 1 ] && echo "air -c deploy_live_local/air.toml + a sibling-repo watcher" || echo "it directly")"
  exit 0
fi

# --- 3. Preconditions ----------------------------------------------------------
command -v go >/dev/null || { echo "go not found" >&2; exit 1; }
WANT="1.${RUNTIME#go1}"   # go126 -> 1.26
go version | grep -q "go${WANT}" || \
  echo "WARNING: app.yaml runtime is $RUNTIME but local $(go version | awk '{print $3}'); GAE builds with Go $WANT" >&2
gcloud auth application-default print-access-token >/dev/null 2>&1 || {
  echo "no Application Default Credentials: run  gcloud auth application-default login  (GCS needs them)" >&2
  exit 1
}

AIR_BIN=""
if [ "$RELOAD" -eq 1 ]; then
  AIR_BIN="$(command -v air || true)"
  if [ -z "$AIR_BIN" ]; then
    GOBIN_DIR="$(go env GOPATH)/bin"
    if [ ! -x "$GOBIN_DIR/air" ]; then
      echo "air not found; installing github.com/air-verse/air..."
      go install github.com/air-verse/air@latest
    fi
    AIR_BIN="$GOBIN_DIR/air"
  fi
fi

# --- 4. Build (vendors when needed) --------------------------------------------
"$BUILD"
BIN="$REPO_DIR/bin/orchestrator"

# --- 5. Working folder and live token ------------------------------------------
if [ "$FRESH" -eq 1 ] && [ -d "$APP_FOLDER" ]; then
  echo "--fresh: removing $APP_FOLDER (the local working folder)"
  rm -rf "$APP_FOLDER"
fi
mkdir -p "$APP_FOLDER"
TOKEN_OBJ="gs://$BUCKET/schwab-token/shared.json"
if [ -n "$TOKEN_PATH" ] && [ ! -s "$TOKEN_PATH" ]; then
  echo "live token: copying $TOKEN_OBJ -> $TOKEN_PATH"
  mkdir -p "$(dirname "$TOKEN_PATH")"
  gcloud storage cp "$TOKEN_OBJ" "$TOKEN_PATH" >/dev/null
  chmod 600 "$TOKEN_PATH"
fi
gcloud storage ls -l "$TOKEN_OBJ" 2>/dev/null | awk 'NR==1 {print "live token object last updated: " $2}' || true

# --- 6. The process GAE would run: empty directory, scrubbed environment ------
PASS=(HOME="$HOME" PATH="$PATH")
for v in GOOGLE_APPLICATION_CREDENTIALS CLOUDSDK_CONFIG HTTPS_PROXY HTTP_PROXY NO_PROXY https_proxy http_proxy no_proxy SSL_CERT_FILE; do
  [ -n "${!v:-}" ] && PASS+=("$v=${!v}")
done
{
  echo '#!/usr/bin/env bash'
  echo '# Generated by servelocal.sh. Do not commit.'
  echo 'set -euo pipefail'
  printf 'cd %q\n' "$APP_FOLDER"
  printf 'exec env -i'
  for kv in "${PASS[@]}" "${GAE_ENV_VARS[@]}"; do
    printf ' %q' "$kv"
  done
  printf ' %q serve -port %q\n' "$BIN" "$PORT"
} > "$RUN_APP"
chmod 700 "$RUN_APP"

echo
echo "Serving http://localhost:$PORT  (dashboard login = DASHBOARD_USERNAME / DASHBOARD_PASSWORD from the live config)"
echo "Ctrl-C stops it; the app shuts down cleanly on SIGINT/SIGTERM."

if [ "$RELOAD" -eq 0 ]; then
  exec "$RUN_APP"
fi

# --- 7. Hot reload: air for this repo, a watcher for the sibling repos --------
STAMP="$HERE/.sibling-stamp"
TRIGGER="$HERE/.sibling-changed"
touch "$STAMP" "$TRIGGER"
(
  while sleep 2; do
    if "$BUILD" --changed-since "$STAMP"; then
      touch "$STAMP"   # air reacts to writes, not touch, so also write the trigger
      date +%s > "$TRIGGER"
    fi
  done
) &
WATCHER=$!

cd "$REPO_DIR"
"$AIR_BIN" -c "$HERE/air.toml" &
AIR=$!
stop_all() { kill -INT "$AIR" 2>/dev/null || true; kill "$WATCHER" 2>/dev/null || true; }
trap stop_all INT TERM
trap 'kill "$WATCHER" 2>/dev/null || true' EXIT
wait "$AIR" || true
wait "$AIR" 2>/dev/null || true
