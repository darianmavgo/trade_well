#!/bin/bash
# ==============================================================================
# deploy_gae/deploy.sh
# Incremental App Engine deploy with the Turso (libSQL) backend: skip setup already done; only redeploy app/cron
# when sources changed since the last successful deploy.
# Project: trade-orchestrator-508722 (Project Number: 252214285524)
# Deploying REPLACES the running version of the default service. Run
# deploy_gae/setup_turso.sh and turso_check.sh first.
# Overrides:
#   FORCE_SETUP=1   re-check APIs / secrets / storage
#   FORCE_DEPLOY=1  redeploy app+cron even if hashes match
#   FORCE_DEPLOY=app  redeploy the app only (cron.yaml still deploys if it changed)
#
# Speed: the upload is limited by ../.gcloudignore to the source and sql/ (a few MB of
# changed files, not binaries/databases). Most of what is left is Google's side:
# Cloud Build queueing (~50 s) and compiling the Go app (~90 s). Each step prints
# how long it took so you can see where time goes.
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

if ! command -v gcloud >/dev/null 2>&1; then
  if [ -f "$HOME/google-cloud-sdk/bin/gcloud" ]; then
    export PATH="$HOME/google-cloud-sdk/bin:$PATH"
  fi
fi

PROJECT_ID="${GCP_PROJECT_ID:-trade-orchestrator-508722}"
PROJECT_NUMBER="${GCP_PROJECT_NUMBER:-252214285524}"
REGION="${GCP_REGION:-us-central1}"
BUCKET_NAME="${GCS_BUCKET_NAME:-trade-orchestrator-data-trade-orchestrator-508722}"
AE_SERVICE_ACCOUNT="${PROJECT_ID}@appspot.gserviceaccount.com"
COMPUTE_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"

FORCE_SETUP="${FORCE_SETUP:-0}"
FORCE_DEPLOY="${FORCE_DEPLOY:-0}"

# App Engine locationId is often "us-central"; Cloud Scheduler uses "us-central1".
# gcloud app deploy does NOT accept --location (despite the Cloud Scheduler warning).
ae_scheduler_location() {
  local loc
  loc="$(gcloud app describe --project="$PROJECT_ID" --format='value(locationId)' 2>/dev/null || true)"
  case "$loc" in
    us-central) echo "us-central1" ;;
    europe-west) echo "europe-west1" ;;
    us-east1|us-east4|us-west1|us-west2|us-west3|us-west4|europe-west1|europe-west2|europe-west3|asia-northeast1|asia-east1|australia-southeast1) echo "$loc" ;;
    "") echo "${REGION:-us-central1}" ;;
    *) echo "$loc" ;;
  esac
}

# Strip the known-harmless Scheduler warning that tells you to pass --location
# to `gcloud app deploy` (that flag does not exist on this command).
run_app_deploy_quiet_location_warn() {
  local err rc=0
  err="$(mktemp)"
  if gcloud app deploy "$@" 2>"$err"; then
    grep -v -F 'We are using the App Engine app location' "$err" >&2 || true
  else
    rc=$?
    cat "$err" >&2
  fi
  rm -f "$err"
  return "$rc"
}


CACHE_DIR="$ROOT_DIR/.deploy_cache/gae_turso"
mkdir -p "$CACHE_DIR"
STATE_FILE="$CACHE_DIR/last_deploy.env"

