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

echo "================================================================================"
echo " Bootstrap App Engine project (billing → APIs → storage → config object → deploy)"
echo "================================================================================"

"$SCRIPT_DIR/setup_billing.sh"
"$SCRIPT_DIR/enable_apis_service_accounts.sh"
"$SCRIPT_DIR/setup_storage.sh"
"$SCRIPT_DIR/config.sh" migrate   # the app reads gs://<bucket>/config/env; no Secret Manager
"$SCRIPT_DIR/deploy.sh"

echo ""
echo "✅ App Engine bootstrap finished."
