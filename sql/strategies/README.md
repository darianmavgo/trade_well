# Strategy pipelines

Every strategy is calculated here in SQL and run by Go. A folder under `sql/strategies/<id>/` is the signal math for the strategy with that id; Go only builds its config and runs the folder.

## Directory Structure

1. Add a family table (see `price_action_reclaim_strategy` for the newest) and a `pkg/backtest/<family>_strategy` package that builds a row's config and calls `strategy.RunPipeline`.
2. Add `sql/strategies/<family>_strategy/` with `.sql` files that run in name order (`01_schema.sql`, `02_calc_signals.sql`, …).
3. `GenerateSignals` runs that pipeline when the market and calc databases are set.

A folder that no family runs is not run.

```
sql/strategies/
├── streak_strategy/   one pipeline for every streak_strategy row
├── tree_strategy/     one pipeline for every tree_strategy row
├── markov_model/      reads the persisted Markov model (see train)
├── hmm_strategy/      reads the persisted hidden Markov model (see train hmm)
├── markov_hmm/        reads hmm_regime.db from study hmm_regime
├── hold_strategy/     one pipeline for every hold_strategy row
└── price_action_reclaim_strategy/  one pipeline for every price_action_reclaim_strategy row (one symbol each)
```

## Contract & Signal Extraction

The backtester looks for an output table containing trade triggers:
- Any table with columns: `idx, symbol, date, open, high, low, close, volume, buylimit, entry`.
- Rows where `entry = 1` are treated as entry signals and routed into the Tier 2 chronological portfolio simulator.
- Standard signal table names checked:
  - `<strategy_id>_signals`
  - `rsi_oversold_signals`
  - `entry`

## Running

```bash
./bin/backtest -strategylist
./bin/backtest -strategy streak-voo-buy-tecl -capital 100000
```
