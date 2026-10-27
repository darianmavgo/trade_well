#!/bin/bash
# Project: trade-orchestrator-508722
# Lets YOUR Google account trigger /cron/<job> on the deployed service by hand
# (`orchestrator cron <job>` or curl with an ID token). Without this only App
# Engine's own cron can call /cron/*.
#
#   1. checks gcloud is signed in
#   2. adds your email to CRON_ALLOWED_EMAILS in the config object
#      gs://<bucket>/config/env (existing entries are kept; nothing is printed)
#   3. optionally redeploys so a fresh instance reads it   (--deploy)
#   4. optionally proves it works with a harmless dry run  (--verify)
#
# Usage:
#   deploy_gae/setup_cron_access.sh [--email you@example.com] [--deploy] [--verify] [--dry-run]
#
#   --email    address to allow (default: the account gcloud is signed in as)
#   --deploy   run deploy_gae/deploy.sh afterwards (FORCE_DEPLOY=app: the app only). The config is read
#              when an instance starts, so a running instance keeps the old value.
#   --verify   run `orchestrator cron stage_orders --dry-run` (sizes and reports, writes nothing)
#   --dry-run  show what would change; write nothing
#
# Only allow addresses you control: anyone holding a valid Google ID token for an
# allowed account can start jobs that place real orders.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

if ! command -v gcloud >/dev/null 2>&1; then
  if [ -f "$HOME/google-cloud-sdk/bin/gcloud" ]; then
    export PATH="$HOME/google-cloud-sdk/bin:$PATH"
  fi
fi

PROJECT_ID="${GCP_PROJECT_ID:-trade-orchestrator-508722}"
BUCKET="${GCS_BUCKET:-trade-orchestrator-data-$PROJECT_ID}"
CONFIG_OBJ="gs://$BUCKET/config/env"
SERVICE_URL="${ORCHESTRATOR_URL:-https://${PROJECT_ID}.uc.r.appspot.com}"

# merge_email FILE EMAIL: make FILE's CRON_ALLOWED_EMAILS line contain EMAIL,
# keeping any addresses already there. Prints "added", "present" or "created".
merge_email() {
  local file="$1" email="$2" current="" list item found=0
  if grep -q '^CRON_ALLOWED_EMAILS=' "$file"; then
    current="$(grep '^CRON_ALLOWED_EMAILS=' "$file" | tail -n1 | cut -d= -f2-)"
    current="${current%\"}"; current="${current#\"}"; current="${current%\'}"; current="${current#\'}"
    IFS=',' read -r -a list <<<"$current"
    for item in "${list[@]:-}"; do
      item="$(printf '%s' "$item" | tr -d '[:space:]')"
      if [ "$(printf '%s' "$item" | tr '[:upper:]' '[:lower:]')" = "$(printf '%s' "$email" | tr '[:upper:]' '[:lower:]')" ]; then
        found=1
      fi
    done
    if [ "$found" = 1 ]; then
      echo present
      return 0
    fi
    local merged="${current:+$current,}$email"
    local tmp; tmp="$(mktemp)"
    grep -v '^CRON_ALLOWED_EMAILS=' "$file" >"$tmp" || true
    printf 'CRON_ALLOWED_EMAILS=%s\n' "$merged" >>"$tmp"
    cat "$tmp" >"$file"; rm -f "$tmp"
    echo added
  else
    # last byte is not a newline -> add one so we do not glue onto the final line
    if [ -s "$file" ] && [ -n "$(tail -c1 "$file")" ]; then printf '\n' >>"$file"; fi
    printf 'CRON_ALLOWED_EMAILS=%s\n' "$email" >>"$file"
    echo created
  fi
}

if [ "${SETUP_CRON_ACCESS_SOURCE_ONLY:-}" = 1 ]; then
  return 0 2>/dev/null || exit 0
fi

EMAIL="" DO_DEPLOY=0 DO_VERIFY=0 DRY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --email) EMAIL="${2:?--email needs an address}"; shift 2 ;;
    --deploy) DO_DEPLOY=1; shift ;;
    --verify) DO_VERIFY=1; shift ;;
    --dry-run) DRY=1; shift ;;
    -h|--help) sed -n '2,24p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 1 ;;
  esac
done