hash_paths() {
  local paths=("$@")
  local existing=()
  local p
  for p in "${paths[@]}"; do
    if [ -e "$ROOT_DIR/$p" ]; then
      existing+=("$ROOT_DIR/$p")
    fi
  done
  if [ ${#existing[@]} -eq 0 ]; then
    echo "missing"
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    find "${existing[@]}" \( -type f -o -type l \) -print0 2>/dev/null \
      | sort -z | xargs -0 shasum -a 256 2>/dev/null | shasum -a 256 | awk '{print $1}'
  else
    find "${existing[@]}" \( -type f -o -type l \) -print0 2>/dev/null \
      | sort -z | xargs -0 sha256sum 2>/dev/null | sha256sum | awk '{print $1}'
  fi
}

state_get() {
  local key="$1"
  if [ -f "$STATE_FILE" ]; then
    grep "^${key}=" "$STATE_FILE" 2>/dev/null | head -1 | cut -d= -f2- || true
  fi
}

state_set() {
  local key="$1" val="$2" tmp
  tmp="$(mktemp)"
  if [ -f "$STATE_FILE" ]; then
    grep -v "^${key}=" "$STATE_FILE" > "$tmp" || true
  fi
  echo "${key}=${val}" >> "$tmp"
  mv "$tmp" "$STATE_FILE"
}

# post_deploy_ready asks the deployed app's /ready page whether it can do tonight's work, and
# prints every check that is not ok. It returns 1 only when the allowlist check is bad. The dashboard
# credentials come from the config object and never reach the command line or the output.
post_deploy_ready() {
  local url="$1" user pass body
  local cfg
  cfg="$(gcloud storage cat "gs://${BUCKET_NAME}/config/env" --project="$PROJECT_ID" 2>/dev/null || true)"
  user="$(printf '%s\n' "$cfg" | sed -n 's/^DASHBOARD_USERNAME=//p' | head -1)"
  pass="$(printf '%s\n' "$cfg" | sed -n 's/^DASHBOARD_PASSWORD=//p' | head -1)"
  if [ -z "$user" ] || [ -z "$pass" ] || ! command -v python3 >/dev/null 2>&1; then
    echo "   • post-deploy /ready check skipped (no dashboard credentials in the config object, or no python3)"
    return 0
  fi
  user="$(printf '%s' "$user" | sed 's/[\\"]/\\&/g')"
  pass="$(printf '%s' "$pass" | sed 's/[\\"]/\\&/g')"
  echo "   • post-deploy check: ${url}/ready (a new instance can take a minute to start)"
  local i
  for i in 1 2 3 4 5 6; do
    body="$(printf 'user = "%s:%s"\n' "$user" "$pass" | curl -sS -K - --max-time 90 "${url}/ready?format=json" 2>/dev/null || true)"
    if printf '%s' "$body" | python3 -c 'import json,sys; json.load(sys.stdin)' >/dev/null 2>&1; then break; fi
    body=""; sleep 10
  done
  if [ -z "$body" ]; then
    echo "   ⚠️  /ready did not answer; check ${url}/ready by hand" >&2
    return 0
  fi
  printf '%s' "$body" | python3 -c '
import json, sys
r = json.load(sys.stdin)
print("   • /ready status: " + r["status"].upper())
for c in r["checks"]:
    if c["status"] != "ok":
        print("     %-4s %-15s %s" % (c["status"].upper(), c["name"], c["detail"]))
# Only the allowlist check can be broken by the deploy itself. Anything else that is bad (a scan
# that failed yesterday, an expiring token) is shown above and left for you to act on.
bad_allow = any(c["name"] == "allowlist" and c["status"] == "bad" for c in r["checks"])
sys.exit(1 if bad_allow else 0)'
}

api_enabled() {
  local api="$1"
  gcloud services list --enabled --project="$PROJECT_ID" \
    --filter="config.name:${api}" --format="value(config.name)" 2>/dev/null | grep -q .
}

bucket_has_object_admin() {
  local member="$1"
  gcloud storage buckets get-iam-policy "gs://${BUCKET_NAME}" --project="$PROJECT_ID" \
    --flatten="bindings[].members" \
    --filter="bindings.role:roles/storage.objectAdmin AND bindings.members:${member}" \
    --format="value(bindings.members)" 2>/dev/null | grep -q .
}

echo "================================================================================"
echo " INCREMENTAL App Engine deploy (Turso) — trade_orchestrator"
echo " Project: $PROJECT_ID"
echo "================================================================================"

gcloud config set project "$PROJECT_ID" --quiet >/dev/null

# --- 1. APIs -----------------------------------------------------------------
REQUIRED_APIS=(
  appengine.googleapis.com
  cloudbuild.googleapis.com
  storage.googleapis.com
  iam.googleapis.com
)
API_STATE_FILE="$CACHE_DIR/apis_enabled"
NEED_APIS=0
if [ "$FORCE_SETUP" = "1" ] || [ ! -f "$API_STATE_FILE" ]; then
  for api in "${REQUIRED_APIS[@]}"; do
    if ! api_enabled "$api"; then
      NEED_APIS=1
      break
    fi
  done
  if [ "$NEED_APIS" = "1" ]; then
    echo "[1/5] Enabling missing APIs..."
    gcloud services enable "${REQUIRED_APIS[@]}" \
      appengineflex.googleapis.com cloudscheduler.googleapis.com iamcredentials.googleapis.com \
      --project="$PROJECT_ID" >/dev/null
  else
    echo "[1/5] APIs already enabled. Skipping."
  fi
  touch "$API_STATE_FILE"
else
  echo "[1/5] APIs already enabled (cached). Skipping."
fi

