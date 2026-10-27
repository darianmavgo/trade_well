#!/bin/bash
# Project: trade-orchestrator-508722 (Project Number: 252214285524)
# Shared GCS bucket IAM. Skips bindings already present.
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

gcloud config set project "$PROJECT_ID" --quiet >/dev/null

echo "================================================================================"
echo " GCS bucket for the config object and Schwab token"
echo "================================================================================"

if gcloud services list --enabled --project="$PROJECT_ID" \
    --filter="config.name:storage.googleapis.com" --format="value(config.name)" 2>/dev/null | grep -q .; then
  echo "   • storage.googleapis.com already enabled"
else
  gcloud services enable storage.googleapis.com --project="$PROJECT_ID"
fi

if gcloud storage buckets describe "gs://$BUCKET_NAME" --project="$PROJECT_ID" >/dev/null 2>&1; then
  echo "   • Bucket gs://$BUCKET_NAME already exists."
else
  echo "   • Creating gs://$BUCKET_NAME in $REGION..."
  gcloud storage buckets create "gs://$BUCKET_NAME" \
    --project="$PROJECT_ID" \
    --location="$REGION" \
    --default-storage-class=STANDARD \
    --uniform-bucket-level-access
fi

has_object_admin() {
  local member="$1"
  gcloud storage buckets get-iam-policy "gs://${BUCKET_NAME}" --project="$PROJECT_ID" \
    --flatten="bindings[].members" \
    --filter="bindings.role:roles/storage.objectAdmin AND bindings.members:${member}" \
    --format="value(bindings.members)" 2>/dev/null | grep -q .
}

for SA in "$AE_SERVICE_ACCOUNT" "$COMPUTE_SA"; do
  if has_object_admin "serviceAccount:${SA}"; then
    echo "   • storage.objectAdmin already on $SA — skip"
    continue
  fi
  echo "   • Granting storage.objectAdmin to $SA"
  gcloud storage buckets add-iam-policy-binding "gs://$BUCKET_NAME" \
    --member="serviceAccount:${SA}" \
    --role="roles/storage.objectAdmin" \
    --project="$PROJECT_ID" \
    --quiet >/dev/null
done

echo "✅ Storage ready: gs://$BUCKET_NAME"
