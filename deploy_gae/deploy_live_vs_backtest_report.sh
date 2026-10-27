#!/bin/bash
# Deploy only the live-vs-backtest-report App Engine service. Does not deploy the
# default trading service and does not deploy cron.yaml.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

if ! command -v gcloud >/dev/null 2>&1; then
  if [ -f "$HOME/google-cloud-sdk/bin/gcloud" ]; then
    export PATH="$HOME/google-cloud-sdk/bin:$PATH"
  fi
fi

PROJECT_ID="${GCP_PROJECT_ID:-trade-orchestrator-508722}"

cd "$ROOT_DIR"

gcloud app deploy \
  --appyaml="$SCRIPT_DIR/live_vs_backtest_report.yaml" \
  --project="$PROJECT_ID" \
  --promote \
  --stop-previous-version \
  --quiet

echo ""
echo "Report service: https://live-vs-backtest-report-dot-${PROJECT_ID}.uc.r.appspot.com/"
echo "Same Basic Auth as the trading dashboard. Refresh the page with ?refresh=1"
echo "Logs: gcloud app logs tail -s live-vs-backtest-report --project=$PROJECT_ID"
