#!/usr/bin/env bash
# =============================================================================
# servelocal.sh: run the live-vs-backtest-report service locally the way App Engine
# runs it. Live config, live Turso database, air hot reload. The HTTP endpoint
# is local (http://localhost:PORT, default 8081).
#
#   deploy_report_local/servelocal.sh [--port N] [--folder DIR] [--no-reload]
#                                     [--fresh] [--dry-run]
#
# The binary starts with no arguments. App Engine's entrypoint is `main` with
# no arguments; main calls live_vs_backtest_report.Serve when GAE_SERVICE is the
# service name in deploy_gae/live_vs_backtest_report.yaml (live-vs-backtest-report).
#
# LIVE CONFIG
#   Everything else the app is configured with is the single object
#   gs://<bucket>/config/env, read by the app at startup, exactly as on App
#   Engine. Edit it with deploy_gae/config.sh. A variable already in the
#   process environment beats the object, so the scrubbed environment passes
#   only the GAE runtime variables and the report data folder.
#
# DATA FOLDER
#   This process sets APP_FOLDER to /tmp/trade_orchestrator_report (or
#   --folder). That wins over the config object's APP_FOLDER, which is the
#   trading copy. The operational database is the live Turso database named
#   in the config object (orchestrator.db is only the label of the file under
#   test). The folder holds the market file and bundled models, which the app
#   places there when missing.
#   --fresh deletes that report folder first. The folder must be its own
#   directory under /tmp, and it must not overlap the config APP_FOLDER.
#
# READ ONLY
#   The report page reads the database and writes nothing to it. The trading
#   service remains the writer.
#
# BUILD  deploy_live_local/build.sh: go build ./cmd/orchestrator (GAE's build).
# RELOAD air rebuilds and restarts on changes under this repo.
#        --no-reload builds once and runs the binary.
#
# The report page does not call Schwab, so this script does not copy the
# Schwab token. Sign in with DASHBOARD_USERNAME / DASHBOARD_PASSWORD from the
# live config. The page caches for two minutes; ?refresh=1 rebuilds it.
#
# NOT the same as GAE: no nginx/HTTPS in front; your gcloud credentials (not
# the GAE service account) authorise GCS.
# Needs `gcloud auth application-default login`.
# =============================================================================

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$HERE/.." && pwd)"
APP_YAML="$HERE/app.yaml"
SERVICE_YAML="$REPO_DIR/deploy_gae/live_vs_backtest_report.yaml"
RUN_APP="$HERE/.run_app.sh"
BUILD="$REPO_DIR/deploy_live_local/build.sh"

PORT="${PORT:-8081}"
FRESH=0; DRY=0; RELOAD=1
REPORT_FOLDER="${REPORT_FOLDER:-/tmp/trade_orchestrator_report}"
while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="${2:?--port needs a value}"; shift 2 ;;
    [0-9]*) PORT="$1"; shift ;; # old form: servelocal.sh 8081
    --folder) REPORT_FOLDER="${2:?--folder needs a value}"; shift 2 ;;
    --no-reload) RELOAD=0; shift ;;
    --fresh) FRESH=1; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
done

[ -f "$APP_YAML" ] || { echo "missing $APP_YAML" >&2; exit 1; }
[ -f "$SERVICE_YAML" ] || { echo "missing $SERVICE_YAML" >&2; exit 1; }
[ -f "$BUILD" ] || { echo "missing $BUILD" >&2; exit 1; }

# --- 1. The live configuration object (names are printed, values never) -------
RUNTIME="$(awk '/^runtime:/ {print $2; exit}' "$APP_YAML")" # e.g. go126
SERVICE="$(awk '/^service:/ {print $2; exit}' "$SERVICE_YAML")"
[ -n "$SERVICE" ] || { echo "missing service: in $SERVICE_YAML" >&2; exit 1; }
PROJECT="${GCP_PROJECT_ID:-trade-orchestrator-508722}"
BUCKET="${GCS_BUCKET:-trade-orchestrator-data-$PROJECT}"
if ! command -v gcloud >/dev/null && [ -x "$HOME/google-cloud-sdk/bin/gcloud" ]; then
  export PATH="$HOME/google-cloud-sdk/bin:$PATH"
