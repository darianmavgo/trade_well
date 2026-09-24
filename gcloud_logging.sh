gcloud logging read 'resource.type="gae_app" AND (protoPayload.status >= 400 OR severity >= ERROR)' \
  --project=trade-orchestrator-508722 \
  --limit=50 \
  --format="table(timestamp, severity, protoPayload.status, textPayload)"