command -v gcloud >/dev/null 2>&1 || { echo "ERROR: gcloud not found on PATH (install the Google Cloud SDK)" >&2; exit 1; }
gcloud config set project "$PROJECT_ID" --quiet >/dev/null

echo "================================================================================"
echo " Manual cron access for the deployed orchestrator ($PROJECT_ID)"
echo "================================================================================"

# 1. signed in?
ACCOUNT="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>/dev/null | head -n1)"
if [ -z "$ACCOUNT" ]; then
  echo "   • not signed in to gcloud; running: gcloud auth login"
  gcloud auth login
  ACCOUNT="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | head -n1)"
fi
[ -n "$ACCOUNT" ] || { echo "ERROR: still not signed in" >&2; exit 1; }
echo "   • gcloud account: $ACCOUNT"
if ! gcloud auth print-identity-token >/dev/null 2>&1; then
  echo "ERROR: could not mint an identity token for $ACCOUNT. Run: gcloud auth login" >&2
  exit 1
fi
echo "   • identity token: ok"

EMAIL="${EMAIL:-$ACCOUNT}"
case "$EMAIL" in
  *@*.*) ;;
  *) echo "ERROR: '$EMAIL' does not look like an email address (a service account or bare name cannot be used here)" >&2; exit 1 ;;
esac

# 2. config object
gcloud storage ls "$CONFIG_OBJ" --project="$PROJECT_ID" >/dev/null 2>&1 \
  || { echo "ERROR: $CONFIG_OBJ not found (run deploy_gae/config.sh migrate first)" >&2; exit 1; }

WORK="$(mktemp)"
chmod 600 "$WORK"
trap 'rm -f "$WORK"' EXIT
gcloud storage cat "$CONFIG_OBJ" --project="$PROJECT_ID" >"$WORK"

RESULT="$(merge_email "$WORK" "$EMAIL")"
case "$RESULT" in
  present)
    echo "   • $EMAIL is already in CRON_ALLOWED_EMAILS in $CONFIG_OBJ; unchanged"
    ;;
  added|created)
    if [ "$DRY" = 1 ]; then
      echo "   • [dry-run] would $RESULT $EMAIL in CRON_ALLOWED_EMAILS in $CONFIG_OBJ"
    else
      gcloud storage cp "$CONFIG_OBJ" "gs://$BUCKET/config/env.prev" --project="$PROJECT_ID" >/dev/null
      gcloud storage cp "$WORK" "$CONFIG_OBJ" --project="$PROJECT_ID" --content-type=text/plain >/dev/null
      echo "   • $RESULT $EMAIL in CRON_ALLOWED_EMAILS in $CONFIG_OBJ"
    fi
    ;;
esac

# 3. deploy
if [ "$DO_DEPLOY" = 1 ]; then
  if [ "$DRY" = 1 ]; then
    echo "   • [dry-run] would run: FORCE_DEPLOY=1 deploy_gae/deploy.sh"
  else
    echo "   • redeploying so a fresh instance reads the config object..."
    FORCE_DEPLOY=app "$SCRIPT_DIR/deploy.sh"
  fi
else
  echo "   • NOTE: a running instance keeps the old value. It picks the change up when an"
  echo "     instance restarts (after a redeploy, or once the idle instance has shut down)."
  echo "     Re-run with --deploy to redeploy now."
fi

# 4. client side
echo ""
echo "Client side (this machine):"
echo "   (the URL defaults to $SERVICE_URL from your gcloud project; export ORCHESTRATOR_URL to override)"
echo "   orchestrator cron list"
echo "   orchestrator cron stage_orders --dry-run"
echo "   (from the repo: go run ./cmd/orchestrator cron list)"

if [ "$DO_VERIFY" = 1 ]; then
  echo ""
  if [ "$DRY" = 1 ]; then
    echo "   • [dry-run] would run: orchestrator cron stage_orders --dry-run"
  else
    echo "   • verifying with a dry run (writes nothing)..."
    (cd "$ROOT_DIR" && ORCHESTRATOR_URL="$SERVICE_URL" go run ./cmd/orchestrator cron stage_orders --dry-run)
  fi
fi

echo ""
echo "✅ Done. To remove access later, edit CRON_ALLOWED_EMAILS with deploy_gae/config.sh edit and redeploy."
