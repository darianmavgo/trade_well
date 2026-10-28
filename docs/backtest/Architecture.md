# Architecture

Technical map of this repository. Command usage, defaults, and examples are in [README.md](README.md).

This code is `pkg/backtest/` in the `github.com/darianmavgo/trade_well` module (it was the `backtestgosqlite` repository); `cmd/`, `sql/`, `refdata/` and `data/` are at the repo root. SQLite is `modernc.org/sqlite` (no cgo). Every database `pkg/backtest/storage.OpenSQLite` opens sets `journal_mode=WAL`, `synchronous=NORMAL`, `temp_store=MEMORY`, and a 64 MB cache.

## Layout

```
cmd/<name>/main.go     thin main; calls pkg/backtest/<name>.Main
pkg/backtest/appenv             APP_FOLDER and .env
pkg/backtest/datasource         Alpaca, Polygon, Polygon options, Yahoo (daily bars), SQLite reads
pkg/backtest/market_history     gap-filling writer into the market DB
pkg/backtest/storage            bar, trade, signal, equity, performance, option schemas
pkg/backtest/refdb              strategies.db universes, ETF-tree rows, streak_strategy
pkg/backtest/models             Bar, Signal, Trade, Position, PerformanceReport
pkg/backtest/strategy           Strategy interface, strategy configs, pipeline runner
pkg/backtest/streak_strategy    one Strategy per streak_strategy row
pkg/backtest/sim      portfolio simulator: plays the signals over hourly bars
pkg/backtest/hoursim  runs a strategy or a stack through sim (config, stack, replay)
pkg/backtest/pipeline           one scoped run of every stage (scope and step state in pipeline.db)
pkg/backtest/runner             load bars, run, stack, scan, staleness
pkg/backtest/analytics          metrics and the HTML tear sheet
pkg/backtest/options            covered-call simulation
pkg/backtest/study              research studies; market_context is a subpackage
sql/strategies/<id>/   ordered .sql pipeline for a strategy
sql/studies            SQL text some studies execute
sql/validation         walk-forward summary SQL
refdata/strategies.db    streak, hold, tree and markov strategy tables; symbol tables
data/market_history.db daily and intraday bars, option chains
data/reports/          one SQLite file per run, plus HTML
```

`cmd/*` does not hold strategy logic. A command's flags, defaults, and `Run` live in the matching `pkg` so tests can call `Run` without parsing `os.Args`.

## Configuration

`pkg/backtest/appenv` searches `.env` in `.`, `..`, `../..`, the binary's directory, and that directory's parent. The first file to define a key wins. Process environment overrides the file.

| Function | Path |
|---|---|
| `Folder()` | `APP_FOLDER` or `.` |
| `Data()` | `Folder()/data` |
| `Ref()` | `Folder()/refdata` |
| `Reports()` | `Folder()/data/reports` |
| `MarketDB()` | `Data()/market_history.db` |
| `RefDB()` | `Ref()/strategies.db` |
| `ReportFile(p)` | absolute paths pass through; a leading `reports/` is stripped and the rest is joined to `Reports()` |

`cliutils.GetDefaultMarketDB` returns `MarketDB()`. `backtest`, `study`, and `livescan` use that helper. `market_history` and the batch tools call `MarketDB()` directly.

`strateval`'s ledger is `strategies.db` in the run folder, `$APP_FOLDER/data/reports/<run>/` (`-run-id`); the `-db` flag names another file. There are no environment overrides.

## Market data

`market_history.Run` opens `-db`, ensures the bar table, and upserts. With no `-symbols` it reads `SELECT DISTINCT symbol FROM <table>` in the settings DB (`leveraged_etf` by default, `-limit 50`). A `-symbols` name that is a `symbol_lists` id expands to that list's tickers (`expandSymbolLists`). Bare arguments are tickers when `-symbols` is empty: commas are separators, values are uppercased.

Coverage is per `(symbol, timeframe)`. The writer requests only the missing span unless `-force`. The default source is Alpaca and `-source polygon` uses Polygon. There is no fallback: a failed download is an error naming the source and symbol. `-source polygon-options` does not download equities; it lists call chains for one underlying and stores them in the market DB. The Polygon key is `-polygon-key` or `POLYGON_API_KEY` via `datasource.ResolvePolygonAPIKey`. Each source's HTTP body is parsed in memory into `models.Bar` and then inserted. There is no file ingest path.