# --- 2. App Engine app -------------------------------------------------------
if gcloud app describe --project="$PROJECT_ID" >/dev/null 2>&1; then
  LOC="$(gcloud app describe --project="$PROJECT_ID" --format='value(locationId)')"
  echo "[2/5] App Engine app already exists ($LOC). Skipping create."
else
  echo "[2/5] Creating App Engine app in $REGION..."
  gcloud app create --region="$REGION" --project="$PROJECT_ID"
fi

# --- 3. Config object -------------------------------------------------------
# The app reads its whole configuration (credentials and settings) from
# gs://<bucket>/config/env at startup. Secret Manager, app.yaml env and .env are not used.
if ! gcloud storage ls "gs://${BUCKET_NAME}/config/env" --project="$PROJECT_ID" >/dev/null 2>&1; then
  echo "❌ gs://${BUCKET_NAME}/config/env does not exist: the deployed app would not start." >&2
  echo "   Create it with: deploy_gae/config.sh migrate, then deploy_gae/setup_turso.sh" >&2
  exit 1
fi
echo "[3/5] Config object gs://${BUCKET_NAME}/config/env present ($(gcloud storage cat "gs://${BUCKET_NAME}/config/env" --project="$PROJECT_ID" | grep -c '=') line(s))."

# Turso gate: the object must name the database and token, and the
# database must answer. SKIP_TURSO_CHECK=1 skips the live query (keys are still required).
KEYS="$(gcloud storage cat "gs://${BUCKET_NAME}/config/env" --project="$PROJECT_ID" | sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p')"
for k in TURSO_DATABASE_URL TURSO_AUTH_TOKEN; do
  if ! printf '%s\n' "$KEYS" | grep -qx "$k"; then
    echo "❌ $k is not in the config object. Run deploy_gae/setup_turso.sh" >&2
    exit 1
  fi
done
if [ "${SKIP_TURSO_CHECK:-0}" != "1" ]; then
  "$SCRIPT_DIR/turso_check.sh" || exit 1
fi

# --- 4. GCS bucket / IAM -----------------------------------------------------
STORAGE_STATE_FILE="$CACHE_DIR/storage_ready"
NEED_STORAGE=0
if [ "$FORCE_SETUP" = "1" ] || [ ! -f "$STORAGE_STATE_FILE" ]; then
  if ! gcloud storage buckets describe "gs://${BUCKET_NAME}" --project="$PROJECT_ID" >/dev/null 2>&1; then
    NEED_STORAGE=1
  elif ! bucket_has_object_admin "serviceAccount:${AE_SERVICE_ACCOUNT}"; then
    NEED_STORAGE=1
  fi
  if [ "$NEED_STORAGE" = "1" ]; then
    echo "[4/5] Storage incomplete — running setup_storage.sh..."
    "$ROOT_DIR/deploy_gae/setup_storage.sh"
  else
    echo "[4/5] GCS bucket + IAM already in place. Skipping."
  fi
  touch "$STORAGE_STATE_FILE"
else
  echo "[4/5] Storage already configured (cached). Skipping."
fi

# --- 5. Deploy only what changed --------------------------------------------
APP_HASH=$(hash_paths \
  cmd/orchestrator \
  pkg \
  deploy_gae/app.yaml \
  go.mod go.sum \
  sql)
CRON_HASH=$(hash_paths deploy_gae/cron.yaml)

LAST_APP="$(state_get APP_HASH)"
LAST_CRON="$(state_get CRON_HASH)"

DEPLOY_APP=0
DEPLOY_CRON=0
if [ "$FORCE_DEPLOY" = "1" ]; then
  DEPLOY_APP=1
  DEPLOY_CRON=1
  echo "[5/5] FORCE_DEPLOY=1 — redeploying app + cron."
elif [ "$FORCE_DEPLOY" = "app" ]; then
  DEPLOY_APP=1
  if [ ! -f "$STATE_FILE" ] || [ "$CRON_HASH" != "$LAST_CRON" ]; then
    DEPLOY_CRON=1
  fi
  echo "[5/5] FORCE_DEPLOY=app — redeploying the app (cron only if cron.yaml changed)."
else
  if [ ! -f "$STATE_FILE" ] || [ "$APP_HASH" != "$LAST_APP" ]; then
    DEPLOY_APP=1
  fi
  if [ ! -f "$STATE_FILE" ] || [ "$CRON_HASH" != "$LAST_CRON" ]; then
    DEPLOY_CRON=1
  fi
fi

