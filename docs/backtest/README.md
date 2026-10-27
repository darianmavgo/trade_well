# backtestgosqlite

> **Moved 2026-10-27.** This was the `backtestgosqlite` repository README. The code is now `pkg/backtest/` in the single `trade_well` module; paths in this document are relative to the repo root (`cmd/`, `sql/`, `refdata/`, `data/`, `deploy_*` stay at the root). Build with `make build`; binaries land in `bin/`.

Go backtester. Market bars, reference lists, signals, trades, and reports live in SQLite. Build the binaries, then run a command from the repository root so `.env` and the default paths resolve.

```bash
make build          # every cmd/* binary into bin/
make test           # go test ./...
make check          # go build, go vet and go test, writing no binaries
make list           # the cmd/ names; strategies: ./bin/backtest -strategylist
```

`go run ./cmd/<name>` is the same program as `./bin/<name>`. Package map and internals are in [Architecture.md](Architecture.md); rules for editing the code are in [pkg/backtest/CLAUDE.md](../../pkg/backtest/CLAUDE.md) and [../CLAUDE.md](../CLAUDE.md).

## Pipeline in order

Each command is one stage and answers one question. Run them left to right, or run them all for a set of stocks with [`pipeline`](#pipeline).

| Stage | Question | Command |
|---|---|---|
| Data | What bars do I have? | `market_history`, `universe` |
| Train | Fit the models | `train` |
| Run | How did it do? | `backtest` |
| Tune | Which parameters? | `gridsearch` |
| Validate | Is it real or overfit? | `validate` |
| Score | In and out of sample, with a tier A to D and the deployed ledger | `strateval` |
| Rank | Which is best? | `scoreboard` |
| Go live | What do I buy next session? | `livescan` |

## What it can do

| Capability | Command |
|---|---|
| Download daily, minute and option bars | `market_history` |
| Build the stock and ETF universe (`refdata/universe.db`) | `universe` |
| Backtest one strategy, many, or a shared-cash stack | `backtest` |
| Stack strategies on one cash ledger with `a+b+c`, with leftover cash parked in a symbol (`park-<symbol>`) | `backtest` |
| Rank strategies as idle-cash overlays and build a greedy stack | `backtest stack` |
| Sweep hold, take-profit and stop, then promote winners to new strategies | `gridsearch` |
| Is it real or overfit: walk-forward folds plus a verdict per strategy | `validate` |
| In-sample / out-of-sample ledger with tiers A to D | `strateval` |
| Rank every registered strategy, or a list | `scoreboard` |
| Live signal scan for the next session | `livescan` |
| Research studies (clustering, HMM regimes, lead/lag, decision trees) | `study` |
| Covered-call simulation | `backtest covered-call` |

### Strategies

Every strategy is calculated in SQL and run by Go. `backtest -strategylist` prints the counts per family and asks before dumping all of them, about 65,600 today; `./bin/strategy` prints the strategies that have their own pipeline. Where the definition lives:

| Source | Count | Defined in |
|---|---|---|
| Own pipeline | a handful (`voo-up3`, `price-action-reclaim`, `tsll-daily-one-share`, covered calls) | a small config in `pkg/backtest/strategy/` plus `sql/strategies/<id>/` |
| Rows in `refdata/strategies.db` | `streak_strategy` 13,136, `hold_strategy` 26,242 (the old hold_bail rows were merged in when the DB was first opened) `tree_strategy` 13,121 (ids `<symbol>_tree`), `markov_strategy` 13,122, `rotation_strategy` 8 (five rank a liquid universe on momentum and hold the top few. `biggest-winner`, `-short` and `-inverse` are period rows: at the start of each calendar `period` (`1d`, `1w`, `1m`, `1q`, `1y`) hold the top `top_k` names by the previous period's return, `side` long, short or the matched inverse ETF; seeded by `sql/seed/rotation_period.sql`) | `pkg/backtest/streak_strategy`, `pkg/backtest/hold_strategy`, `pkg/backtest/tree_strategy`, `pkg/backtest/markov_strategy`, `pkg/backtest/rotation_strategy`, each running one shared `sql/strategies/<family>/` |

Most streak rows are a generic "drop 3 days, buy the rebound" rule, one per symbol. A few were promoted from sweeps (`source_strategy` `voo-up3`, `gld-decline`, `universe-screen`, `manual`). `streak-voo-buy-tecl` is now `streak-voo-buy-tecl`. `park-<symbol>` resolves for any ticker without registration (see below). The older `streak-voo-buy-tecl`, `gld-decline` and `googl-hop` strategies are no longer registered; their SQL folders remain in `sql/strategies/`.

### Searching for a stack

The search that produced the current numbers, in order:

1. `validate -keep-going` over the candidates on a market DB cut off before a held-out final year.
2. `sql/search/01_gate_walk_forward.sql`: strict out-of-sample gate (at least 30 OOS trades, OOS Sharpe 1.5, 75% positive folds, worst fold drawdown 6%).
3. `sql/search/02_liquidity_returns.sql` and `03_liquidity_screen.sql`: dollar volume, price and split-jump screen. `04_candidates.sql` joins the survivors to their trade symbols. Run these with `sqlite3`, attaching the walk-forward DB (`wf`), market DB (`mkt`) and `refdata/strategies.db` (`ref`).
4. `backtest stack` over the survivors for several primaries and `-alloc` sizes.
5. Re-run the frozen stacks on the held-out year with `-start`.

`./bin/train check` lists models trained on bars after the holdout cutoff (today: trees; markov is walk-forward by construction) and saves them to `training_leak` in `data/reports/training_leaks.db`. `-fix` retrains each through the cutoff; a symbol without enough pre-cutoff history stays on the list.

Every `backtest` and `backtest stack` run now does step 5 itself (see the holdout note under `backtest`). Result to date: no stack reached 79% CAGR with drawdown under 6%. Liquid stacks held a Calmar of about 4 to 8 in-sample and about 4.5 on the holdout. The benchmark to beat is in [docs/omnifunds_benchmark.md](../research/omnifunds_benchmark.md).

Strategies that looked like they reached the goal and did not hold up are logged, with the reason, in [docs/StrategyLog.md](StrategyLog.md). No strategy has reached the goal.

## Create and polish a strategy: GOOGL walkthrough

Every command in this repo, in the order you use them to take one idea from raw bars to a live signal. `pipeline` runs all of these for you in one scoped run, and `scripts/googl_pipeline.sh` is the GOOGL example. The example is GOOGL, using the rows that already exist in `refdata/strategies.db`: `streak-googl-down3`, `streak-googl-down3-googl`, `googl_tree`, `markov_model_googl` and `markov_hmm_googl`. Swap in another ticker the same way. Flags go before positional ids. Steps 2, 3, 17 and 18 are optional; the rest are the path.

| # | Stage | Command | What it does for GOOGL | Reads / writes |
|---|---|---|---|---|
| 1 | Get data | `./bin/market_history GOOGL VOO SGOV -years 5` | Downloads hourly bars (and derives daily sessions) for the signal symbol, a benchmark and a cash park. Run again to fill new sessions. | writes `data/market_history_hourly.db` |
| 2 | Find symbols (optional) | `./bin/universe -stocks-only` | Rebuilds the stock and ETF universe to pick related tickers. Needs `POLYGON_API_KEY`. | writes `refdata/universe.db` |
| 3 | See what exists | `./bin/strategy` then `./bin/backtest -strategylist` | Confirms the GOOGL ids above are registered and shows row counts per family. | reads `refdata/strategies.db` |
| 4 | Explore the idea | `./bin/study -list`, `./bin/study -study hmm_regime -symbol GOOGL`, `./bin/markov_test` | Regime study for GOOGL; `markov_test` prints the bear / sideways / bull matrix and tomorrow's odds. Use `-study googl_market_context` only if a caller supplies a cluster DB. | reads market DB; writes `data/reports/<study>.db` |
| 5 | Train models | `./bin/train markov GOOGL` and `./bin/train tree GOOGL` | Fits the Markov regime table and the depth-3 tree that `markov_model_googl` and `googl_tree` read. `streak` and `hold` have nothing to train. Use `-through <date>` on `markov` or `tree` to keep later months out of sample (see [docs/CorrectlyUsingMarkov.md](../CorrectlyUsingMarkov.md)). | writes `data/markov_models.db`, `data/tree_models.db` |
| 5a | QQQ regime model | `./bin/daily_bars` then `./bin/train hmm` (also `train hmm compare`, `extend`, `report`, `export`) | Downloads QQQ daily bars from 2010 into `data/daily_bars.db` (replacing that symbol's history; `-dry-run` writes nothing), then fits a 3-state Gaussian hidden Markov model of daily return and 20-day realized volatility, with the newest 12 months held out (`-through`, `-holdout-months`). `extend` appends predictions with the frozen parameters and never refits. `train hmm` is not part of `train all`. Full walk-through: [docs/MarkovQQQ.md](../MarkovQQQ.md). | writes `data/daily_bars.db`, `data/hmm_models.db` |
| 5b | Trade the QQQ regime | `./bin/backtest -strategy hmm-calm-qqq,hmm-calmdown-qqq -auto-download=false` | The `hmm` family reads the model from step 5a. `hmm-calm-qqq` is long while the stressed regime is unlikely, `hmm-calmdown-qqq` after stress eases. The model's regimes are volatility regimes, so a rule works with the stressed one (highest volatility), never a state number. QQQ hourly bars start 2024-10, so the backtest window is about two years and the held-out year is thin. The model's `hmm_symbol` is a row field, default QQQ | writes `data/reports/<run_id>/hmm.db` |
| 7 | First backtest | `./bin/backtest -strategy streak-googl-down3,googl_tree,markov_model_googl` | Runs each idea alone: an IN-SAMPLE pass, then one OUT-OF-SAMPLE pass on the last 12 months. Tune on the in-sample numbers only. | writes `data/reports/<run_id>/` (`streak.db`, `tree.db`, `markov.db`, `report.html`, `oos/`) |
| 8 | Look at the grid | `./bin/gridsearch params streak-googl-down3` | Prints the hold, take-profit, stop and regime grid with no simulation. | none |
| 9 | Sweep parameters | `./bin/gridsearch -strategy streak-googl-down3 -top 20` | Sweeps hold, target, stop and regime. The sweep stops before the held-out months (`-holdout-months`, default 12) so they do not tune the result. | writes `data/reports/gridsearch.db`, `<strategy>_gridsearch.html` |
| 10 | Promote winners | `./bin/gridsearch promote -strategy streak-googl-down3 -min-win-rate 0.6 -min-trades 30 -top 5` | Copies the best sweep rows into `streak_strategy` as new ids such as `streak-googl-down3-googl`. | writes `refdata/strategies.db` |
| 11 | Re-run tuned | `./bin/gridsearch apply -run-id <N> -strategy streak-googl-down3` | Re-runs with the highest-resilience sweep row. Compare to step 7. | reads `gridsearch.db` |
| 12 | Walk-forward | `./bin/validate walk -keep-going -strategy streak-googl-down3-googl,googl_tree` | Rolling 24 month train / 6 month test folds, one strategy at a time. | writes `data/reports/walk_forward.db` |
| 13 | Overfit verdict | `./bin/validate verdict -strategy streak-googl-down3-googl,googl_tree` (or plain `validate`, which does 12 and 13 together) | Prints `HOLDS`, `DECAYS`, `CURVE_FIT` or `INSUFFICIENT` per strategy. Drop anything that is not `HOLDS`. | reads/writes `walk_forward.db` |
| 14 | Ledger and tier | `./bin/strateval -strategy streak-googl-down3-googl`, then `strateval report` | Records in-sample and out-of-sample results and a tier A to D (A needs 12 OOS trades, win rate 0.55, drawdown under 0.15). `-optimize -max-trials 50` searches parameters inside the eval. | writes `data/reports/strategies.db` |
| 15 | Stack it | `./bin/backtest -strategy "streak-googl-down3-googl+googl_tree+park-googl" -alloc 0.1` | Shares one cash ledger. `park-googl` (or `-default-asset GOOGL`) holds leftover cash in GOOGL and is never the primary. | writes `data/reports/<run_id>/stack.db` |
| 16 | Find complements | `./bin/backtest stack -primary streak-googl-down3-googl -secondary "googl_tree,markov_model_googl,park-sgov" -stack-depth 3` | Ranks each secondary as an idle-cash overlay, then builds the greedy stack. Rank finalists by Calmar and max drawdown. Beat [docs/omnifunds_benchmark.md](../research/omnifunds_benchmark.md). | writes `data/reports/stack_eval_<primary>.db` |
| 17 | Park-symbol sweep (optional) | `./bin/park_sweep -strategy <ids> seed`, `run`, `rank`, `report` | Runs the listed `streak_strategy` and `markov_strategy` rows (all rows without `-strategy`) with leftover cash parked in GOOGL and ranks them. | writes `data/reports/park_googl.db`, `park_googl.html` |
| 18 | Options overlay (optional) | `./bin/market_history -source polygon-options -symbols GOOGL`, then `./bin/backtest covered-call -symbol GOOGL` | Compares holding GOOGL with selling a monthly call. | writes `option_*` tables; prints to stdout |
| 19 | Rank everything | `./bin/scoreboard -strategy <ids>`, `scoreboard status -strategy <ids>` | Backtests the listed strategies (all registered ones without `-strategy`) that lack a result and ranks them. | writes `data/reports/scoreboard.db` |
| 20 | Keep fresh | `./bin/backtest stale` and `./bin/gridsearch stale` | Lists results made stale by new bars or edited SQL. Rerun steps 1, 5, 7 for those. | none |
| 21 | Go live | `./bin/livescan -strategy streak-googl-down3-googl,googl_tree -json` | Signals for the next session from the last completed bar. `ENTER` means buy next session. `trade_orchestrator` consumes the JSON. | writes `data/reports/livescan.db` |
| 23 | Rank in bulk | the `stack-candidates` skill | Runs `train`, `backtest`, `gridsearch` and `strateval` for all families and reports the top 20 to stack. See [Stack candidates skill](#stack-candidates-skill). | uses all of the above |

Steps 6 and 22 were the `stratlist` and `transaction_calc` commands, which were removed; the other numbers are unchanged so references to them stay valid.

Notes:

- Steps 7, 9, 12 and 14 must stay on in-sample data; look at the `oos/` pass or the last 12 months only once, at the end. Edit the strategy after seeing the held-out result and that result is in-sample.
- A new idea that is not a row yet: write it as SQL following [docs/strategies/writing_a_strategy.md](strategies/writing_a_strategy.md), or as a `streak_strategy` row ([docs/strategies/streak_strategy.md](strategies/streak_strategy.md)), then start at step 5.
- Command details and flags are in the per-command sections below.

## Paths

`pkg/backtest/appenv` reads the environment, then the nearest `.env`. A real environment variable wins.

| Variable | Default | Used for |
|---|---|---|
| `APP_FOLDER` | `.` | repository root. `data/`, `refdata/` and `data/reports/` are fixed subfolders of it |
| `POLYGON_API_KEY` | empty | Polygon equity and option downloads, `universe` |
| `ALPACA_KEY`, `ALPACA_SECRET` | empty | Alpaca bar downloads (`market_history -source alpaca`) |
| `STRATEGY_ALLOWLIST` | empty | `strateval` live-list snapshot |

Default files:

| Role | Path |
|---|---|
| Market bars | `data/market_history_hourly.db`, table `backtest_start` (`appenv.MarketDB`, `appenv.HourlyDB`). Hourly bars (timeframe 1h, `Date` = `YYYY-MM-DD HH:MM:SS` UTC) are the only bars downloaded. The daily session rows that signals and the simulation clock read (`Date` = `YYYY-MM-DD`, timeframe 1d) are derived from them in the same table. There is no separate daily database |
| Minute bars (1m, 5m, ...) | `data/market_history_minute.db`, table `backtest_start` (`appenv.MinuteDB`; not created until minute bars are downloaded) |
| Strategy tables and symbol lists | `refdata/strategies.db` (`streak_strategy`, `hold_strategy`, `tree_strategy`, `markov_strategy`, `hmm_strategy`, `rotation_strategy`, and `symbol_lists`: `symbol_list_id`, `list` = comma separated tickers. `etf-pre-2021` is every ETF in `universe.db` first traded before 2021-01-01, from `sql/seed/symbol_lists.sql`. A `rotation_strategy` row whose `symbols` equals a `symbol_list_id` (case and surrounding spaces ignored) uses that list; any other value is a literal ticker list. `strategy export` carries the lists a row names) |
| Stock and ETF universe | `refdata/universe.db`, table `universe` |
| Backtest results | one folder per run, `data/reports/<run_id>/`, numbered 1, 2, 3, … Inside it one database per strategy family (`markov.db`, `tree.db`, `streak.db`, `hold.db`, `builtin.db` for Go-defined strategies, `stack.db` for stacks), the HTML report as `report.html`, and the held-out pass in `oos/` with the same file names |
| Shared-account results | `data/reports/shared_<primary>_<secondary>_….db` |
| HTML tear sheet | `data/reports/backtest_report.html` |

Every command defaults to `data/market_history_hourly.db`. `backtest`, `gridsearch`, `market_history` and `strategy coverage` refuse a `-db` that is not an hourly database (the name must end in `_hourly`), so a leftover daily file is never read.

The Go `flag` package stops at the first bare argument. Put flags before a positional id or ticker. A subcommand is argument 1: `backtest stack -primary …`, not `backtest -primary … stack`.

Daily bars are derived, not downloaded (`pkg/backtest/sessions`). A session is built from the regular-session hourly bars (the bars that start 9:00 to 15:00 Eastern): open is the 9:00 bar's open, high and low are the extremes, close is the 15:00 bar's close, volume is the sum. The 9:00 bar includes the half hour before the 9:30 open, so the session open and range can include pre-market trades; the close is exact. An hour with no trade has no bar, which is right for a bar built from trades. `market_history` derives the sessions after every download, `backtest` and `gridsearch` bring them up to date at startup, and `market_history sessions` rebuilds them on demand. Simulations load rows with `length(Date) = 10`.

---

## Simulator, engine and checks

Backtests, grid searches, walk-forward folds and stacks (with or without a parked default asset) run on the **hourly simulator** (`pkg/backtest/sim`, driven through `pkg/backtest/hoursim`): every fill and exit is judged on the hour it can happen, and nothing exits on the bar that entered it. A session with no hourly bars is played as its daily bar (one bar) and the log names the symbols. It is the only simulator: the daily-range one was deleted, and a strategy that needs margin, ATR stops, Kelly sizing or dividends (total-return) is refused with that reason.

Each stored run records its engine in `runs.engine` (`hourly`; none means it predates the column and was made by the old daily simulator, so `scoreboard` and `backtest` redo it). A run that traded a symbol with an unadjusted split (a close-to-close move beyond 1.8x between sessions, `sessions.SplitJumps`) records why in `runs.suspect` and is left out of `scoreboard` unless `-include-suspect`. A row that can never fill (entry limit at or below its stop) fails with that reason instead of reporting zero trades.

`docs/HourlyEngineParity.md` shows what changed against the daily engine and how to measure it (`pkg/backtest/hoursim/parity_test.go`).

### check

```sh
./bin/check -results data/reports/<run>/streak.db           # the newest run in that file
./bin/check -results <db> -run-id 3 -markov -through 2025-10-09
```

Asks, in SQL (`sql/stages/check/`), whether every trade started from a signal, each signal bought at most once, trades exit on or after entry with a size and prices, and a strategy never held a symbol twice at once. With `-markov` it also checks the model database: every model records a cutoff not past `-through`, the signal is `prob_bull - prob_bear`, probabilities are probabilities, and every model has predictions. Exits 1 when a checkpoint fails.

## market_history

Pull hourly bars into SQLite and derive the daily sessions from them. The source is Alpaca (default) or Polygon. Yahoo and Stooq and any timeframe other than `1h` are retired.

**Reads:** `refdata/strategies.db`. A name in `-symbols` (or a bare argument) that is a `symbol_lists` id, such as `etf-pre-2021-unleveraged`, is replaced by that list's tickers; the other names are tickers. With no symbols at all it reads table `leveraged_etf` (limit 50), which is empty today, so pass tickers or a list id.

**Writes:** `data/market_history_hourly.db`, table `backtest_start` (hourly rows, then the derived session rows). Option history uses the same DB, tables `option_contracts`, `option_bars`, `option_expiry_scan`. Missing dates are filled; `-force` replaces the range.

| Flag | Default |
|---|---|
| `-db` | `data/market_history_hourly.db` |
| `-settings` | `refdata/strategies.db` |
| `-source` | `alpaca` (`polygon`, `polygon-options`, `alpaca-options`: every call and put, expired too, daily bars from 2024 into `option_contracts` / `option_bars`; `-symbols` are the underlyings) |
| `-target-table` | `backtest_start` |
| `-timeframe` | `1h` (the only value) |
| `-years` | `5` |
| `-table` | `leveraged_etf` (settings DB symbol table) |
| `-limit` | `50` (only the settings-table fallback) |
| `-otm` | `0,2,5` (polygon-options, percent OTM) |
| `-rate` | `5` API calls/minute (polygon and polygon-options; a 429 waits a minute and retries; `0` = unthrottled) |
| `-unleveraged-etfs` | off. Symbols = every active ETF with leverage `none` in `refdata/universe.db`, most traded first (20-day average volume). `-years` defaults to 2 with it |
| `-status` | off. Downloads nothing: prints how many of the symbols are already pulled, partly pulled or not pulled in the target database, the requests left and the time they take at `-rate` |
| `-universe-db` | `refdata/universe.db` (read by `-unleveraged-etfs`) |

```bash
./bin/market_history VOO IEF GLD
./bin/market_history -symbols PDD -years 6
./bin/market_history -symbols etf-pre-2021-unleveraged -timeframe 1h -years 2 -source polygon -concurrency 1
./bin/market_history -source polygon -symbols VOO -timeframe 1m -polygon-key "$POLYGON_API_KEY"
./bin/market_history -source polygon-options -symbols VOO
./bin/market_history -source alpaca-options -symbols UPRO,SPXU
./bin/market_history -source polygon -timeframe 1h -unleveraged-etfs -concurrency 1   # hours of runtime at 5 calls/min
./bin/market_history -source polygon -timeframe 1h -unleveraged-etfs -status       # what is pulled, what is left
./bin/market_history -source alpaca -timeframe 1h -symbols SPY,QQQ -years 5          # Alpaca hourly bars (keys from .env; always the consolidated sip feed, which needs a paid Alpaca data plan)
```

Hourly and minute bars go to `data/market_history_hourly.db` / `data/market_history_minute.db`, and coverage is checked there first, so a rerun asks the source (Polygon or Alpaca) only for the missing days. Alpaca always uses the consolidated `sip` feed, which needs a paid Alpaca data plan; on the free plan it answers 403 (the IEX feed is no longer selectable).

**`market_history update -strategy <id>`** keeps the bars of one strategy's symbols complete. It takes the symbols from the strategy (candidates, regime symbol, benchmark; `a+b+c` stacks work), checks with the same completeness test as `strategy coverage` which of them have sessions with no bars, and downloads only those. Defaults: `-timeframe 1h`, `-source alpaca`, `-start 2021-01-01`, no `-limit`. A symbol with a gap is fetched again over the whole window and replaces bars stored at the same time, because a normal run fills only the two ends of a symbol's range, never a gap inside it. Alpaca returns up to 10,000 bars a request and the source pages itself. `-strategy` is required; `-dry-run` lists the symbols with gaps and downloads nothing; the other flags are the download flags above (`-symbols` is ignored).

```bash
./bin/market_history update -dry-run -strategy rotation-2x-sector-pairs-daily-limit95-tp3-sl5-hold2-pos20
./bin/market_history update -strategy rotation-2x-sector-pairs-daily-limit95-tp3-sl5-hold2-pos20
./bin/market_history sessions                                                                                       # rebuild the derived daily sessions
```


`-start YYYY-MM-DD` overrides `-years`. `-end` defaults to today.

---

## backtest

Run one strategy, many strategies, or a shared cash account.

**Reads:** market DB (`-db`, table `-table` `backtest_start`). `gridsearch apply` is what reads `gridsearch.db` and calls back into this package. SQL pipelines read `sql/strategies/<id>/` from disk: the folder named `sql` in the working directory or the nearest parent, with nothing embedded in the binary and no other place searched. Without one the command fails with an error that names the directory it searched from.

**Writes:** `data/reports/<id>.db` (next free `data/reports/<id>_N.db` if the name is taken). Tables `signals`, `trades`, `equity_curve`, `performance_summary`, and when relevant `return_breakdown` and `run_metrics`. HTML at `data/reports/backtest_report.html`. Auto-download (`-auto-download`, default on) writes missing bars into the market DB. Window starts `2021-01-01` (`-start`); earlier bars warm up SMAs only. Capital default `$100,000`. `-download-years` default `5`.

Limit entries are judged on the hourly bars, in `backtest` and `gridsearch` alike: the fill is the first hour whose low reaches the limit, and only hours after it can book the take-profit or stop (the stop is checked before the target inside one hour). A symbol with no hourly bars is named in a warning.

**Reliability report.** Before the tear sheet, `backtest -strategy <id>` prints a PASS / INFO / WARN / FAIL line for six recognized backtest problems, with the counts behind each, and repeats them in the HTML report's first parameter group. It is computed from the finished trades and the bars in the market databases and does not change the simulation. `INTRABAR`: stop and target (or limit and target) inside one bar so the order is unknown, and same-session exits with no hourly bars to order them (FAIL). `RESOLUTION`: limit entries judged on thin or missing hourly sessions. `LOOKAHEAD`: fills priced outside the session's range, and tree models trained past the holdout cutoff (look-ahead inside a strategy's own signal SQL is not audited). `FILLMODEL`: how many limit fills were a bare touch of the limit, and what a fill assumes (no queue, no partial fills). `DATAGAPS`: sessions with no hourly bars for the symbols the strategy trades. `OVERFIT`: no held-out period, a strategy that was swept, fewer than 30 trades, and in the out-of-sample pass a collapse against the in-sample report (FAIL). In a multi-strategy run only the strategies whose trades are kept get a one-line summary. `backtest stack` does not print it yet.

Every run prints the database it reads bars from, with size and last update, right after startup: `market_history_hourly.db`, whose derived sessions feed signals and whose hourly bars decide limit fills and exits (used for limit-entry fills and exits; `not found` means those fills fall back to daily rules). The same path is in the HTML report's Run parameters as `Market DB`.

```bash
./bin/backtest -strategylist                    # counts per family, then asks before dumping every strategy (y/N)
./bin/backtest -strategy streak-voo-buy-tecl -capital 100000
./bin/backtest -strategy streak-voo-buy-tecl,mara_tree
./bin/backtest -strategy all
./bin/backtest -family rotation                 # every row of one family: streak, hold, tree, markov, hmm, rotation (same as -strategy rotation; not combinable with -strategy)
./bin/backtest -strategy markov                 # every row of one family: streak, hold, tree or markov
./bin/backtest -strategy markov -run-id 7       # continue run 7: strategies already done in it are skipped
./bin/backtest -primary streak-voo-buy-tecl -secondary mara_tree,pdd_tree -capital 100000
./bin/backtest streak-voo-buy-tecl+mara_tree
./bin/backtest -alloc 0.10 streak-voo-buy-tecl+mara_tree+pdd_tree
./bin/backtest -primary streak-voo-buy-tecl -secondary mara_tree -alloc 0.10 -default-asset VYM
```

**Out-of-sample holdout (default).** The last 12 months of history are held out. The main pass runs from `-start` through the cutoff (the last bar minus `-holdout-months`), prints as `IN-SAMPLE`, and writes its usual files. Then the same strategy, or for `backtest stack` the stack it found (park included), runs once on the held-out months, flat at the start, printed as `OUT-OF-SAMPLE` and written to `data/reports/oos/` with an `_oos` HTML file. Select and tune on the in-sample pass only. `-holdout-months 0` simulates all history in one pass; `-end YYYY-MM-DD` ends history earlier. The split is skipped, with a message, when less than a year would be left in-sample. It does not apply to `-strategylist`, `-signals-only`, `stale`, `gridsearch apply` or `covered-call`. Two caveats: single-strategy results in `data/reports/<id>.db` now cover the in-sample window only, and a strategy whose SQL pipeline or tree was fitted on all history can still see the future inside the held-out months.

`-symbol` limits the book to one ticker. `-hold`, `-target`, `-stoploss`, `-max-positions`, and `-alloc` override the strategy config when set (non-zero). `-alloc` is a fraction of equity per position (`0.10` = 10%) on standalone runs and on shared-account stacks. `-default-asset GOOGL` is shared-account only: after each session, leftover cash is bought into that symbol, and a sleeve entry sells it first to fund the order. The result goes to `stack.db` in the run folder, and `shared_account_audit` records the default asset. `-no-reinvest-dividends` pays dividends into cash for total-return strategies. `-force` re-runs strategies that already have a usable result (multi-strategy only). `-signals-only` skips the portfolio sim and scans the live window the same way `livescan` does; it does not stack.

### backtest newrun

Create the next run folder and print only its number, so a script can pass one `-run-id` to every command and keep a whole pipeline in one folder (`scripts/googl_pipeline.sh` does this).

```bash
RUN=$(./bin/backtest newrun)
```

### backtest lastrun

Record where each strategy's most recent backtest lives. It reads `performance_summary` in every result DB under `data/reports/` (`*.db` and `<run>/*.db`, not `oos/`) and rewrites the table `strategy_last_backtest` in `strategies.db`: `strategy_id`, `result_db` (relative to `APP_FOLDER`, e.g. `data/reports/58/rotation.db`), `created_at`, `win_rate`, `total_trades`. Stacks (`a+b`) and built-ins get a row too; a strategy never backtested has none. It starts no run folder and runs no simulation. `-out-dir` picks the reports root and `-strategies-db` the database to write (default `refdata/strategies.db`).

```bash
./bin/backtest lastrun
sqlite3 refdata/strategies.db "select result_db from strategy_last_backtest where strategy_id='streak-voo-buy-tecl'"
```

### backtest stale

Print which `data/reports/*.db` results are stale (unknown strategy, newer market bars, or an edited SQL pipeline). No simulation.

```bash
./bin/backtest stale
```

### backtest stack

`backtest stack-eval` is the deprecated name. Rank registered strategies as idle-cash overlays on one primary, one shared ledger. Rankings go to `data/reports/stack_eval_<primary>.db` (table `overlay_rankings`). Pairwise runs do not each write a `shared_*.db`. `-persist-best` (default on) writes one `data/reports/shared_<primary>_<secondaries>_N.db` for the greedy stack.

Default candidates skip duplicate `*-sql` ids, `voo-buy-hold`, `genetic-momentum`, and any strategy that does not name its symbols. Pairs are ranked by added equity; the greedy stack is chosen the same way, not by drawdown. Rank the finalists by Calmar and max drawdown from the persisted DB. `-include-universe` adds symbol-scanning strategies. Pass `-secondary` with an explicit list when the registry holds tens of thousands of strategies; `-db`, `-auto-download=false` and `-out-dir` let a search run on a trimmed market DB without touching `data/`. `-stack-depth` (3) is how many complementary overlays are stacked after the ranking.

```bash
./bin/backtest stack -primary streak-voo-buy-tecl
./bin/backtest stack -primary streak-voo-buy-tecl -secondary mara_tree,pdd_tree -persist-best
./bin/backtest stack -primary streak-voo-buy-tecl -stack-depth 5
```

### Park member in a stack

`park-<symbol>` (for example `park-googl`, `park-sgov`) is a stack member that holds leftover cash in that symbol. It is the same book as `-default-asset`, written as a stack member so it can be named in `-strategy`, `-secondary` and `backtest stack`. It emits no signals, resolves for any ticker without registration, cannot be the primary, and a stack holds one. Combining it with `-default-asset` for a different symbol is an error.

```bash
./bin/backtest -strategy "streak-voo-buy-tecl+streak-fslr-down3+park-googl" -alloc 0.1
./bin/backtest stack -primary streak-voo-buy-tecl -secondary "streak-fslr-down3,streak-penn-down3,park-sgov"
```

In `backtest stack` the park applies to the baseline, every pairwise run and the final stack; it is not ranked as an overlay. Compare parks by running `backtest stack` once per park symbol.

### backtest covered-call

Hold one underlying (default `VOO`) and sell a monthly call. Prints the comparison on stdout. Does not write a strategy result DB.

**Reads:** equity bars and `option_contracts` / `option_bars` in the market DB. Load the chains first with `market_history -source polygon-options`.

```bash
./bin/market_history -source polygon-options -symbols VOO
./bin/market_history -source alpaca-options -symbols UPRO,SPXU
./bin/backtest covered-call -symbol VOO -otm 2 -commission 0.65 -opt-slip 0.05
```

`-start` defaults to the first rollable month (option history is about two years on the Polygon free tier), not `2021-01-01`.

---

## livescan

Same signal generation as `backtest`, window fixed to the last completed session in the market DB. `ENTER` means a buy for the next session. Refresh failure aborts; it does not scan a stale tip.

**Reads:** market DB, table `backtest_start`. `-bars 0` loads each strategy's minimum history.

**Writes:** `data/reports/livescan.db` (`livescan_status`, `livescan_signals`). `-json` also prints the scan to stdout for `trade_orchestrator`.

```bash
./bin/livescan -list
./bin/livescan -strategy streak-voo-buy-tecl,mara_tree
./bin/livescan all
./bin/livescan -strategy streak-voo-buy-tecl -json
```

`-auto-download` defaults on. `-download-years 0` derives the window from `-bars` or the strategy minimum.

---

## gridsearch

Sweep hold, take-profit, stop, and the strategy's other axes. One strategy writes HTML. Several strategies share one worker pool and record progress so a rerun skips finished ids.

**Reads:** `data/market_history_hourly.db`, table `backtest_start`. `-symbols-from` reads a study DB `etf_compare` view (for example `data/reports/voo_up3_etf.db`) ranked by `rank_cagr`.

**Writes:** `gridsearch.db` (`gridsearch_runs`, `gridsearch_results`) in a run folder, `data/reports/<run_id>/`. `-run-id N` goes back into run N, and without it a sweep starts a new run (`stale` and `promote` use the latest). Single-strategy HTML defaults to `<strategy>_gridsearch.html` in the same folder. `stale` and `promote` take `-strategy` to limit to those ids. `-holdout-months` (default 12, the same window `backtest` holds out) stops the sweep at the in-sample cutoff, so the held-out months do not tune the parameters; `-end <date>` sets the stop date directly and `-holdout-months 0` sweeps all history. What it varies per family is in the `strategy_family_param` table of `refdata/strategies.db` (streak: signal days, hold, take-profit, stop, regime. tree and markov: exits only. hold: nothing. rotation period rows: the calendar period, below). Start date `2021-01-01`. Capital `$100,000`. Allocation `0.65`. Cash yield `0.045`. `-min-trades 5`, `-top 10`. `-max-perms 20000` skips a huge generic grid in multi-strategy mode only. `streak-*` strategies loaded from `refdata/strategies.db` are excluded unless `-include-streak`.

A sweep prints the market databases it reads at startup, and the HTML report's Run group lists it as `Market DB`. Period sweeps with a limit axis load the hourly bars, so a sweep and `backtest` agree on fills.

```bash
./bin/gridsearch -list
./bin/gridsearch -strategy streak-voo-buy-tecl -top 20
./bin/gridsearch -strategy all -no-html
./bin/gridsearch -strategy voo-up3 -symbols-from data/reports/voo_up3_etf.db -top-cagr 10
```

`-force` redoes a strategy already marked done in `gridsearch_runs`.

`-period 1d,1w,1m,1q,1y` (any subset, default all five from `strategy_family_param`) sets the calendar periods a rotation period row such as `biggest-winner` is swept over. Each period reruns the row's SQL pipeline once and the results are labelled `Period-<p>/...` (column `period` of `gridsearch_results`). Example: `./bin/gridsearch -strategy biggest-winner -period 1m,1q,1y`. It has no effect on other strategies.

### gridsearch params

Print the parameter grid. No database and no simulation.

```bash
./bin/gridsearch params streak-voo-buy-tecl
./bin/gridsearch params all
```

### gridsearch stale

Report which completed sweeps are stale. No new sweep.

```bash
./bin/gridsearch stale
```

### gridsearch promote

Copy winning sweep rows into `refdata/strategies.db` table `streak_strategy`. Each row becomes a strategy id `streak-<signal>-<up|down><days>-<trade>` with the watch symbol, the symbol bought, and the swept hold, take-profit, stop, and regime. Backtest, scoreboard, livescan, and strateval load those rows on the next run.

```bash
./bin/gridsearch promote -strategy voo-up3 -min-win-rate 0.6 -min-trades 30 -top 5
```

Defaults for promote are win rate `0.6`, `30` trades, and `-top 5`. A sweep's own `-min-trades` default stays `5`. `-gridsearch-db` chooses the sweep file. Rows that share an id keep the higher win rate. A NULL `signal_symbol` (sweeps from before that column existed) uses the parent strategy's watch symbol.

The walkthrough for writing a row, backtesting it, sweeping it, and ranking it is [docs/strategies/streak_strategy.md](strategies/streak_strategy.md).

### gridsearch persymbol

Tunes the rotation parameters of every symbol with enough history on its own. Each symbol runs as a one-name rotation row (100% positions, no cash yield) over the family grid (period, buy limit, hold, take profit, stop). The pick in sample is the setting whose resilience score, averaged with its neighbours one step away on each axis, is highest, so a plateau beats a lone spike, with at least 4 trades per year of the in-sample window (about 20; `-min-trades` overrides). The chosen setting and the shared base setting (1w, limit 90%, hold 2d, take profit 5%, no stop) are then read from a sweep over the held-out months. Results go to table `persymbol_results` in the run's `gridsearch.db`, and the summary prints the median in-sample and held-out CAGR of tuned against base.

```bash
./bin/gridsearch persymbol                      # every ETF with 1000+ sessions
./bin/gridsearch persymbol -symbol SPXL,UPRO    # just these
./bin/gridsearch persymbol -write-rows          # store the passing symbols
```

Flags: `-min-sessions` (1000), `-all-symbols` (not only ETFs, flagged `is_etf` in `refdata/universe.db`), `-write-rows`. A symbol passes with at least 4 trades per year of the held-out window, a held-out profit and a better held-out CAGR than the base; `-write-rows` stores those as `rotation_strategy` rows `persym-<symbol>` in `refdata/strategies.db`.

### gridsearch apply

Re-run strategies with the highest-resilience row the sweep recorded in the run folder's `gridsearch.db` (`hold_days` must be set, older rows without it are ignored). It goes back into the run that holds the sweep: `-run-id N` names it, and without it the latest run is used. No `-strategy` means every registered strategy. A strategy with no usable sweep runs on its own defaults and is named in the output. The backtest itself is `pkg/backtest`'s, so the holdout and the result files work as for `backtest`.

```bash
./bin/gridsearch apply -run-id 12 -strategy streak-voo-buy-tecl
./bin/gridsearch apply -run-id 12
```

`backtest optimized` is the deprecated name.

---

## park_sweep

Run every row of `streak_strategy` and `markov_strategy` with leftover cash parked in one symbol. The lists, the window, and the results stay in SQLite. The park symbol for the default config is GOOGL.

**Reads:** `refdata/strategies.db` (`streak_strategy`, `markov_strategy`) and `data/market_history_hourly.db`.

**Writes:** `data/reports/park_googl.db` (`sweep_config`, `park_asset`, `sweep_strategy`, `strategy_run`). Nothing is inserted into the settings tables, and no per-strategy file is written under `data/reports/`.

```bash
./bin/park_sweep seed
./bin/park_sweep run
./bin/park_sweep rank
./bin/park_sweep report
./bin/park_sweep -strategy streak-googl-down3-googl,markov_model_googl seed   # compare only these rows; put the same -strategy on run, rank and report
```

`-strategy a,b,c` limits seed, run, rank and report to those ids (case-insensitive). Only `streak_strategy` and `markov_strategy` ids qualify, and any other id is printed and skipped. `report` writes `data/reports/park_googl_report.db` and `data/reports/park_googl.html` from the sweep. The database holds every strategy row. The page shows the park buy-and-hold, every done streak, and the 25 Markov rows with the highest edge.

`sweep_config` holds the window (`2021-10-01` through `2026-10-01`), capital (`100000`), and park symbol (`GOOGL`). `allocation_pct` NULL uses each row's own allocation. A symbol that does not cover the window is stored as `skipped`. `run` retries `failed` and `running` rows and leaves `done` rows. `rank` prints whatever is already `done`. `-db`, `-market-db`, `-settings-db`, and `-concurrency` override the defaults.

---

## scoreboard

Backtest every registered strategy that lacks a usable result, then rank them.

**Reads:** `data/market_history_hourly.db` (`backtest_start`) and existing `data/reports/<id>[_N].db`.

**Writes:** per-strategy `data/reports/<id>[_N].db` for anything it has to run, then `data/reports/scoreboard.db`. If that file is locked it writes `data/reports/scoreboard_N.db`. Capital `$100,000`, start `2021-01-01`, download horizon 5 years. No `-db` or `-out-dir` flag; both follow `APP_FOLDER`.

```bash
./bin/scoreboard
./bin/scoreboard -force
./bin/scoreboard compile
./bin/scoreboard status
./bin/scoreboard -force -strategy streak-googl-down3,googl_tree   # compare only these (ids, a family name, or all)
./bin/scoreboard compile -strategy streak-googl-down3,googl_tree
./bin/scoreboard -detail 100   # faster bulk run: daily equity curves only for the 100 best by CAGR
```

`-strategy` takes comma-separated ids or a family name (`streak`, `hold`, `tree`, `markov`) and limits the backtests, the comparison table and `scoreboard.db` to those strategies. With a subcommand, put it after the subcommand. `compile` only reads result DBs. `status` reports whether every registered strategy has a usable result and does not write `scoreboard.db`.

`-detail N` (default `-1`: every strategy saves its daily equity curve) saves the curve of only the N best strategies of the run by CAGR, after the bulk pass; the others still save trades, signals and the performance summary, so the ranking is the same. The curve is one row per session, and writing it is the one step of a bulk run that cannot run in parallel (one writer per result database), so `-detail 100` cuts the wall time of a large run noticeably. A strategy without a saved curve can be rerun with `backtest -strategy <id>`.

---

## study

Run one research study.

**Reads:** market DB (`-db`).

**Writes:** `data/reports/<study-id>.db`.

```bash
./bin/study -list
./bin/study -study market_context_20d
./bin/study -study hmm_regime -symbol QQQ
./bin/study -study gain_5pct_frequency
./bin/study -study voo_up3_etf
```

Registered ids: `hmm_regime_etf`, `market_context_20d`, `cluster_5pct`, `googl_market_context`, `gain_5pct_frequency`, `hmm_regime`, `etf_study`, `sp500_lead_lag`, `market_clustering`, `qqq_tqqq_volume`, `voo_up3_etf`, `mara_decision_tree`, `mu_decision_tree`, `march_april_voo_gld_uten`.

`hmm_regime_etf` (`./bin/study -study hmm_regime_etf [-symbol QQQ]`, about 2 minutes) splits every active ETF's next-session return by the regime the saved HMM had at the previous close, with regimes ranked by volatility. It reads `data/hmm_models.db` and `refdata/universe.db`, drops sessions with a close ratio past 1.8x as unadjusted splits, and writes `data/reports/hmm_regime_etf.db` (`hmm_regime_etf`, `hmm_regime_top` (the top 10 ETFs per regime, with `MinRegime` 100 sessions), `hmm_regime_pick` (top 10 picked in-sample, read held-out), `hmm_regime_etf_info`, `etf_dropped`; recreated on each run). Per ETF and sample (`all`, `in`, `oos`): mean and log return per regime, the Welch t of not-stressed against stressed, the share of variance explained, the volatility ratio, and the growth of holding only after not-stressed closes. The SQL is `sql/stages/hmm_regime_etf`. It needs a three-state model.

`cluster_5pct` and `googl_market_context` need a cluster database supplied in code (`SetClusterDB`). The CLI does not pass one, so those two exit until a caller sets it. `cluster_5pct` also expects `data/gain_5pct_frequency.db` beside the market DB.

---

## strateval

In-sample / out-of-sample ledger. It does not edit `STRATEGY_ALLOWLIST` or live jobs.

**Reads:** `data/market_history_hourly.db`, table `backtest_start`. Allowlist from `-allowlist` or `STRATEGY_ALLOWLIST` (comma-separated ids).

**Writes:** `strategies.db` (`strategies`, `strategy_evals`, `deployments`) in a run folder, `$APP_FOLDER/data/reports/<run_id>/`, unless `-db` names another file. `-run-id N` uses run N and without it an evaluation starts a new run (`report`, `status`, `path` use the latest). `report` and `status` take `-strategy` to show only those ids. Scratch artifacts under `strateval_runs/` in the same folder. Held-out window `-oos-months 12`. Capital `$100,000`. Start `2021-01-01`. Tier A gates: at least 12 OOS trades, win rate 0.55, max drawdown 0.15.

```bash
./bin/strateval -list
./bin/strateval -strategy streak-voo-buy-tecl
./bin/strateval -strategy all -optimize -max-trials 50
./bin/strateval report
./bin/strateval status
./bin/strateval sync-deployed -allowlist "$STRATEGY_ALLOWLIST"
./bin/strateval path
```

`path` prints how to open the ledger. `sync-deployed` snapshots the allowlist into `deployments`. `-sync-deployed` on an eval run does the same snapshot after the eval.

---

## pipeline

Runs every stage for a set of stocks as one controlled run: download the bars, train, backtest, sweep and tune, validate, score, stack, and compare. It calls the same code as the commands above, in order, and keeps the whole run in one folder.

```bash
./bin/pipeline -symbol GOOGL                      # every stage for GOOGL, in a new run
./bin/pipeline -symbol GOOGL,AAPL -skip-network   # two stocks, no downloads
./bin/pipeline -symbol GOOGL -strategy streak-googl-down3,googl_tree
./bin/pipeline -family streak -limit 50 -skip-network -skip park_sweep   # the first 50 streak rows
./bin/pipeline -run-id 17                         # resume run 17 and skip the steps it finished
./bin/pipeline -steps backtest,validate -run-id 17 -redo
./bin/pipeline -list-steps
```

**Scope.** The run may touch only the symbols and strategies it was started with. Without `-strategy` that is every strategy row tied to the symbols (as signal or trade symbol, from `streak_strategy`, `tree_strategy`, `markov_strategy` and `hold_strategy`), and rows the `gridsearch_promote` step writes for those symbols join it. With `-strategy` the list is fixed. The scope is stored in `pipeline.db` and a resumed run reads it back: naming different symbols for an existing run is refused. Every step gets that list, so nothing else is backtested, swept, validated or ranked.

**Run id.** A new run takes the next folder number from `storage.NewRun`, which makes the folder atomically, so two runs never share an id. Inside the folder a `pipeline.lock` file holds the process id, and a second pipeline on the same run is refused while the first is alive (a lock left by a dead process is taken over).

**State.** `pipeline.db` in the run folder has `pipeline_run`, `pipeline_scope` and `pipeline_step` (name, status, times, error). A failed step is recorded and the run goes on. The exit code is 1 if any step failed, and `pipeline -run-id N` retries the failed ones.

**Downloads.** The pipeline downloads only what the scope reads: the run's symbols, every symbol its strategies declare, and each strategy's benchmark (the same `runner.RequiredSymbolsFor` the backtest uses), plus the park symbol when `stack_eval` is going to run. A symbol already in the market database is topped up with the bars it is missing, and one that is current is not fetched at all. Option history is downloaded only for the underlyings of covered-call strategies in scope, which no GOOGL strategy is. Steps with nothing to do for the scope are skipped with the reason printed: `market_history_options` (no covered-call strategy), `study` (no `markov_hmm` strategy), `train` (no markov or tree strategy), and `covered_call`, which only runs when you name it. Naming a step in `-steps` always runs it.

**Steps**, in order, each named like its command: `market_history`, `market_history_options` (needs `POLYGON_API_KEY`), `study` (`hmm_regime`), `train` (markov and tree), `backtest`, `gridsearch_params`, `gridsearch`, `gridsearch_promote`, `gridsearch_apply`, `validate`, `strateval`, `stack`, `stack_eval`, `covered_call` (needs option bars), `park_sweep`, `scoreboard`, `stale`. `-steps` and `-skip` choose some of them.

| Flag | Default | Meaning |
|---|---|---|
| `-symbol` | GOOGL (a resumed run keeps its own) | stocks the run may touch |
| `-strategy` | every strategy tied to the symbols | fixed strategy list |
| `-family` | none | scope to the rows of one strategy family (`streak`) instead of `-symbol`; the symbols are the ones the rows read and trade, the list is fixed, and with no primary the stack steps skip |
| `-limit` | 0 (every row) | with `-family`: only the first N rows by id |
| `-park` | SGOV | park symbol of the `stack_eval` step, downloaded only when that step runs |
| `-primary` | `streak-<symbol>-down3-<symbol>` | primary of the stack steps |
| `-years` | 6 | years of bars to download for a symbol that has none yet |
| `-refresh` | off | download the whole window again for the symbols the run needs, not only the missing bars |
| `-run-id` | 0 (new run) | resume this run |
| `-steps`, `-skip`, `-redo` | all steps | which steps run, and rerun finished ones |
| `-skip-network` | off | leave out the downloading steps |

`scripts/googl_pipeline.sh` is a one-line wrapper for `pipeline -symbol GOOGL`. Not yet in the pipeline: `markov_test` (its logic is still in `cmd/`) and `universe` and `livescan`, which are not part of building a strategy. `hmm_regime.db` is still written to `data/reports/` and shared between runs, because the `markov_hmm` strategies read it from there. `park_sweep` uses its own configured park symbol (GOOGL).

## backtest export_views

Writes the indicator calculations as SQL views, and a new database holding those views and only the market history they read. It writes new files only; it never writes the market database, and it touches no broker or account.

```bash
./bin/backtest export_views -strategy streak-voo-up5-tqqq   # data/reports/streak-voo-up5-tqqq.db and .sql
./bin/backtest export_views -symbols VOO,TQQQ               # the shared indicator views over those two symbols
./bin/backtest export_views -dry-run -strategy <id>         # list the views, write nothing
```

The database has `backtest_start` (same columns and indexes as the market table) with only the **daily session rows** (`length(Date) = 10`) of the **relevant symbols**, plus the views. Hourly bars are left out because no view reads them. The relevant symbols are the strategy's own (a streak row: its watch and trade symbols), or `-symbols`. With neither, every symbol's daily bars are copied.

With `-strategy <id>` the views are that strategy's own signal pipeline (`sql/strategies/...`) with the row's parameters filled in, named `v_<id>_slice`, `v_<id>_signals`, and so on (dashes in the id become underscores). They are the pipeline's own SQL, so they return what a run puts in its slice tables. Streak strategies are checked against a pipeline run in a test; rotation strategies create their views but are not compared to a run. Hold, tree and markov strategies are refused with the reason, because they read a trained model or attached database.

Without `-strategy` the views are the shared indicators (definitions in `sql/views/`): `v_daily_bars`, `v_bar_sma`, `v_streak_diff` / `v_streak_group` / `v_streak_slice`, `v_tree_base` / `v_tree_chg` / `v_tree_win` / `v_tree_features`, `v_avg_volume` (20, 50 and 200 sessions), `v_session_ratio` / `v_split_jumps`. Filter by `symbol`: every window partitions by symbol alone so SQLite searches the index.

Flags: `-strategy`, `-symbols`, `-db` (market DB, default `appenv.MarketDB()`), `-db-out` (default `auto`: `data/reports/views.db`, or `<id>.db` with `-strategy`; `none` skips it), `-sql-out` (the script; same `auto` and `none`), `-dry-run`. An existing `-db-out` file is replaced.

## validate

Is the strategy real or overfit? Walks it through rolling train / test folds (24 months in sample, 6 out, step 6), then prints a verdict per strategy: `HOLDS`, `DECAYS`, `CURVE_FIT`, or `INSUFFICIENT`.

```bash
./bin/validate -keep-going -strategy streak-googl-down3,googl_tree    # folds, then the verdict
./bin/validate walk -strategy streak-googl-down3                       # only the folds
./bin/validate verdict -run-id 12 -strategy streak-googl-down3         # only the verdict, from run 12
```

**Reads:** `data/market_history_hourly.db`, table `backtest_start`.

**Writes:** `walk_forward.db` (`walk_forward_fold`, `walk_forward_summary`, and `check_overfit_gate` with the gates) in a run folder, `data/reports/<run_id>/`. `-run-id N` goes into run N. Without it the folds start a new run and a verdict alone uses the latest. `-db` names a file instead. `-strategy` is required for the folds, and for a verdict it limits the output to those ids.

Folds: train 24 months, test 6, step 6 (`-train-months`, `-test-months`, `-step-months`), capital `$100,000`. It registers the same strategies as `backtest` and walks them one at a time, not as a stack. `-keep-going` skips a strategy with too little history instead of stopping the list. `-market-db` lets a run use a copy of the market DB that ends before a held-out period.

Verdict gates: `-min-oos-trades 8`, `-trial-cutoff 20`, `-decay 0.25`. A streak strategy has one trial, so it is never `CURVE_FIT` however many were screened. After screening thousands on the same data, add a stricter gate such as `sql/search/01_gate_walk_forward.sql`.

`walk_forward` and `check_overfit` still work as the two halves (`validate walk`, `validate verdict`) and print a deprecation note. The logic stays in `pkg/backtest/walk_forward` and `pkg/backtest/check_overfit`.

---

## universe

Discover US stocks and ETFs from Polygon and the Nasdaq lists, and classify them (leverage, direction, category, first trade date, whether history reaches 2021).

**Writes:** `refdata/universe.db`, table `universe` (about 13,500 rows). Needs `POLYGON_API_KEY` or `-polygon-key` for the Polygon part.

```bash
./bin/universe
./bin/universe -etfs-only -max-checks 200
./bin/universe -refresh               # look every symbol up again
```

Each run fetches the symbol lists, then looks up only the symbols it has not settled. A symbol with a stored first trade date is skipped, since that date never changes, and one that could not be verified is tried again after `-retry-days` (7). A run with nothing new makes no Yahoo requests. `-refresh` looks up everything again.

Flags: `-db`, `-polygon-key`, `-workers 16`, `-limit 1000`, `-max-checks 0`, `-etfs-only`, `-stocks-only`, `-refresh`, `-retry-days 7`.

### universe reclassify

Relabels `leverage` and `direction` of every stored symbol from its stored name with the current classifier, with no network calls. `-dry-run` lists the changes without saving. Run it after the name rules in `pkg/backtest/universe` change; `universe` itself only classifies the symbols it looks up.

```bash
./bin/universe reclassify -dry-run
./bin/universe reclassify
```

### universe avgvol

Average daily share volume (ADTV) of every symbol in the market database, over one or more trailing windows of daily bars.

```bash
./bin/universe avgvol                 # 20-day window
./bin/universe avgvol -days 20,60     # two windows
```

**Reads:** `data/market_history_hourly.db` (`-market-db`), table `backtest_start`, daily bars only.

**Writes:** table `avg_volume` in `refdata/universe.db` (`-db`): `symbol`, `window_days`, `avg_volume`, `bars_used`, `first_date`, `last_date`, `computed_at`, one row per symbol and window. A symbol with fewer bars than the window is averaged over the bars it has and `bars_used` says how many. A rerun replaces the rows of the windows it computes and leaves other windows alone. The calculation is SQL (`sql/stages/avgvol`): a slice table `avgvol_window_bars` of each symbol's last N bars, then the average. Join to `universe` on `symbol` to filter by liquidity.

## train

`train <family> [flags] [symbols]` trains a strategy family's model and saves it in SQLite. `backtest` only reads a saved model; it never trains one, so run this first, and again when the market data has advanced. Every model trains on the daily bars in `data/market_history_hourly.db`, and the command prints the last bar date it trained through.

| Family | Result |
|---|---|
| `markov` | trains the per-symbol Markov regime model into `data/markov_models.db` (`markov_prediction`, `markov_model_meta`) |
| `tree` | trains a depth-3 CloudForest decision tree per signal symbol into `data/tree_models.db` (`tree_node`, `tree_model_meta`) |
| `streak`, `hold` | nothing to train: these strategies have no fitted model, their parameters are the row's columns in `refdata/strategies.db` |
| `all` | every family above, in name order |

`markov_hmm_*` strategies read `data/reports/hmm_regime.db`, which `study hmm_regime` writes; that is a study, not a `train` family.

**markov.** A bar is bull at +5% or more over 20 bars, bear at -5% or less, otherwise sideways. For each date the model stores the walk-forward chance that the next bar is bull or bear, using only transitions known by that date. The calculation is SQL (`sql/stages/markov_train`, one slice table per stage). **Reads:** `data/market_history_hourly.db`, and `refdata/strategies.db` (`markov_strategy.signal_symbol`) when no symbols are given. Symbols already in the model are replaced. A symbol with fewer than 21 bars gets no model. A markov strategy whose signal symbol has no model produces no signals and logs which `train markov` command to run.

**tree.** Each bar is labelled with the next bar's return in 5 buckets: class -2 at or below -5%, -1 above -5% and below -1%, 0 from -1% to 1% inclusive, 1 above 1% and below 5%, 2 at or above 5%. The features are 13 values per bar (returns over 1, 3, 5 and 10 bars, RSI, distance from the 20, 50 and 200 bar averages, volume ratio, range against ATR, close within the day's range, consecutive down closes), calculated in SQL by `sql/stages/tree_features`. The neutral class usually dominates, so the tree is grown on every bar of the four other classes plus an evenly spaced sample of neutral bars no larger than the biggest of those four, with the classes weighted to balance the grown set. A symbol needs 250 labelled bars (the first 200 bars of history are warm-up) and at least 10 bars of class 2, otherwise it is skipped and the reason is printed. The tree is stored one row per node, addressed by CloudForest's L/R path from the root. A `tree_strategy` row buys its trade symbol on every bar the saved tree of its signal symbol predicts class 2; a row whose signal symbol has no saved tree produces no signals and logs which `train tree` command to run. Its `coil_range_max`, `sma_bounce_min` and `sma_bounce_max` columns are no longer read (role `legacy` in `strategy_family_param`). **Reads:** `data/market_history_hourly.db`, and `refdata/strategies.db` (`tree_strategy.signal_symbol`) when no symbols are given. The last bar of a symbol has no next return, so it is scored but not trained on: the "through" date printed is the last labelled bar.

```bash
./bin/train markov                      # every signal_symbol in markov_strategy
./bin/train markov GOOGL AAPL           # just these
./bin/train markov -symbols GOOGL,AAPL -batch 100
./bin/train tree MARA NVDL             # just these
./bin/train streak                      # says there is nothing to train
```

Flags for `markov` and `tree` (`tree` has no `-batch`): `-db`, `-model-db`, `-ref-db`, `-symbols`, `-batch 200`, `-calc-dir` (keep the last batch's slice tables). Both take `-through <date>`, which learns only from bars up to that date so the later months stay out of sample, and `markov` also takes `-from <date>`, the first session it may learn from. With `markov -through`, later sessions are still scored, with the counts frozen at that date, and `markov_model_meta.trained_through` records it; that is the model a live scan extends, so train with the backtest's in-sample end.

**`slippage_pct` (every row family).** An adverse price move applied to each fill, as a fraction of the fill price: a long entry pays `price * (1 + slippage_pct)`, and every exit (take-profit, stop, time-up, signal) sells at `price * (1 - slippage_pct)`. A short is the mirror image. It is charged once per side, so a round trip costs about twice the value. It stands in for the bid/ask spread and market impact on a limit or market fill. It is not a commission (that is `CommissionPerShare`) and not a financing cost (`cash_yield`). `0.001` is 0.1% per side. Code: `slip()` in `pkg/backtest/sim/run.go`.

## strategy

Print every strategy that has its own `sql/strategies` pipeline, then a row count per strategies.db table. Rows are not listed one by one. No flags.

```bash
./bin/strategy
```

### strategy coverage

Report how complete the bar history is for the symbols a strategy needs, so a backtest on thin data is caught before it is trusted.

```bash
./bin/strategy coverage -id rotation-2x-sector-pairs-daily-limit95-tp3-sl5-hold2-pos20
./bin/strategy coverage -symbols SPY,QQQ -start 2023-01-01
```

`-id` takes strategy ids and `a+b+c` stacks and reports every symbol they need (candidates, regime symbol, benchmark); `-symbols` adds more. For each symbol it prints first and last session with a bar, sessions expected, sessions present, missing, the longest run of missing sessions and the coverage percent. A session is a day any of the listed symbols (or SPY) traded, counted from the symbol's first to its last session, so a later listing is not a gap. A session is present when the symbol has at least one regular-session hourly bar, and the table adds the average bars per covered day (a full session is 7). A day with no trade in any hour looks the same as a missing download, which is where the thin names show gaps. Flags: `-start` (default `2021-01-01`), `-end`, `-db`.

### strategy defaults

Make one row of a sweep the stored defaults of a strategy: its hold days, take-profit and stop-loss. Use it on a row-backed strategy (streak, hold, tree, markov, hmm or rotation) after `gridsearch`, with the `label` of the chosen `gridsearch_results` row, exactly as `gridsearch` prints it.

**Writes:** `hold_days`, `take_profit_pct` and `stop_loss_pct` of that strategy's row in `refdata/strategies.db`, and then the table's seed file in `sql/seed`. Live by default. `-dry-run` shows the change and writes nothing.

```bash
sqlite3 -header data/reports/12/gridsearch.db "select label, cagr from gridsearch_results order by cagr desc limit 5"
./bin/strategy defaults -label "TECL/Hold-8d/TP+0%/SL-2%" -gridsearch-db data/reports/12/gridsearch.db -dry-run
./bin/strategy defaults -label "TECL/Hold-8d/TP+0%/SL-2%" -gridsearch-db data/reports/12/gridsearch.db
```

The strategy is the one the sweep row belongs to (`gridsearch_results.strategy_id`). Add `-id` only when the sweep holds the same label for more than one strategy, or to check the row is for the strategy you expect: the label must belong to `-id` (ids match loosely on case, `-` and `_`) or the command refuses. A label that appears more than once for the strategy with different parameters is refused too. After the write it recreates the table's seed file from the database (the same dump as `strategy seed`, so the seed never holds the old values): `sql/seed/<table>.sql` if that file exists, or the `-seed` path, whatever was in it replaced. `-no-seed` skips this, and `-dry-run` writes neither. A table with no seed file gets none unless `-seed` is given. Re-export the bundle afterwards if the strategy is deployed. Flags: `-label` and `-gridsearch-db` (required), `-id`, `-seed`, `-no-seed` (optional), `-ref` (default `refdata/strategies.db`), `-dry-run`.

### strategy seed

Write a table's schema and rows as a seed SQL file, so a seed is made from the database and never edited to match it. It is the equivalent of the `sqlite3` `.schema` and `.dump` for one table, with rows as `INSERT OR REPLACE` ordered by `id`, so running the file twice changes nothing.

**Writes:** `sql/seed/<table>.sql` (or `-out`). It refuses to replace a file that exists unless `-force`, because a hand-written seed has comments the dump does not keep. `-dry-run` prints the seed to the terminal and writes nothing. It reads the database and never changes it.

```bash
./bin/strategy seed -table hmm_strategy -dry-run
./bin/strategy seed -table hmm_strategy -ids hmm-normal-tecl,hmm-calm-soxl -out sql/seed/hmm_strategy.sql -force
```

Flags: `-table` (required), `-ids` (only these rows), `-out`, `-ref` (default `refdata/strategies.db`), `-force`, `-dry-run`. The table needs an `id` column. It does not write the `.duckql` twin. Dump a table right after `strategy defaults` or any other change, so the seed matches.

### strategy derive

Add a rotation strategy that is a copy of an existing one with the parameter set of a sweep result. The parameter set is the label `gridsearch` prints, alone or inside the whole result line.

**Writes:** one new row in `refdata/strategies.db` table `rotation_strategy`.

```bash
./bin/strategy derive -from rotation-2x-sector-pairs-daily-limit90 \
  -id rotation-2x-sector-pairs-daily-limit95-tp3-sl3 -name "2x Sector Pairs Daily 95% Limit TP3 SL3" \
  "#1  Period-1d/Limit-95%/Hold-2d/TP+3%/SL-3%   Profit=+$258505.67  CAGR=37.73%"
```

| Flag | Default |
|---|---|
| `-from` | required: the rotation strategy id to copy |
| `-params` | the parameter set, or bare arguments after the flags |
| `-position-pct` | none. The size of each position as a fraction of portfolio value for a `pick = all` row (`0.20` is 20%); it sets `allocation_pct` and `max_weight_pct` together. Alone it copies the row with only that change, and the parameter set becomes optional |
| `-id` | the source id without its `-limit..` suffix, then `-limit95-tp3-sl3-hold2` (and the period when it is not `1d`) |
| `-name` | the source name with the parameter set |
| `-ref` | `refdata/strategies.db` |

The copy sets the period, buy limit, hold days, take profit and stop from the label and keeps every other column, the symbol list by its `symbol_lists` id included. The label holds whole percents (the sweep prints `%.0f`). An id that exists, an unknown source, or a parameter set the row cannot run (a stop of 100%) is refused and writes nothing. The command prints the `backtest` line to run it.

### strategy export

Write a small reference database holding only the rows of the named strategies, for a program that cannot carry the 65,000-row `refdata/strategies.db`.

```bash
./bin/strategy export -ids "streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull" \
  -out pkg/orchestrator/strategies/deploy/strategies.db
```

`-ids` takes strategy ids and `a+b+c` stacks, comma-separated. Every id must be a row in `streak_strategy`, `tree_strategy`, `hold_strategy` or `markov_strategy`, or the command fails and names the ones that are not. `-src` chooses the full database. `-alloc 0.20` sizes every exported row at that fraction of equity per position and records it in the bundle's `bundle_meta` table (`alloc_pct`); give it the `-alloc` the backtest ran with, because a live position is sized from the row and nothing else carries the backtest's allocation across. `backtest stack -alloc 0.20` is flat: every position is 20% of equity, and a signal's own allocation (the Markov pipeline's 5 to 30% by signal strength) is ignored in the run (`StrategyConfig.FlatAllocation`), as it is live. `trade_orchestrator` embeds the output and reads it where it has no reference database.

A strategy that needs something built from the bars before it can signal implements `strategy.Preparer`, and a live scan (`livescan`, `runner.RunLiveScan`) calls it after refreshing the bars. `markov_strategy` brings its saved model up to the last completed session when it is behind, because the model holds one prediction per date and has none for a session it was not trained through. It does not refit: `train.ExtendMarkov` (stage `sql/stages/markov_extend`) scores each new session from its 20-bar return with the counts frozen at the model's `trained_through`, the same way an out-of-sample backtest scores the sessions after its cutoff, so live and out-of-sample agree and a scan needs about 25 closes per symbol, not years. A symbol with no saved model is an error that names the command (`train markov -symbols X`, then export the bundle); `Prepare` never trains one. A failed `Prepare` stops the scan with `PREPARE_FAILED`, and a missing model never passes as "no signal".

## cleanup

One command for tidying `data/reports/`, built on `prune_losers` and `clear_older` (both still work). Every mode takes `-dry-run`. Workers default to 32 and are kept between 10 and 32.

```bash
./bin/cleanup report                 # one line per run folder: size, lock, step statuses, notes
./bin/cleanup runs -dry-run          # dead locks, aborted steps, empty run folders
./bin/cleanup runs -failed           # also delete runs where no step finished (never one a strategy's latest backtest lives in)
./bin/cleanup sql                    # sql/ files no .go, .md or .sh mentions (report only; -remove-sql deletes)
./bin/cleanup all -dry-run -keep "$STRATEGY_ALLOWLIST"
./bin/cleanup all -yes -vacuum -keep "$STRATEGY_ALLOWLIST"
```

`runs` removes a `pipeline.lock` whose process is gone, turns steps stuck in `running` into `failed` (so `pipeline -run-id N` retries them) and deletes empty folders. A folder held by a live pipeline is never touched. `all` runs, in this order: `runs`, losing strategies (`prune_losers`, only when `-keep` names the greenlit ids), orphan rows (`prune_losers orphans`, empty result DBs deleted, `-vacuum` for mostly-free files), old versions (`clear_older`), then lists unreferenced `sql/` files (never deletes them) and runs `backtest lastrun`. `sql` matches names in the repo and sibling repos, so a name built at run time shows up as a candidate: read the list before `-remove-sql`. `all` refuses to delete without `-yes`. Not undoable: copy `data/reports` first. The `untrainable` and `symbols` prunes are not part of `all`; run them with `prune_losers`.

## prune_losers

Remove what does not earn its place. `-dry-run` on every mode reports without changing anything.

```bash
./bin/prune_losers -dry-run                       # strategies whose latest backtest has win_rate < 0.5 (or no trades)
./bin/prune_losers untrainable -dry-run           # tree strategies and models that cannot be trained before the holdout cutoff
./bin/prune_losers orphans -dry-run                 # result-DB rows of strategies that no longer exist
./bin/prune_losers symbols -strategy rotation-2x-sector-pairs-daily-limit95-tp3-sl3 -dry-run
```

The first two delete rows from `refdata/strategies.db`.

### prune_losers orphans

Delete, from every result DB under `data/reports/` (`*.db`, `<run>/*.db`), the rows of strategies that no longer exist: `strategy.Get` finds no such id, or for a stack `a+b` finds no such member. Every table keyed by `strategy_id` is cleaned (`combined_id` for the shared-account tables). A DB without `performance_summary` (gridsearch, walk-forward) is left alone. The strategies come from `-db` (default `refdata/strategies.db`) and `sql/strategies/` under `-root`, so prune strategies first. `-dry-run` counts only. Deleted rows leave the file the same size until `-vacuum` rewrites it (slow on big files); a result DB with no backtest left is deleted unless `-remove-empty=false`. `-workers` defaults to 32 and is kept within 10 to 32. Run `backtest lastrun` afterwards to refresh `strategy_last_backtest`. Not undoable: copy `data/reports` first.

### prune_losers symbols

Take the losing names out of a rotation strategy's symbol list. A name is a loser only when all three hold: it lost money in the in-sample backtest, it lost money in the held-out backtest, and it won fewer than `-min-win-rate` (default `0.4`) of its trades over both windows. A name with no trades in either window is never removed. The results are the strategy's latest run in a run folder, `rotation.db` (in-sample) and `oos/rotation.db` (held out), the latest folder holding both unless `-run-id N` names one.

The strategy is pointed at a new symbol list, the old id plus `-pruned` (`etf-2x-sector-pairs` becomes `etf-2x-sector-pairs-pruned`). The list it used is not changed, because other strategies may use it. A strategy with a literal ticker list has the names removed from that list. Run the strategy's `backtest` again to see the effect; the result files of the earlier run still hold the removed names, and the command prints the table it decided from.

---

## markov_test

Print the empirical bear / sideways / bull transition matrix and tomorrow's probabilities for GOOGL (20-day return, plus or minus 5% thresholds). No flags, no writes. See [docs/MarkovModel.md](../archive/MarkovModel.md).

```bash
./bin/markov_test
```

## Stack candidates skill

`.claude/skills/stack-candidates/` is a Claude Code skill that runs `train`, `backtest`, `gridsearch` and `strateval` as needed and reports the 20 best strategies across all families to try in a stack. `top_strategies.sh <run_dir> [n]` does the ranking on its own: the lower of in-sample and held-out Calmar, with gates on trades, profit and held-out drawdown, one strategy per trade symbol and at most 8 per family.