Bar table (`storage.EnsureBarTable`), default name `backtest_start`:

```
idx, Date, timeframe, asset_class, open, high, low, close, "Adj Close", volume, symbol
UNIQUE (symbol, Date, timeframe)
```

`storage.FetchBars` is the daily simulator's read. It keeps `length(Date) = 10`, so `1m` timestamps never enter a daily run. It no longer computes moving averages: `Bar.SMA50` and `Bar.SMA200` are filled only for strategies that need them, by `strategy.LoadBarSMA`, which runs the `sql/stages/bar_sma` stage into a `bar_sma` slice table in that run's calc database and copies the values onto the bars. Multi-strategy runs (`backtest`, `scoreboard`, `gridsearch apply`) go through `runner.RunBatched`: bars are loaded per batch of up to 200 strategies, only for the symbols that batch declares, and a strategy that declares none runs in one final full-universe batch. `-start` is applied in Go after that window, so bars before the start still warm the averages, then drop out of the simulated dates. `FetchRecentBars` is the live-scan equivalent: same daily filter, last N bars per symbol, averages still computed on the longer series.

Option tables in the same market DB (`storage.EnsureOptionTables`):

| Table | Grain |
|---|---|
| `option_contracts` | one listed contract; `bars_fetched_at` lets a rerun skip finished contracts |
| `option_bars` | one day per `(ticker, Date)` |
| `option_expiry_scan` | one `(underlying, expiry)`, including empty chains |

## Reference DB

`pkg/backtest/refdb` opens `refdata/strategies.db` and ensures:

| Table | Contents |
|---|---|
| `etf_universe` | `(list, symbol)`. Lists: `all` (Polygon active US ETFs), `6yr`, `sweep` |
| `streak_strategy` | one runnable streak per row: watch symbol, bought symbol, direction (`drop` or `rally`), signal days, hold, take-profit and stop as fractional offsets, regime, allocation, cash yield, slippage, next-day limit. `pkg/backtest/streak_strategy.Register` loads it. `gridsearch promote` upserts winning rows |

`strategy_family_param` says, per family column, whether `gridsearch` varies it and with which values (`sql/stages/family_params`, written on every open of the file). Streak rows search signal days, hold, take-profit, stop and regime. Tree and markov rows search the exits only (hold, take-profit, stop) over the entries their own pipeline produces. Hold and hold_bail rows have nothing to search.

Older symbol tables (`leveraged_etf`, `momentum_candidates`, `backtested_win_20_10d`) are also in this file. `market_history -table` and the UI category lookup read them. `etf_universe` is empty: the command that filled it was removed. The per-ETF CloudForest `dt_*` strategies and the `etf_dt_strategies` table were deleted; the tree family (`tree_strategy` rows) is the one decision-tree family. Stock and ETF discovery now goes to `refdata/universe.db` through `universe`. Registration reads the settings tables through `APP_FOLDER` / `refdb`. A `go test` whose working directory is not the module root does not see that file unless `APP_FOLDER` is set.

## Strategies

```go
type Strategy interface {
    ID() string
    Name() string
    Description() string
    DefaultConfig() StrategyConfig
    Validate() error
    SetDatabases(marketDBPath, calcDBPath string)
    GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal
}
```

Optional interfaces, checked by the runner:

| Interface | Role |
|---|---|
| `RequiredSymbolsProvider` | symbols to load; also marks a stack candidate as eligible without `-include-universe` |
| `TotalReturnProvider` | simulate on `Adj Close` / `Close` scaled bars so dividends reinvest; `ExecuteStrategyWithDividends(..., false)` keeps raw prices and pays cash dividends |
| `OptionOverlayProvider` | no stock signals; covered call via `pkg/backtest/options` |
| `MinHistoryProvider` | trailing bars a live scan needs; default `DefaultMinHistoryBars` (250) |
| `DeclineDaysConfigurable` | `SetDeclineDays` so `gridsearch apply` can apply a swept streak length |

