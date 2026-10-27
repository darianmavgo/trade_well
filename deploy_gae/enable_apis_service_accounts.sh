#!/bin/bash
# Enable App Engine–related APIs and ensure the App Engine app exists.
# Skips APIs already enabled and skips create when the app already exists.
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
AE_SERVICE_ACCOUNT="${PROJECT_ID}@appspot.gserviceaccount.com"
COMPUTE_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"

gcloud config set project "$PROJECT_ID" --quiet >/dev/null

echo "================================================================================"
echo " Enabling APIs + verifying App Engine (skip if already done)"
echo "================================================================================"

APIS=(
  appengine.googleapis.com
  appengineflex.googleapis.com
  cloudbuild.googleapis.com
  storage.googleapis.com
  cloudscheduler.googleapis.com
  iam.googleapis.com
  iamcredentials.googleapis.com
)

MISSING=()
for api in "${APIS[@]}"; do
  if gcloud services list --enabled --project="$PROJECT_ID" \
      --filter="config.name:${api}" --format="value(config.name)" 2>/dev/null | grep -q .; then
    echo "   • $api already enabled"
  else
    MISSING+=("$api")
  fi
done

if [ ${#MISSING[@]} -gt 0 ]; then
  echo "[1/3] Enabling ${#MISSING[@]} missing API(s)..."
  gcloud services enable "${MISSING[@]}" --project="$PROJECT_ID"
else
  echo "[1/3] All required APIs already enabled. Skipping."
fi

echo "[2/3] App Engine application..."
if gcloud app describe --project="$PROJECT_ID" >/dev/null 2>&1; then
  echo "   • Already exists: $(gcloud app describe --project="$PROJECT_ID" --format='value(locationId)')"
else
  echo "   • Creating in $REGION..."
  gcloud app create --region="$REGION" --project="$PROJECT_ID"
fi

echo "[3/3] Service accounts..."
if gcloud iam service-accounts describe "$AE_SERVICE_ACCOUNT" --project="$PROJECT_ID" >/dev/null 2>&1; then
  echo "   • Found App Engine SA: $AE_SERVICE_ACCOUNT"
else
  echo "   ⚠️  App Engine SA not visible yet (sometimes lags create)."
fi
if gcloud iam service-accounts describe "$COMPUTE_SA" --project="$PROJECT_ID" >/dev/null 2>&1; then
  echo "   • Found Compute SA: $COMPUTE_SA"
else
  echo "   ⚠️  Compute SA missing — enable compute.googleapis.com and retry."
fi

echo ""
echo "✅ APIs / App Engine application ready."