fi
CONFIG_OBJ="gs://$BUCKET/config/env"
CONFIG="$(gcloud storage cat "$CONFIG_OBJ" --project="$PROJECT" 2>/dev/null || true)"
[ -n "$CONFIG" ] || { echo "cannot read $CONFIG_OBJ: run deploy_gae/config.sh migrate (and gcloud auth login)" >&2; exit 1; }
get_cfg() { # value of one variable in the config object
  printf '%s\n' "$CONFIG" | awk -F= -v k="$1" '{ key=$1; gsub(/[[:space:]]/, "", key) } key==k { v=substr($0, index($0, "=")+1); gsub(/^[[:space:]]+|[[:space:]]+$/, "", v); gsub(/^["\x27]|["\x27]$/, "", v); print v }' | tail -n1
}
CONFIG_KEYS="$(printf '%s\n' "$CONFIG" | awk -F= '/^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=/ {gsub(/[[:space:]]/, "", $1); print $1}' | sort -u | tr '\n' ' ')"
CONFIG_APP_FOLDER="$(get_cfg APP_FOLDER)"
[ -n "$CONFIG_APP_FOLDER" ] || { echo "APP_FOLDER is missing in $CONFIG_OBJ" >&2; exit 1; }
MARKET_CFG="$(get_cfg MARKET_DB)"
MARKET_NAME="market_history.db"
if [ -n "$MARKET_CFG" ]; then
  MARKET_NAME="$(basename "$MARKET_CFG")"
fi
PRIMARY_NAME="orchestrator.db"

# --- 2. Own data folder, under /tmp, clear of the trading copy ----------------
REPORT_FOLDER="${REPORT_FOLDER%/}"
CONFIG_APP_FOLDER="${CONFIG_APP_FOLDER%/}"
case "$REPORT_FOLDER" in
  /tmp/?*) ;;
  *) echo "refusing: report data folder must be a directory under /tmp (got $REPORT_FOLDER)" >&2; exit 1 ;;