`Register` keys on the lowercased id. `Get` also matches with `-`, `_`, and spaces removed. Constructors call `Register` from `init`. Row-backed tables (streak, hold, tree, markov, hmm, rotation) are not registered per row: each is a `strategy.Family` (`pkg/backtest/strategy/family.go`) and `Get` falls through to it, reading and building the one row asked for. `List()` returns only the strategies with their own pipeline; `ListAll()` also builds every row.

`StrategyConfig` holds sizing (`fixed_pct`, `fixed_dollar`, `fixed_shares`, `kelly`), hold, slippage, commission, cash yield, and the two profit/stop fields. `TargetPct` / `TakeProfitPct`: the portfolio uses `TargetPct` when it is greater than 1, otherwise `TakeProfitPct`. `StopLossPct` is a price multiplier (`0.93` is −7%), not an offset. `NextDayLimitEntry` means the signal is known after the close and the order is a next-session limit at the signal price. It fills on that next bar only if the low is at or below the limit (at the limit, or at the open if the open is already through it). The booked price is the fill times `(1+SlippagePct)`. An unmet limit opens nothing. Signals saved in the result DB are the pre-simulator signals, so a dropped next-day order is still in `signals`. `EntryLimitPct` (for example `0.9`) is a buy limit on the signal's own session at that fraction of the previous session's close. It fills if that session's low reaches the limit (at the open when the open is lower) and is dropped otherwise. `SameDayExit` lets a long position be stopped out (low at or under the stop, checked first) or take its profit (high at or over the target) on the session it opened. The order of the day's touches is unknown, so this assumes the entry came before the high. The rotation family sets both for a period row with `entry_limit_pct` or `take_profit_pct`. With hourly bars in the market DB (`market_history -source polygon -timeframe 1h`, stored in UTC with `timeframe = '1h'`), a limit entry on a covered session fills on them in time order and only a later hour's high can take the profit. The fill hour counts its low but not its high, because the order inside one hour is unknown. A day inside a symbol's hourly span with no hourly bars makes no entry. A day outside it uses the daily bar. The 9:00 ET hour includes the half hour before the open, so its open is replaced by the daily open and its range is cut to the daily range. `PortfolioSimulator.Intraday` carries the bars and `runner.LoadIntraday` loads them. The shared-account simulator does not use them yet. Gridsearch sweeps a long period row over `entry_limit_pct` (1.0, 0.97, 0.95, 0.9), `take_profit_pct` (3% to 8%) and `hold_days` (1 to 5 sessions after the entry session, 0 = hold to the period end), besides `period`. A limit point measures its target from the booked fill. The sweep runs on daily bars, so its same-day exits use the optimistic daily-range rule. Check a winning point with `backtest` on hourly bars before trusting it.

There is one strategy type. Its config is built in Go (`pkg/backtest/strategy`) and its signals are calculated by the SQL in `sql/strategies/<id>/`. `AutoRegisterSQLStrategies(root, marketDB)` still inserts a `<dir>-sql` registry entry for a pipeline directory that already has an owner. That entry is the same pipeline, used to resolve the directory, not a second strategy. `failed_training/` has no matching registered strategy, so it stays unregistered. That SQL is archived; the Go package is behind `//go:build ignore` plus a `doc.go` stub. Registration is once per `(root, db)` for the process.

A pipeline is a directory of `.sql` files run in name order by `SQLPipelineStrategy`. `pipelinefs.go` reads the directory from disk: a path such as `sql/strategies/<id>` is relative to the folder that holds the sql folder (found by `sql/sqlfiles.go` from the working directory), an absolute path is used as given. There is no embedded copy and no fallback; a directory that is not there is an error naming the path. Placeholders substituted from `StrategyConfig` before execution:

| Token | Source |
|---|---|
| `__DECLINE_DAYS__` | `DeclineDays` |
| `__TAKE_PROFIT_MULT__` | take-profit multiplier |
| `__STOP_LOSS_MULT__` | stop multiplier |
| `__HOLD_DAYS__` | `HoldingWindow` |
| `__SHORT_TAKE_PROFIT_MULT__` | short-leg take-profit |
| `__SHORT_STOP_LOSS_MULT__` | short-leg stop |
| `__SHORT_HOLD_DAYS__` | short-leg hold |
| `__SYMBOL__` | `Benchmark` (one pipeline, many symbols) |

