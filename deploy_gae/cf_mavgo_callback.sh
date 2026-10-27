#!/usr/bin/env bash
# Deploy (or refresh) the Cloudflare Worker that keeps the still-live Schwab
# redirect_uri working while a Developer Portal callback change is pending:
#
#   https://mavgo.com/schwabcallback*  --302-->
#   https://trade-orchestrator-508722.uc.r.appspot.com/auth/schwab/callback
#   (preserves query: code, state, …)
#
# Requires: wrangler logged in (`wrangler whoami`), zone mavgo.com on this account.
#
# Usage (from repo root or this directory):
#   ./deploy_gae/cf_mavgo_callback.sh           # write worker files + deploy + verify
#   ./deploy_gae/cf_mavgo_callback.sh verify    # verify only
#   ./deploy_gae/cf_mavgo_callback.sh undeploy  # remove worker + routes
#   ./deploy_gae/cf_mavgo_callback.sh write     # sync worker files only
#
# Env overrides (optional):
#   CF_ZONE_NAME  CF_CALLBACK_PATH  GAE_CALLBACK_TARGET  CF_WORKER_NAME  CF_WORKER_DIR
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

ZONE_NAME="${CF_ZONE_NAME:-mavgo.com}"
CALLBACK_PATH="${CF_CALLBACK_PATH:-/schwabcallback}"
GAE_TARGET="${GAE_CALLBACK_TARGET:-https://trade-orchestrator-508722.uc.r.appspot.com/auth/schwab/callback}"
WORKER_NAME="${CF_WORKER_NAME:-mavgo-schwab-callback-forward}"
DEFAULT_WORKER_DIR="$(cd "$REPO_ROOT/.." && pwd)/mavgo-schwab-callback-forward"
WORKER_DIR="${CF_WORKER_DIR:-$DEFAULT_WORKER_DIR}"

cmd="${1:-deploy}"

need_wrangler() {
  if ! command -v wrangler >/dev/null 2>&1; then
    echo "wrangler not found on PATH. Install Cloudflare Wrangler, then: wrangler login" >&2
    exit 1
  fi
}

write_worker_files() {
  mkdir -p "$WORKER_DIR/src"

  cat > "$WORKER_DIR/package.json" <<EOF
{
  "name": "$WORKER_NAME",
  "private": true,
  "version": "1.0.0",
  "main": "src/index.js"
}
EOF

  cat > "$WORKER_DIR/wrangler.toml" <<EOF
name = "$WORKER_NAME"
main = "src/index.js"
compatibility_date = "2024-09-01"

# Route only the OAuth callback path (does not affect the rest of the site).
routes = [
  { pattern = "$ZONE_NAME${CALLBACK_PATH}", zone_name = "$ZONE_NAME" },
  { pattern = "$ZONE_NAME${CALLBACK_PATH}*", zone_name = "$ZONE_NAME" },
]
EOF

  python3 - "$WORKER_DIR/src/index.js" "$GAE_TARGET" "$ZONE_NAME" "$CALLBACK_PATH" <<'PYINNER'
import json, sys
out, target, zone, cb = sys.argv[1:5]
open(out, "w").write(
    f"""/**
 * Forward Schwab OAuth callback from the still-live registered redirect_uri
 *   https://{zone}{cb}
 * to GAE:
 *   {target}
 * Preserves the full query string (code, state, etc.).
 */
const TARGET = {json.dumps(target)};

export default {{
  async fetch(request) {{
    const url = new URL(request.url);
    const path = url.pathname.replace(/\\/$/, "") || "/";
    if (path === {json.dumps(cb)}) {{
      const dest = new URL(TARGET);
      dest.search = url.search;
      return Response.redirect(dest.toString(), 302);
    }}
    return new Response("Not found", {{ status: 404 }});
  }},
}};
"""
)
print(f"Wrote {out}")
PYINNER

  echo "Worker project: $WORKER_DIR"
}

verify_forward() {
  local url="https://${ZONE_NAME}${CALLBACK_PATH}?code=TESTCODE&state=TESTSTATE"
  local expect="${GAE_TARGET}?code=TESTCODE&state=TESTSTATE"
  echo "GET $url"
  local headers
  headers="$(curl -sI "$url" | tr -d '\r')"
  echo "$headers" | grep -iE '^(HTTP/|location:)' || true
  local loc
  loc="$(echo "$headers" | awk 'tolower($1)=="location:"{print $2; exit}')"
  if [[ "$loc" == "$expect" ]]; then
    echo "OK: Location matches expected forward"
    return 0
  fi
  echo "FAIL: expected Location: $expect" >&2
  echo "      got:      Location: ${loc:-<none>}" >&2
  return 1
}

deploy_worker() {
  need_wrangler
  write_worker_files
  echo "Deploying $WORKER_NAME …"
  (cd "$WORKER_DIR" && wrangler deploy)
  echo
  verify_forward
  echo
  echo "Reminder: GAE must use SCHWAB_CALLBACK_URL=https://${ZONE_NAME}${CALLBACK_PATH}"
  echo "  (see deploy_gae/app.yaml). Deploy GAE yourself when ready:"
  echo "  FORCE_DEPLOY=1 ./deploy_gae/deploy.sh"
}

undeploy_worker() {
  need_wrangler
  if [[ ! -f "$WORKER_DIR/wrangler.toml" ]]; then
    write_worker_files
  fi
  echo "Deleting worker $WORKER_NAME (and routes) …"
  (cd "$WORKER_DIR" && wrangler delete --force) || true
  echo "Done. Check mavgo no longer redirects:"
  curl -sI "https://${ZONE_NAME}${CALLBACK_PATH}" | tr -d '\r' | grep -iE '^(HTTP/|location:)' || true
}

case "$cmd" in
  deploy)
    deploy_worker
    ;;
  verify)
    verify_forward
    ;;
  undeploy|delete)
    undeploy_worker
    ;;
  write|sync)
    write_worker_files
    ;;
  *)
    echo "Usage: $0 [deploy|verify|undeploy|write]" >&2
    exit 2
    ;;
esac
