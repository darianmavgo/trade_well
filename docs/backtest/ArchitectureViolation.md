# Architecture violations: calculation done in Go

Rule 1 (`../CLAUDE.md`): Go controls execution, SQL does the calculation, and every stage persists in a slice table. This file lists what a scan of `pkg/` and `cmd/` found that calculates in Go, or whose result never lands in SQLite. Nothing here has been changed. Line numbers are from the scan date, 2026-10-04.

Not every file was read in full. Entries for `etf_study.go`, `sp500_lead_lag.go`, `market_clustering.go`, `march_april_voo_gld_uten.go`, `market_context/`, `universe` and `scoreboard` come from loop and math counts, not a line-by-line read; confirm before acting on them.

Severity: **High** = a signal, feature or model input is computed in Go. **Medium** = a result metric or study is computed in Go and only the output is stored. **Low** = date or ledger arithmetic, or logic placed in `cmd/`.

## Strategies

All 65,600 strategies get their signals from SQL: the five row families run one shared pipeline each (`sql/strategies/streak_strategy`, `tree_strategy`, `markov_model`, `hold_strategy`) and the handful in `pkg/backtest/strategy` run `every_bar`, `annual_winner`, `price_action_reclaim` and `voo_up3`. No `GenerateSignals` loops over bars to decide an entry. Exceptions:

| Sev | Strategy / code | Violation | Fix |
|---|---|---|---|
| Medium | `*-covered-call` ids (`dividend_covered_call.go`) | Return no signals; the whole overlay (strike choice, premium, assignment, roll) is simulated in Go (`pkg/backtest/options/coveredcall.go:85` `SimulateCoveredCall`, `dates.go`, `dividends.go`). The result is not captured in slice tables. | Move strike selection, premium and monthly roll into `sql/` stages writing a covered-call ledger table. |
| Low | `<dir>-sql` ids (`sql_strategy.go:500-536`) | Duplicate registry entries for a folder that already has an owner. They exist only because the code once had a Go-versus-SQL split. | Remove `AutoRegisterSQLStrategies`' `-sql` ids once nothing resolves a directory through them. |

## Training (model fitting)

| Sev | Code | Violation | Fix |
|---|---|---|---|
| High | `pkg/backtest/train/tree.go:154` `fitTree`, `:117` `downsampleNeutral` | Tree growing with CloudForest and the neutral-class downsample/weighting run in Go. The features and labels do come from SQL (`sql/stages/tree_features`), and the tree is stored one row per node, but the split search and class balancing are Go. | Class balancing (`downsampleNeutral`) belongs in a SQL stage. The split search has no SQL equivalent yet; keep it, but record it as the one accepted exception. |
| Low | `pkg/backtest/train/markov.go` | Training itself is SQL (`sql/stages/markov_train`). Clean. | none |

## Studies (`pkg/backtest/study`, 6,055 lines)

Rule 1 says not to add to `pkg/backtest/study/*` and to move calculation into SQL when touched. None have been moved.

| Sev | File | Violation |
|---|---|---|
| High | `mara_decision_tree.go:177` `computeMARASamples` (~1,145 lines); `mu_decision_tree.go:195` `computeMUSamples` (~1,194 lines) | Per-bar features (RSI at about line 223, SMA distances, returns) and a decision tree built in Go. These duplicate `sql/stages/tree_features` and the `train tree` path. Candidates for deletion rather than migration. |
| High | `hmm_regime.go` (413 lines) | A standalone study that fits an HMM in Go. No strategy reads its `hmm_regime.db` output since the `markov_hmm` pipeline was removed. |
| High | `gain_5pct_frequency.go:273` `computeTickerStats` (~1,300 lines) | Gain frequency, returns and annualised volatility (`:379-380`) computed per ticker in a Go loop. |
| Medium | `qqq_tqqq_volume.go:92,120,352,363` | Log volume changes, mean and median in Go. |
| Medium | `stats.go:246,369` | Incomplete beta and correlation in Go (a library for the test statistics). SQLite has no such function; accept as a Go helper, but feed it from SQL. |
| Medium | `etf_study.go`, `sp500_lead_lag.go`, `market_clustering.go`, `march_april_voo_gld_uten.go`, `pkg/backtest/study/market_context/` | Returns, lead/lag and clustering inputs built in Go from loaded bars. |

## Metrics and simulation

| Sev | Code | Violation | Fix |
|---|---|---|---|
| Medium | `pkg/backtest/analytics/metrics.go:11-266` (`CalculatePerformanceMetrics*`, `applyIdleDays`) | CAGR (`:170`), Sharpe (`:203`), Sortino (`:208`), Ulcer index (`:157`), drawdown and win/loss are computed from the equity curve and trades row by row in Go. The report is saved afterwards. | Compute from `equity_curve` and `trades` with SQL window functions into `performance_summary`. `sql/validation/` already does this for `check_overfit`. |
| Medium | `pkg/backtest/walk_forward/run.go:107` `simulate`, `:126` `normalizeBars` | Runs the Go simulator per fold and normalises bars in Go. Fold dates (`folds.go:22`) are Go date math. Fold and summary results are stored; `check_overfit` itself is SQL (clean). | Generate the fold calendar in SQL; keep the simulator call. |
| Medium | `pkg/backtest/gridsearch/sweep.go:235-379` | Each grid point is simulated in Go, then ranked by `sort.Slice` on Calmar (`:337`) and resilience. Entries are SQL (`streak_slice`, `streak_entry`); only the ranking and exits are Go. | Rank from the `gridsearch_results` table with `ORDER BY` and `LIMIT`. |
| Low | `pkg/backtest/strateval/split.go:20,58` `SplitDates` | In/out-of-sample date split in Go. Ledger writes are SQL. | One SQL query over distinct bar dates. |
| Low | `pkg/backtest/strateval/eval.go:155` `optimizeOnIS` | Parameter selection over trials in Go. | Select from the stored trial table. |

## Other

| Sev | Code | Violation | Fix |
|---|---|---|---|
| Low | `pkg/backtest/universe/universe.go` | Classification of leverage, direction and category is Go string matching over Polygon results. | Store the raw Polygon rows and classify with a SQL `CASE` stage. |
| Low | `pkg/backtest/scoreboard/main.go` | Ranking in Go over result DBs. | Same. |

## Clean

`check_overfit` (verdict logic is `sql/validation/check_overfit.sql`), `train markov`, `tree_strategy` and `markov_strategy` signal generation, `streak_strategy`, `hold_strategy` and the `gridsearch` streak entry build (all stage-per-file slice tables), `bar_sma`, `stratlist`, `refdb`.

## Not strategies, not flagged

The Strategy `Name()` and `Description()` text, config validation (`ValidateRow`), file and path handling, and `runner` batching are control flow, not calculation.

## Suggested order

1. Delete or migrate the MARA/MU decision-tree studies (about 2,300 lines of duplicate feature code).
2. Move `analytics` metrics to SQL and remove the duplicate CAGR in the simulator.
3. HMM regime labels to SQL.
4. Covered-call overlay to SQL.