Each strategy gets its own calc DB (`calc_<id>.db`) so pipeline tables do not collide. `SetDatabases(market, calc)` is called before `GenerateSignals`.

No strategy is registered from Go any more: every one is a row of a family table. `voo-up3`, `voo_up3-sql`, `price_action_reclaim-sql` and `tsll-daily-one-share` were deleted, `price-action-reclaim` became the `price_action_reclaim` family and the dividend covered calls became rows of `hold_strategy`. `sig-voo-buy-tecl`, `sig-voo-buy-spxu`, `gld-decline` and `mara_pdd_nvdl` no longer register as Go strategies (the TECL signal is the `streak-voo-buy-tecl` row), and their orphaned SQL folders were deleted. `mara_tree`, `nvdl_tree` and `pdd_tree` are rows of `tree_strategy`; their own SQL folders were deleted because the generic `tree_strategy` pipeline gives identical signals (109, 65 and 150 on the real market DB). Most strategies are rows in the settings tables, loaded by `pkg/backtest/stratreg.RegisterAll`. `./bin/backtest -strategylist` is the live set.

Every strategy calculates its signals in SQL. `strategy.RunPipeline` runs a pipeline directory with the market database attached as `backtest_start` and the strategy's calc database for the slice tables, and gives the pipeline `__START_DATE__` and `__END_DATE__` (the window of bars the run loaded) plus the strategy's own `StrategyConfig.SQLParams`. With either database path empty it logs and returns no signals. Go only passes parameters and stamps the strategy id on the result.

| Pipeline | Strategies |
|---|---|
| `streak_strategy`, `tree_strategy`, `markov_model` | the streak, tree and markov rows (`tree_strategy` and `markov_model` read the saved model, see `train`) |
| `hmm_strategy` | the `hmm` family (`hmm-calm-qqq`, `hmm-calmdown-qqq`). Reads the model `train hmm` saved in `hmm_models.db` (`__HMM_MODEL_DB__`), takes the stressed state as the one with the highest mean volatility, and enters on the session its filtered probability is under `stress_prob` (`calm_filter`) or has just fallen back under it (`calm_down`). The simulator buys at the next session open |
| `hold_strategy` | the `hold` family and the `*-margin-buy-hold` ids: first bar, plus (when `sma_reentry_period` > 0) every bar whose close is above that SMA. `trailing_stop_pct` > 0 is the simulator's bail; 0 for both is plain buy and hold. `take_profit_pct`, `stop_loss_pct` and `hold_days` are the exits every family has (0 is not set, and 0 or 99999 hold days is no limit). `regime_symbol` set: long except on bars where that symbol's trained Markov state (`train markov`) is bear. One entry per run of non-bear bars, sold at the close of the next bear bar (`sql/seed/hold_markov.sql`). `__TOTAL_RETURN__` prices the signal on dividend-adjusted prices when the runner simulates on them (`strategy.DividendModeSetter`) |
| `rotation_strategy` | the `rotation` family. Each session it ranks the 1,000 most liquid names (60 session dollar volume known at that close) on the mean of five percentile ranks (21, 63 and 126 session return, distance above the 50 session average, distance from the 63 session high), buys the top `top_k` and sells a name once it ranks worse than `top_k + exit_buffer`. Optional QQQ-above-SMA gate. Entry = -1 closes the position. Row `symbols` lists the candidates because the runner loads bars only for named symbols. Rows are seeded by `sql/seed/rotation_strategy.sql` and name the symbol list `rotation-candidates` in `symbols`. `strategy rotation_universe` writes that list from the stage `sql/stages/rotation_universe` (the point in time liquid universe, the candidates and the equal weight top-K curves, ported from the omnifunds study) |
| `price_action_reclaim_strategy` | the `price_action_reclaim` family, one symbol per row (seeded for the unleveraged ETFs by `sql/seed/price_action_reclaim_strategy.sql`). A close that reclaims support after closing under it, in an uptrend: support is the lowest low `support_bars` to `reclaim_gap` bars back, the uptrend a close at or over its `sma_period` bar average, the stop `stop_cushion_pct` under support (skipped when more than `max_stop_pct` away), then the row's take-profit and hold. A symbol needs 250 bars in the window |
| `rotation_period` | the period rows of `rotation_strategy`, among them `biggest-winner` (long), `-short` and `-inverse`: at the start of each calendar period (`1d`, `1w`, `1m`, `1q`, `1y`) the top `top_k` names by the previous period's return, held to the period's last session (the next session for a one session period). A symbol whose first open of the period is 0 has no return and is not ranked. `pick = all` skips the ranking and enters every name of the row's list each period at `max_weight_pct` of equity each (position cap = list size), for example `rotation-2x-sector-pairs-daily-limit90` over symbol list `etf-2x-sector-pairs`. A `1d` row with `hold_days` writes no period-end exit (`__PERIOD_EXIT__ = 0`): the hold window, `take_profit_pct` and `stop_loss_pct` close the position. `cooldown = month` sets `StrategyConfig.OneFillPerMonth`: the simulator ignores a symbol's signals for the rest of the calendar month once a position in it was booked (an order with no cash or room is not a fill) |

