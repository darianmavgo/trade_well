#!/usr/bin/env bash
# setup_client_portal.sh: install/configure/start IBKR's Client Portal Gateway. The logic is
# Go (pkg/gateway), shared with `ibkr gateway ...`; this only builds the binary and calls it.
# Every step checks first and is skipped when already done.
#
#   ./setup_client_portal.sh [--dir PATH] [--port N] [--stop|--status|--login]
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE" && go build -o bin/ibkr ./cmd/ibkr
DIR=""; PORT=""; ACTION="setup"
while [ $# -gt 0 ]; do
  case "$1" in
    --dir) DIR="${2:?}"; shift 2 ;;
    --port) PORT="${2:?}"; shift 2 ;;
    --stop) ACTION=stop; shift ;;
    --status) ACTION=status; shift ;;
    --login) ACTION=login; shift ;;
    -h|--help) awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
ARGS=("$ACTION")
[ "$ACTION" = setup ] && { [ -z "$DIR" ] || ARGS+=(-dir "$DIR"); [ -z "$PORT" ] || ARGS+=(-port "$PORT"); }
exec ./bin/ibkr gateway "${ARGS[@]}"
