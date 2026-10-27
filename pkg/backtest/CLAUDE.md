# backtestgosqlite (BT)

Go backtester. Bars, signals, trades and reports live in SQLite. Module `github.com/darianmavgo/backtestgosqlite`, pure-Go SQLite (`modernc.org/sqlite`, no cgo).

The cross-repo rules (ownership, one implementation for CLI/web/GAE, naming, no mocks) are in `../CLAUDE.md` and apply here in full. This file holds only what is specific to BT. Read `README.md` for command usage and `Architecture.md` for the package map; do not duplicate them here.
## Tool Swaps
replace sed with sd
replace find with fd
replace grep with rg

## Commands

Run from the repo root so `.env` and default paths resolve.

```bash
make build            # every cmd/* into bin/
make test             # go test ./pkg/... ./cmd/...
go build ./... && go vet ./... && go test ./...   # required before saying it works
go test ./pkg/strategy -run TestName              # one test
./bin/backtest -strategylist                              # registered strategy ids
```

- Flags go before positional args (Go `flag` stops at the first bare arg). A subcommand is arg 1: `backtest stack -primary ...`.
- `data/market_history_hourly.db` (table `backtest_start`) and `refdata/strategies.db` are real data. Tests and experiments must not write to them; use `t.TempDir()` databases or `data/reports/`. For a search or experiment on trimmed data, build a copy of the market DB and pass `-db` (and `-out-dir`).
- Paths come from `pkg/appenv`. Only `APP_FOLDER` is configurable; `data/`, `refdata/` and `data/reports/` are fixed subfolders (`APP_DATA`, `APP_REF` and `APP_REPORTS` no longer exist). Do not hardcode `data/...` or `reports/...` in Go, including flag defaults; call `appenv.MarketDB()`, `appenv.ReportFile(name)` or `cliutils.GetDefaultMarketDB`.

## Layout rules

- Signals are calculated in SQL and Go only runs them: a strategy builds its config, calls `strategy.RunPipeline` with a `sql/strategies/<name>/` directory and returns the result. Do not add a Go loop over bars that decides entries, and do not add an indicator function in Go.