The covered-call ids (`schd-covered-call`, `vym-covered-call`, `dvy-covered-call` and the `-5pct` ids) are rows of `hold_strategy` with `covered_call_otm_pct` set (seeded by `sql/seed/hold_covered_call.sql`). Such a row returns no stock signals: the family builds it as `hold_strategy.CoveredCall`, which exposes `OverlaySpec`, and the overlay is simulated in Go in `pkg/backtest/options`.

Still Go, and not signal generation: the simulator and its metrics (`pkg/backtest/sim`, `pkg/backtest/hoursim`, `pkg/backtest/analytics`), the dividend-adjusted price copy and dividend recovery in `pkg/backtest/runner`, the covered-call overlay, and the studies in `pkg/backtest/study`.

## Simulation




`SharedAccountSimulator` is one cash ledger. List order is priority; 0 is primary. Only the primary may preempt: it can liquidate a subordinate for cash or for the same symbol. Same-symbol eviction requires `signal.Priority == 0` and `position.Priority > 0`. Overlays never evict each other; they size to leftover cash or skip. On a cash shortfall the primary sells the default-asset lot first, then liquidates subordinates longest `HoldDays` first. Subordinates use `CalculateShares(cash, equity, …)` and skip when they cannot afford the order. Positions are keyed by symbol. Idle stats (`AvgCashPct`, `FullyIdlePct`, `DaysFullyIdle`) come from the equity curve. An idle day is `invested IS NULL OR invested = 0`.

`-default-asset` parks leftover cash in one symbol after entries each session. The park lot is not a `Positions` entry, so a sleeve can hold that same symbol beside it. Park fills use the primary's slippage and commission and are not written to `trades`, so the sleeve trade count stays comparable to a cash run. Cash dividends on the park come from steps in `Adj Close / Close` and are swept back into the park the same day. A session with no bar for the symbol stays in cash. The park counts as an open position, so idle days fall while it is held. The result goes to `stack.db` in the run folder. `shared_account_audit` stores `default_asset`, `avg_default_pct`, `default_dividends`, and `days_unparked`.

`park-<symbol>` names the same park as a stack member (`strategy.ParkStrategy`, `strategy.SplitResidual`). `Get` resolves it for any ticker-shaped symbol, `backtest` and `backtest stack` pull it out of the member list and pass its symbol as `DefaultAsset`, and `ExecuteStack` rejects a park left in the sleeve list.

Streak rows run the `streak_strategy` SQL pipeline; `gridsearch` calculates their entries once per (trade symbol, streak length, regime) in the `streak_slice` and `streak_entry` stages (`sql/stages/`) and applies take-profit, stop and hold per grid point. Decision-tree rows call `DecisionTreeSignals` in memory and are absent while `etf_dt_strategies` is empty. Markov rows call `sql/strategies/markov_model`. The Markov prediction table does not depend on the strategy row, so it is built once in a temporary calc database and reused. Each strategy deletes only its signal rows before the next insert.

Stacking is a runner concern. `runner.ExecuteStack` does not construct a new `pkg/backtest/strategy` type. Combined ids look like `sig-voo-buy-tecl+mara_tree`. The persisted file is `reports/shared_<primary>_<secondary>_….db` from `storage.CreateUniqueDB`.

