#!/usr/bin/env bash
# Rebuild pkg/orchestrator/strategies/deploy/ (strategies.db, markov_models.db, tree_models.db, hmm_models.db) for an
# allowlist, using `cmd/strategy export` (pkg/backtest) on its own strategies.db and models. Works for every row family
# (streak, markov, tree, hold, rotation). Run it whenever the allowlist gains a strategy that is a row,
# then rebuild, and set the same value with deploy_gae/config.sh before deploying.
#
#   scripts/export_strategies.sh                      # allowlist seeded in deploy_gae/config.sh
#   scripts/export_strategies.sh "a+b+c,markov_model_muu"
#
# EXPORT_ALLOC (default 0.20) is the share of equity per position, the -alloc the backtest ran
# with (`backtest stack -alloc 0.20`). Every exported row is sized at it and the bundle records
# it, so live trades the allocation that was backtested and `ready` can check it.
#   EXPORT_ALLOC=0.25 scripts/export_strategies.sh
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
bt=""                          # code that runs `strategy export`
data_root="$bt"                            # and the strategies.db and trained models it reads
allow="${1:-$(sed -n 's/.*set_kv "\$WORK" STRATEGY_ALLOWLIST "\(.*\)"$/\1/p' "$here/deploy_gae/config.sh")}"
[ -n "$allow" ] || { echo "no allowlist given and none found in deploy_gae/config.sh" >&2; exit 1; }
# `strategy export` rejects an id that is not a row of a strategy table, so keep only the
# entries made of rows.
ref="$data_root/refdata/strategies.db"
rows=()
for entry in ${allow//,/ }; do
  ok=1
  for id in ${entry//+/ }; do
    n=$(sqlite3 "$ref" "SELECT (SELECT count(*) FROM streak_strategy WHERE id='$id')+(SELECT count(*) FROM markov_strategy WHERE id='$id')+(SELECT count(*) FROM hmm_strategy WHERE id='$id')+(SELECT count(*) FROM tree_strategy WHERE id='$id')+(SELECT count(*) FROM hold_strategy WHERE id='$id')+(SELECT count(*) FROM rotation_strategy WHERE id='$id')+(SELECT count(*) FROM price_action_reclaim_strategy WHERE id='$id')")
    [ "$n" -gt 0 ] || ok=0
  done
  if [ "$ok" = 1 ]; then rows+=("$entry"); else echo "not a row, left out of the bundle: $entry"; fi
done
rows_csv=$(IFS=,; echo "${rows[*]}")
echo "exporting: $rows_csv"
cd "$bt"
APP_FOLDER="$data_root" go run ./cmd/strategy export -ids "$rows_csv" \
  -out "/pkg/orchestrator/strategies/deploy/strategies.db" \
  -models-out "/pkg/orchestrator/strategies/deploy" \
  -alloc "${EXPORT_ALLOC:-0.20}"

# The QQQ hidden Markov model is not a strategy row: copy the whole (small) model database. Train
# it first with `train hmm` in backtestgosqlite. The deploy keeps the copy it had if there is none.
if [ -s "$data_root/data/hmm_models.db" ]; then
  APP_FOLDER="$data_root" go run ./cmd/train hmm export -out "/pkg/orchestrator/strategies/deploy/hmm_models.db"
else
  echo "no $data_root/data/hmm_models.db: hmm_models.db in the bundle is unchanged (run: train hmm)"
fi
