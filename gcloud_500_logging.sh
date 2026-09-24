gcloud logging read 'resource.type="gae_app" AND (httpRequest.status >= 400 OR severity >= ERROR)' \
  --project=trade-orchestrator-508722 \
  --limit=20 \
  --format="table(timestamp, severity, httpRequest.status:label=STATUS, httpRequest.requestMethod:label=METHOD, httpRequest.requestUrl:label=URL, textPayload)"