`CreateUniqueDB(dir, base)` creates `<base>.db` with `O_EXCL`, then `<base>_2.db`, `<base>_3.db`, …. The base is lowercased and non-alphanumerics other than `-` and `_` become `_`. The first file is run 1. Later runs are `_N`. Readers (`runner.ScanAndValidate`) take the highest increment that still has a usable `performance_summary` / signals payload and fall back when a newer file is corrupt.

`backtest -signals-only` and `livescan` both call `runner.RunSignalScan`. The as-of date is the tip bar. The entry date is the next session. Output is `reports/livescan.db`.

## Result schema

Written by `pkg/backtest/storage` into each strategy or shared DB:

| Table | What |
|---|---|
| `signals` | pre-simulator signals: strategy, symbol, date, order type, direction, entry, take-profit, stop, regime, metadata JSON |
| `trades` | fills: entry/exit, reason, shares, PnL, hold, MAE, MFE, commission |
| `equity_curve` | `date`, `total_equity`, `cash`, `invested`, `drawdown_pct` |
| `performance_summary` | one row per strategy id, the metrics the scoreboard compiles. `idle_days` is sessions with no open position (`equity_curve.invested` null or 0). Older rows leave it NULL and scoreboard counts the curve |
| `return_breakdown` | price vs dividend split for total-return runs |
| `run_metrics` | extra labeled metrics (covered-call notes and similar) |

`reports/scoreboard.db` is a ranking compiled from those summaries. `scoreboard` deletes and rewrites it; a locked file falls through to `CreateUniqueDB(..., "scoreboard")`.

`reports/gridsearch.db`:

| Table | What |
|---|---|
| `gridsearch_runs` | one row per strategy: status `running` / `done` / `failed`, best Calmar and resilience, `data_max_date` for staleness |
| `gridsearch_results` | one row per config: label, baseline flag, CAGR, drawdown, Calmar, resilience, trades, win rate, idle days, and (added later) `symbol`, `signal_days`, `hold_days`, `take_profit_pct`, `stop_loss_pct`, `regime`, `signal_symbol`, `allocation_pct`. Older sweeps leave `idle_days` NULL |

`running` and `failed` are retried. `done` is skipped unless `-force`. `gridsearch apply` reads the best row and applies it, including `DeclineDays` when the strategy implements `DeclineDaysConfigurable`. Many older rows have NULL `hold_days`; the label is `SYM/sigDaysd/holdd/+TP%-SL%/regime` or `SYM/Hold-Nd/TP+x%/SL-y%`. Older rows also have NULL `signal_symbol` and `allocation_pct`. `gridsearch promote` fills the watch symbol from the parent strategy's `ParameterSpace` when the column is NULL.

`pkg/backtest/streak_strategy` is the `streak` family: `strategy.Get` builds a `streak_strategy` row from its `id` (`streak-<signal>-<up|down><days>-<trade>`). The package imports `pkg/backtest/strategy`, so registration is a call to `Register` from backtest, gridsearch, scoreboard, livescan, and strateval, right after `AutoRegisterSQLStrategies`. Signals come from `sql/strategies/streak_strategy/` (`__SIGNAL_SYMBOL__`, `__TRADE_SYMBOL__`, `__STREAK_COL__`, `__REGIME_PREDICATE__`). `gridsearch -strategy all` skips the `streak-` prefix unless `-include-streak`. Scoreboard and `backtest -strategy all` include the rows.

`reports/stack_eval_<primary>.db` table `overlay_rankings` is replaced on each stack (`DELETE` then insert). Columns include combined equity, CAGR, Sharpe, max drawdown, incremental equity versus the primary alone, secondary PnL and trades, preempted count, and idle-cash percents.

Stack-eval candidate filter (`runner.OverlayCandidates`), when `-secondary` is empty:

- drop the primary, duplicate `*-sql` ids, `voo-buy-hold`, `genetic-momentum`
- strategies that do not implement `RequiredSymbolsProvider` only with `-include-universe`

Explicit `-secondary` ids skip that filter. After pairwise ranking, a greedy pass stacks up to `-stack-depth` overlays whose traded symbols do not overlap. `-persist-best` writes one shared DB for that stack. Calc databases for the sweep sit in `reports/stack_eval_calc/`.