if [ "$DEPLOY_APP" = "0" ] && [ "$DEPLOY_CRON" = "0" ]; then
  echo "[5/5] No app/cron source changes since last deploy. Skipping gcloud app deploy."
  URL="$(gcloud app browse --project="$PROJECT_ID" --uri-only 2>/dev/null || true)"
  if [ -z "$URL" ]; then
    HN="$(gcloud app describe --project="$PROJECT_ID" --format='value(defaultHostname)' 2>/dev/null || true)"
    URL="https://${HN}"
  fi
  echo ""
  echo "✅ Up to date (nothing to deploy)."
  echo "   URL:    $URL"
  echo "   Force:  FORCE_DEPLOY=1 $SCRIPT_DIR/deploy.sh"
  exit 0
fi

if [ "$DEPLOY_APP" = "1" ]; then
  UPLOAD_FILES="$(cd "$ROOT_DIR" && gcloud meta list-files-for-upload 2>/dev/null | wc -l | tr -d ' ')"
  echo "   • upload set: ${UPLOAD_FILES} files (see .gcloudignore)"

  # Before anything is uploaded: does the strategy bundle compiled into THIS source resolve every
  # entry of the live STRATEGY_ALLOWLIST? A row missing from pkg/orchestrator/strategies/deploy/strategies.db
  # stops every job on App Engine, so refuse to deploy it. (SKIP_READY_CHECK=1 skips this.)
  if [ "${SKIP_READY_CHECK:-0}" != "1" ]; then
    echo "   • checking the bundle against the live STRATEGY_ALLOWLIST (orchestrator ready --static)..."
    if ! (cd "$ROOT_DIR" && go run ./cmd/orchestrator ready --static); then
      echo "❌ The bundle does not resolve the live allowlist. Nothing was deployed." >&2
      echo "   Fix: scripts/export_strategies.sh \"<allowlist>\", then deploy again." >&2
      exit 1
    fi
  fi
fi

cd "$ROOT_DIR"

if [ "$DEPLOY_CRON" = "1" ]; then
  T_CRON=$SECONDS
  SCHED_LOC="$(ae_scheduler_location)"
  echo "[5/5] cron.yaml changed — deploying cron (scheduler location $SCHED_LOC)..."
  # Note: --location is NOT a valid flag on `gcloud app deploy` (SDK 585+);
  # Cloud Scheduler still emits a warning naming that flag. We filter it.
  run_app_deploy_quiet_location_warn "$ROOT_DIR/deploy_gae/cron.yaml" \
    --project="$PROJECT_ID" \
    --quiet
  state_set CRON_HASH "$CRON_HASH"
  echo "   • cron deploy took $((SECONDS - T_CRON))s"
else
  echo "[5/5] cron.yaml unchanged. Skipping cron deploy."
fi

if [ "$DEPLOY_APP" = "1" ]; then
  T_APP=$SECONDS
  echo "[5/5] App sources changed — deploying App Engine service..."
  # The source and sql/ are uploaded; Cloud Build downloads the public Go modules itself.
  gcloud app deploy \
    --appyaml="$SCRIPT_DIR/app.yaml" \
    --project="$PROJECT_ID" \
    --promote \
    --stop-previous-version \
    --quiet
  state_set APP_HASH "$APP_HASH"
  echo "   • app deploy took $((SECONDS - T_APP))s (Cloud Build compile is most of this)"
else
  echo "[5/5] App sources unchanged. Skipping service deploy."
fi


URL="$(gcloud app browse --project="$PROJECT_ID" --uri-only 2>/dev/null || true)"
if [ -z "$URL" ]; then
  HN="$(gcloud app describe --project="$PROJECT_ID" --format='value(defaultHostname)' 2>/dev/null || true)"
  URL="https://${HN}"
fi
echo ""
echo "✅ Deploy finished in ${SECONDS}s."
echo "   URL:     $URL"
echo "   Pages:   $URL/ops  $URL/ready  $URL/scan  $URL/config"
if [ "$DEPLOY_APP" = "1" ] && [ "${SKIP_READY_CHECK:-0}" != "1" ]; then
  if ! post_deploy_ready "${URL%/}"; then
    echo "❌ Deployed, but the new version cannot resolve the live STRATEGY_ALLOWLIST (see above). Open ${URL%/}/ready." >&2
    exit 1
  fi
fi
echo "   Logs:    gcloud app logs tail -s default --project=$PROJECT_ID"
echo "   Manage:  $SCRIPT_DIR/manage_gae.sh status"
echo "   Force:   FORCE_SETUP=1 and/or FORCE_DEPLOY=1 $SCRIPT_DIR/deploy.sh"