esac
case "$REPORT_FOLDER" in
  "$CONFIG_APP_FOLDER"|"$CONFIG_APP_FOLDER"/*)
    echo "refusing: report data folder $REPORT_FOLDER overlaps the trading folder $CONFIG_APP_FOLDER" >&2
    exit 1 ;;
esac
case "$CONFIG_APP_FOLDER" in
  "$REPORT_FOLDER"/*)
    echo "refusing: report data folder $REPORT_FOLDER overlaps the trading folder $CONFIG_APP_FOLDER" >&2
    exit 1 ;;
esac
PRIMARY_PATH="$REPORT_FOLDER/$PRIMARY_NAME"
MARKET_PATH="$REPORT_FOLDER/$MARKET_NAME"

# --- 3. The environment the GAE runtime adds, plus the report folder ----------
GAE_ENV_VARS=(
  "GAE_ENV=standard"
  "GAE_SERVICE=$SERVICE"
  "GAE_VERSION=local-$(date +%Y%m%dt%H%M%S)"
  "GAE_INSTANCE=local-$(hostname -s)"
  "GAE_RUNTIME=$RUNTIME"
  "GAE_APPLICATION=e~$PROJECT"
  "GOOGLE_CLOUD_PROJECT=$PROJECT"
  "PORT=$PORT"
)
REPORT_ENV=(
  "APP_FOLDER=$REPORT_FOLDER"
  "ORCHESTRATOR_DB=$PRIMARY_PATH"
  "SCHWAB_DB_PATH=$PRIMARY_PATH"
  "MARKET_DB=$MARKET_PATH"
)

echo "report-local: project=$PROJECT  service=$SERVICE  runtime=$RUNTIME  port=$PORT  reload=$([ "$RELOAD" -eq 1 ] && echo air || echo off)"
echo "  data     : $REPORT_FOLDER"
echo "  database : $PRIMARY_PATH"
echo "  market   : $MARKET_PATH"
echo "  turso    : live database from $CONFIG_OBJ"
echo "  mode     : read only (the report page writes nothing)"
echo "  trading  : $CONFIG_APP_FOLDER stays in place"
echo "  config   : live $CONFIG_OBJ (the app loads it at startup)"
echo "  config variables : $CONFIG_KEYS"
echo "  process env : $(printf '%s ' "${GAE_ENV_VARS[@]%%=*}" "${REPORT_ENV[@]%%=*}")"
echo "  page     : http://localhost:$PORT  (DASHBOARD_USERNAME / DASHBOARD_PASSWORD from the live config)"

if [ "$DRY" -eq 1 ]; then
  [ "$FRESH" -eq 1 ] && echo "[dry run] would delete $REPORT_FOLDER"
  echo "[dry run] would: deploy_live_local/build.sh (go build ./cmd/orchestrator)"
  echo "[dry run] would write $RUN_APP and start $([ "$RELOAD" -eq 1 ] && echo "air -c deploy_report_local/air.toml" || echo "it directly")"
  echo "[dry run] the binary starts with no arguments"
  exit 0
fi

# --- 4. Preconditions ----------------------------------------------------------
command -v go >/dev/null || { echo "go not found" >&2; exit 1; }
WANT="1.${RUNTIME#go1}"   # go126 -> 1.26
go version | grep -q "go${WANT}" || \
  echo "WARNING: app.yaml runtime is $RUNTIME but local $(go version | awk '{print $3}'); GAE builds with Go $WANT" >&2
if ! command -v gcloud >/dev/null && [ -x "$HOME/google-cloud-sdk/bin/gcloud" ]; then
  export PATH="$HOME/google-cloud-sdk/bin:$PATH"
fi
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

# --- 5. Build --------------------------------------------
"$BUILD"
BIN="$REPO_DIR/bin/orchestrator"

# --- 6. Cold start or keep the local copy --------------------------------------
if [ "$FRESH" -eq 1 ] && [ -d "$REPORT_FOLDER" ]; then
  echo "--fresh: removing $REPORT_FOLDER; the market file and bundled models will be placed again"
  rm -rf "$REPORT_FOLDER"
fi
mkdir -p "$REPORT_FOLDER"

# --- 7. The process GAE would run: report folder, scrubbed environment --------
PASS=(HOME="$HOME" PATH="$PATH")
for v in GOOGLE_APPLICATION_CREDENTIALS CLOUDSDK_CONFIG HTTPS_PROXY HTTP_PROXY NO_PROXY https_proxy http_proxy no_proxy SSL_CERT_FILE; do
  [ -n "${!v:-}" ] && PASS+=("$v=${!v}")
done
{
  echo '#!/usr/bin/env bash'
  echo '# Generated by deploy_report_local/servelocal.sh. Do not commit.'
  echo 'set -euo pipefail'
  printf 'cd %q\n' "$REPORT_FOLDER"
  printf 'exec env -i'
  for kv in "${PASS[@]}" "${GAE_ENV_VARS[@]}" "${REPORT_ENV[@]}"; do
    printf ' %q' "$kv"
  done
  # No arguments: main routes on GAE_SERVICE. PORT is already in the environment.
  printf ' %q\n' "$BIN"
} > "$RUN_APP"
chmod 700 "$RUN_APP"

echo
echo "Serving http://localhost:$PORT  (dashboard login = DASHBOARD_USERNAME / DASHBOARD_PASSWORD from the live config)"
echo "Ctrl-C stops it; the server shuts down on SIGINT/SIGTERM."

if [ "$RELOAD" -eq 0 ]; then
  exec "$RUN_APP"
fi

# --- 8. Hot reload: air rebuilds and restarts on changes in this repo ---------
cd "$REPO_DIR"
# air runs in the background so a plain `kill` of this script reaches it too (a
# terminal Ctrl-C already signals the whole process group). air stops the app
# with SIGINT, which shuts it down cleanly.
"$AIR_BIN" -c "$HERE/air.toml" &
AIR=$!
stop_all() { kill -INT "$AIR" 2>/dev/null || true; }
trap stop_all INT TERM
wait "$AIR" || true
wait "$AIR" 2>/dev/null || true