Staleness (`pkg/backtest/runner/staleness.go`) is shared by `backtest stale` and `gridsearch stale`. A result is stale if any one of these is true: the strategy is no longer registered, the market DB has a later bar than the run recorded, or a SQL pipeline file is newer than the result.

## Studies

`pkg/backtest/study` is a registry of `Study` values (`ID`, `Name`, `Description`, `SetDatabases`, `Run`). `cmd/study` imports `pkg/backtest/study` and blank-imports `pkg/backtest/study/market_context`. A study in another subpackage does not register unless that import is added.

`Run` writes `filepath.Join(outDir, id + ".db")`. `SymbolAware` studies receive `-symbol`.

| ID | Output | Notes |
|---|---|---|
| `market_context_20d` | `reports/market_context_20d.db` | 20-session simple and log returns for VOO, IEF, GLD, USO, HYG. SQL: `sql/studies/20dayreturn.sql` |
| `cluster_5pct` | `reports/cluster_5pct.db` | 5% up/down rates by the IEF/GLD/USO cluster. Requires `SetClusterDB` (not a CLI flag) and `data/gain_5pct_frequency.db` next to the market DB |
| `googl_market_context` | `reports/googl_market_context.db` | GOOGL beta, alpha, residual by `cluster_day`. Requires `SetClusterDB`. SQL: `sql/studies/googl_regime.sql` |
| `gain_5pct_frequency` | `reports/gain_5pct_frequency.db` | ranks symbols by days with a ≥5% gain |
| `voo_up3_etf` | `reports/voo_up3_etf.db` | VOO 3-up days, then each sweep-list ETF. View `etf_compare` feeds `gridsearch -symbols-from` |
| `hmm_regime` | `reports/hmm_regime.db` | HMM regimes; `-symbol` defaults to QQQ |
| `qqq_tqqq_volume` | `reports/qqq_tqqq_volume.db` | daily QQQ vs TQQQ volume |
| `march_april_voo_gld_uten` | `reports/march_april_voo_gld_uten.db` | 1-minute VOO/GLD/UTEN, Granger and spillover |
| `mara_decision_tree` / `mu_decision_tree` | `reports/<id>.db` | CloudForest trees aimed at ±5% days |
| `etf_study` | `reports/etf_study.db` | decline-slice study on top S&P ETFs |
| `sp500_lead_lag` | `reports/sp500_lead_lag.db` | minute lead/lag into VOO |
| `market_clustering` | `reports/market_clustering.db` | universe feature clusters |

`sql/studies/cluster_5pct.sql` is the cluster study's SQL text.

## Validation

`validate` (`pkg/backtest/validate`) is the one command for this stage: `validate` runs the folds and then the verdict, `validate walk` only the folds, `validate verdict` only the verdict. The two halves below keep their logic in `pkg/backtest/walk_forward` and `pkg/backtest/check_overfit`, and the commands of those names are deprecated aliases.

`walk_forward` slices the daily bars into rolling folds (default 24 months in sample, 6 out of sample, step 6). Each fold is a full `PortfolioSimulator` run. Rows land in `walk_forward_fold`. `sql/validation/walk_forward_summary.sql` rebuilds `walk_forward_summary`. The default file `reports/walk_forward.db` is relative to the process working directory, not `appenv.Reports()`. Each fold row also keeps idle days: `is_idle_days` and `oos_idle_days`, sessions with no open position, out of `is_days` and `oos_days` sessions. `walk_forward_summary` totals the out-of-sample ones (`oos_idle_days`, `oos_days`, `oos_idle_pct`), `check_overfit` and `validate` print them beside the verdict, and a fold table made before they existed gets the columns (0 of 0 sessions until the strategy is walked again).

`check_overfit` reads `walk_forward_summary` and labels each strategy:

| Verdict | Rule |
|---|---|
| `INSUFFICIENT` | OOS trades below `-min-oos-trades` (8) |
| `CURVE_FIT` | trials at or above `-trial-cutoff` (20) and OOS Sharpe collapses |
| `DECAYS` | OOS Sharpe below `-decay` (0.25) times in-sample Sharpe |
| `HOLDS` | otherwise |

It records the gates in `check_overfit_gate`.

