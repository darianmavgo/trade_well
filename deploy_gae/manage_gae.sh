#!/bin/bash
# Project: trade-orchestrator-508722 (Project Number: 252214285524)
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
# App Engine default service account (runtime)
AE_SERVICE_ACCOUNT="${PROJECT_ID}@appspot.gserviceaccount.com"
# Compute default SA (Cloud Build / shared secrets)
COMPUTE_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"

gcloud config set project "$PROJECT_ID" --quiet >/dev/null

CMD="${1:-status}"

case "$CMD" in
  status)
    echo "=== App Engine ==="
    gcloud app describe --project="$PROJECT_ID" || true
    echo ""
    echo "=== Versions ==="
    gcloud app versions list --project="$PROJECT_ID" --service=default || true
    echo ""
    echo "=== Cron ==="
    gcloud app cron-jobs list --project="$PROJECT_ID" 2>/dev/null \
      || gcloud cron jobs list --project="$PROJECT_ID" 2>/dev/null \
      || echo "(cron list via console: App Engine → Cron jobs)"
    ;;
  logs)
    gcloud app logs tail -s default --project="$PROJECT_ID"
    ;;
  open)
    gcloud app browse --project="$PROJECT_ID"
    ;;
  cron)
    echo "Configured cron (from cron.yaml):"
    cat "$SCRIPT_DIR/cron.yaml"
    ;;
  turso)
    "/turso_check.sh"
    ;;
  versions)
    gcloud app versions list --project="$PROJECT_ID"
    ;;
  *)
    echo "Usage: Usage: $0 {status|logs|open|cron|versions} {status|logs|open|cron|versions|turso}"
    exit 1
    ;;
esac
