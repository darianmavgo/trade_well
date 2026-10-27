#!/bin/bash
# ==============================================================================
# deploy_gae/seed_turso.sh
# One-time copy of a LOCAL SQLite file (for example a restored orchestrator.db) into a
# Turso database, through the Turso CLI. Needs `turso` and `sqlite3`.
#
#   deploy_gae/seed_turso.sh <local.db> <turso-db-name> [--dry-run]
#
# It WRITES to the named Turso database (live data). --dry-run prints the dump size and
# the table list without sending anything. The source file is opened read-only. Run it
# against an empty database: existing tables of the same name make the load fail.
# ==============================================================================
set -euo pipefail

SRC="${1:?usage: seed_turso.sh <local.db> <turso-db-name> [--dry-run]}"
DB="${2:?usage: seed_turso.sh <local.db> <turso-db-name> [--dry-run]}"
DRY=0; [ "${3:-}" = "--dry-run" ] && DRY=1
[ -f "$SRC" ] || { echo "$SRC not found" >&2; exit 1; }
command -v sqlite3 >/dev/null || { echo "sqlite3 not found" >&2; exit 1; }

# A market-bars file is a cache that the scan rebuilds from Alpaca for only the allowlisted
# symbols; loading a research copy of it would spend Turso quota on irrelevant history.
if [ -n "$(sqlite3 -readonly "$SRC" "SELECT name FROM sqlite_master WHERE type='table' AND name='backtest_start'")" ]; then
  echo "refusing: $SRC has a backtest_start table (market bars). Do not load market data into Turso." >&2
  exit 1
fi

DUMP="$(mktemp)"; trap 'rm -f "$DUMP"' EXIT
sqlite3 -readonly "$SRC" .dump >"$DUMP"
for t in $(sqlite3 -readonly "$SRC" "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"); do
  echo "  $t: $(sqlite3 -readonly "$SRC" "SELECT count(*) FROM \"$t\"") row(s)"
done
echo "dump: $(wc -c <"$DUMP" | tr -d ' ') bytes, tables: $(sqlite3 -readonly "$SRC" "SELECT group_concat(name, ', ') FROM sqlite_master WHERE type='table'")"
if [ "$DRY" = "1" ]; then echo "[dry-run] nothing sent to Turso"; exit 0; fi

command -v turso >/dev/null || { echo "turso CLI not found (https://docs.turso.tech/cli)" >&2; exit 1; }
turso db shell "$DB" <"$DUMP"
echo "✅ loaded $SRC into Turso database $DB. Check: deploy_gae/turso_check.sh"