`strateval` is a separate ledger, not the walk-forward file. For each strategy it runs an in-sample window and a held-out tail of `-oos-months` (12). `-optimize` does a coarse in-sample sweep capped by `-max-trials` before the out-of-sample run; the sweep does not see the OOS bars. Rows go to `strategy_evals`. `strategies` and `deployments` track the allowlist snapshot from `sync-deployed`. Tier A requires OOS trades, win rate, and drawdown inside the gates (`-min-oos-trades 12`, `-min-oos-win-rate 0.55`, `-max-oos-dd 0.15`). Scratch sims use `reports/strateval_runs/`. `strategy_evals` also keeps `is_idle_days`, `is_days`, `oos_idle_days` and `oos_days`, and the scorecard shows the out-of-sample idle days.

## Other commands


`universe` writes `refdata/universe.db`. `strategy` lists the registry. The `dataflare` and ETF commands no longer exist.

## Engine rules that are easy to get wrong

- A positional id must not be followed by flags. `flag.Parse` stops at the first non-flag, and `backtest` joins the leftovers into the strategy list.
- `id.db` is run 1. `id_N.db` is run N. The latest usable file is the highest N that still validates, not the newest mtime.
- Shared-account priority is list order. Overlays do not preempt each other.
- Next-day limits that never trade are absent from `trades` and still present in `signals`.
- A limit that does not fill reserves no cash in the simulator, so a `pick = all` period row with dozens of limit entries can book more names than a real account could rest orders for. Same-day fills are taken alphabetically and one that costs more than the cash left is skipped without counting as a fill. The live system (`pkg/orchestrator/trigger_limits`) seats the nearest-to-fill limits until the broker refuses one for buying power, then swaps and triggers; see the buying-power note in README.md under the 80% CAGR result. The two orderings and the position count differ, and the gap has not been measured.
- Daily fetches ignore intraday rows. Downloading `1m` bars does not change a daily backtest until a study queries `timeframe = '1m'` directly (`march_april_voo_gld_uten`, `sp500_lead_lag`).
- `streak-*` registration also depends on `refdata/strategies.db` (`streak_strategy`). `gridsearch -strategy all` omits that prefix unless `-include-streak`. Scoreboard and backtest include every row.

Markov strategies do not train at backtest time. `train markov` (`pkg/backtest/train`) runs `sql/stages/markov_train` and publishes `markov_prediction` and `markov_model_meta` into `data/markov_models.db` (`appenv.MarkovDB()`). The `markov_model` pipeline attaches that file (`__MARKOV_DB__`) and reads one symbol's rows; `markov_strategy.GenerateSignals` refuses to run without a trained model for the symbol.

Training is not part of `backtest`. `train <family>` is the one training command; `markov` and `tree` have a model to fit. Decision trees are trained by `train tree` into `data/tree_models.db`: `sql/stages/tree_features` builds the features and the 5 bucket label, CloudForest grows the tree, and a `tree_strategy` row walks the saved tree in SQL (`sql/strategies/tree_strategy`), buying on class 2. Nothing trains at backtest time. The CloudForest studies live in `pkg/backtest/study` (MARA, MU, HMM), and `gridsearch` searches parameters and writes its own DB.

Results are laid out by run. Each `backtest` invocation creates `data/reports/<run_id>/` (the next number above the highest there is, made with a plain mkdir so two processes get different numbers), and writes one result database per strategy family inside it, `storage.ResultsFor(runDir, strategy.FamilyOf(s))`. The held-out pass writes the same file names under `<run_id>/oos/`. `-run-id N` goes back into an existing run, and then strategies already finished in it are skipped unless `-force`. `scoreboard compile` and `status` and `backtest stale` read the latest run unless given one. Inside a family file, `runs.run_id` still numbers the individual strategy runs.

Throughput: strategies run on a worker pool (default one per CPU core) and `runner.RunBatched` loads the next batch of bars while the current one runs. SQL pipelines run in parallel (the old global lock is gone: every strategy has its own calc database). Pipeline joins to the market database must be on `t.Date = x.date`, not `substr(t.Date, 1, 10) = x.date`, or SQLite cannot use the index and scans the symbol's rows once per row. `backtest -cpuprofile file` writes a CPU profile.