- No shared or lasting scratch tables. A pipeline's slice and signal tables live in one strategy's own scratch database, created fresh for each run (`runner.freshCalcDB`), and are never shared between strategies or kept between runs. Stage SQL must not rely on `CREATE TABLE IF NOT EXISTS` or `CREATE TABLE ... AS` to reuse a table that an earlier run or another strategy may have filled: that returns someone else's rows as this strategy's signals (it made every markov strategy in a scan report the first one's entry). A new scan or run path must give each strategy its own calc database and delete the old file first. Anything worth keeping (a trace, a prediction) goes to a named table in a real database.

- Backtests run on the hourly simulator: `pkg/hoursim` converts the signals, config and bars and calls `pkg/sim` (the engine, with a parked leftover-cash book, `sim.Park`). It is the only simulator: the daily-range one (`pkg/simulator`) is deleted, and a strategy that needs margin, ATR stops, Kelly sizing or dividends is refused with that reason (`hoursim.Supported`). Every stored run records its engine in `runs.engine` (`storage.EngineHourly`); a run with none predates the column and was made by the old daily simulator, so its same-session fills and exits are suspect and bulk commands redo it. Rows on a symbol with an unadjusted split (`sessions.SplitJumps`) are flagged in `runs.suspect` and left out of `scoreboard` unless `-include-suspect`. With no hourly bars a session is played as its daily bar (one bar, so nothing exits on the bar that entered it) and `hoursim` reports which sessions.

- `cmd/<name>/main.go` is a thin main that calls `pkg/<name>.Main` / `Run`. Flags, defaults and logic live in `pkg/<name>` so tests call `Run` without `os.Args`. Follow `pkg/backtest/main.go` and `pkg/livescan/main.go`.
- `pkg/strategy` holds the `Strategy` interface and registry (`Register`, `RegisterAlias`, `Get`; lookup is case- and punctuation-insensitive). Keys differ by `-`/`_`, so do not add a second id that normalizes to an existing one.
- Row-backed strategies (streak, hold, tree, markov, hmm, rotation) are not registered per row. Each table is one `strategy.Family` (`pkg/strategy/family.go`, `RowFamily`); `strategy.Get(id)` reads and builds the row on demand, with the same case/punctuation-insensitive match. `strategy.List()` returns only the strategies with their own pipeline; `strategy.ListAll()` also builds every row (use it only where "all" must mean every row). `pkg/stratreg.RegisterAll(root, db)` registers SQL pipelines plus the families; commands that only need the families call `stratreg.RegisterFamilies()`. Never loop over a table calling `strategy.Register`. Pick subsets with `stratlist` and a `.sql` file (`sql/lists/`).
- Every strategy is SQL controlled by Go. Most (about 65,600) are rows in `refdata/strategies.db` (`streak_strategy`, `hold_strategy`, `tree_strategy`, `markov_strategy`, `hmm_strategy`, `rotation_strategy`). Add or change those with SQL on the table, not Go. `etf_universe` is empty and the `etf_dt_strategies` / `dt_*` handling was deleted; the tree family is the one decision-tree family.
- `park-<symbol>` (`strategy.ParkStrategy`) is a stack member for the residual-cash book. It is never a sleeve: `strategy.SplitResidual` removes it and the runner passes its symbol as `DefaultAsset`. Do not give it signals.
- A live scan prepares strategies that need it: `strategy.Preparer.Prepare` runs after the bars refresh (markov scores the sessions after its saved model's last date with counts frozen at the model's `trained_through`; it never refits an existing model, only a symbol with no model is trained). `strategy export` makes the small reference database `trade_orchestrator` embeds. Keep `trade_orchestrator`'s allowlist rows in that bundle.
- `pipeline` (`pkg/pipeline`, `cmd/pipeline`) runs every stage for a set of symbols in one numbered run folder. Its scope (symbols and strategies) and step status live in `pipeline.db` in the run folder, and each step calls the package of its command as a Go function, never a separate process. Add a new stage as a step in `pkg/pipeline/steps.go`, not as a shell script.
- Other packages: `simulator` (portfolio and shared-account), `runner` (load bars, run, stack), `storage` (schemas, `OpenSQLite`, `ExecuteSQLFile`), `refdb` (strategies.db), `analytics` (metrics, HTML tear sheet), `calendar`, `transaction_calc` (IBKR CSV to report), `universe` (Polygon discovery into `refdata/universe.db`), `validate` (walk-forward folds and overfit verdict, built on `walk_forward` and `check_overfit`).
- `sql/seed/` holds the SQL that inserts family rows into `refdata/strategies.db` (`rotation_strategy.sql`, `hmm_strategy.sql`). `sql/studies/omnifunds_reverse/` is the scratch analysis behind the rotation family, run with `sqlite3` on scratch databases (its README lists the order).
- `sql/search/` holds the stack-search gate and liquidity screen as numbered slice-table stages. Run them with `sqlite3` against the walk-forward, market and settings DBs; there is no Go wrapper yet.
- Trained models stay out of `backtest`: `train markov` writes `data/markov_models.db` and `train tree` writes `data/tree_models.db`; strategies only read them (and log the train command when a model is missing). Do not move fitting back into a strategy or pipeline. Any new strategy family that learns something gets its own `train <family>` entry with the same shape.
- `pkg/hmm` fits the hidden Markov model with Baum-Welch in Go: EM is iterative and cannot be expressed as SQL slice tables, so it is a deliberate exception to "SQL does the calculation". The inputs (`sql/stages/hmm_inputs`) and the out-of-sample report (`sql/stages/hmm_report`) are SQL slice tables. `train hmm` is never part of `train all`, and `extend` never refits. The `hmm` strategy family (`pkg/hmm_strategy`, SQL in `sql/strategies/hmm_strategy`) only reads that model (`__HMM_MODEL_DB__`): it finds the stressed regime by volatility, never by state number, because states are sorted by mean return and two of them have almost the same one. `hmm_symbol` is a row field that defaults to QQQ. A live scan runs `Prepare`, which downloads the daily bars and extends the model with frozen parameters.
- `cmd/markov_test` and `cmd/transaction_calc` hold logic in `cmd/`, which breaks the thin-wrapper rule. Move that logic into `pkg/` when touching them.

## Adding or changing a strategy

1. Implement `strategy.Strategy` in `pkg/strategy/<id>.go` and register it (see `price_action_reclaim.go` for the newest example).
2. Put the signal math in `sql/strategies/<id>/` as ordered files (`01_schema.sql`, `02_calc_....sql`, ...), one stage per file, each writing a slice table. `GenerateSignals` runs them in name order.
3. The output table needs `idx, symbol, date, open, high, low, close, volume, buylimit, entry`; rows with `entry = 1` become entries. See `sql/strategies/README.md`.
4. Add a test using a real temp SQLite DB with literal bars. No mocks or fakes.
5. If the Go side needs a new indicator, write it as SQL instead of extending `strategy/indicators.go` or `decisiontree.go`.

SQL files are embedded via `sql/embed.go`; rebuild after editing them.

## Rule 5: Signals use absolute prices

In `models.Signal`, `TakeProfit` and `StopLoss` are absolute dollar prices (`150.00`), never multipliers (`0.95`) or percentages (`0.10`). A percentage passed as a price triggers instantly on the next tick. To use the configured fallback (`TakeProfitPct`, etc.), leave the field `0`; the simulator applies it to the real entry price.

## Data gotchas

- `backtest` and `backtest stack` hold out the last 12 months by default (`pkg/backtest/holdout.go`: in-sample pass, then one out-of-sample pass into `data/reports/oos/`). Tests or tooling that need a single full-history pass must pass `-holdout-months 0`; single-strategy result DBs otherwise cover only the in-sample window.
- Walk-forward and stack results depend on the market DB window: a stack found by screening many strategies on the full history is in-sample. Keep a held-out final period and report it separately (see `docs/omnifunds_benchmark.md` for the bar to beat).
- Hourly bars are the only bars downloaded (Alpaca or Polygon). Daily simulation reads session rows with `length(Date) = 10` that `pkg/sessions` derives from the hourly bars in the same table; there is no daily market database and no Yahoo or Stooq path for backtests. Do not add one. One narrow exception, approved by the owner: `daily_bars` (`pkg/dailybars`) keeps QQQ's long daily history from Yahoo in its own `data/daily_bars.db` for the HMM (`train hmm`); signals and the simulator never read it, and it never writes the hourly database.
- Result DBs are written per run: `data/reports/<id>.db`, then `<id>_2.db`, `<id>_3.db`. Do not assume a fixed filename.
- `strateval`'s ledger is `strategies.db` in the run folder (`-run-id`); `STRATEGIES_DB`, then `STRATEVAL_DB`, override it. `walk_forward.db` and `gridsearch.db` also live in the run folder, so a run never sees another run's results.
- `data/` (except `README.md` and `ignore_over50MB.sh`), `bin/` and `reports/*.html` are gitignored; `data/` also has its own `.git`. Do not commit databases or binaries.

## Before you finish

Run the checklist in `../CLAUDE.md`, plus: strategy ids registered and listed by `backtest -strategylist`; any new env var or flag is in `README.md`; `TakeProfit`/`StopLoss` are absolute or `0`.
