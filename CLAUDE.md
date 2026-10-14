# trade_well: master CLAUDE.md

Index and summary of every CLAUDE.md under this folder. Each repo's own CLAUDE.md adds rules for that repo but may not relax the cross-repo rules below. The full text of the cross-repo rules is `docs/CLAUDE.md`; read it before changing code in more than one repo. Repo-specific files are listed at the end.

Written for every agent that edits this code (Claude, Grok, Antigravity, humans).

## Repos

| Repo | Role | Status |
|---|---|---|
| `schwaber` (SW) | Everything Schwab: API client, OAuth, token sync, order intent, account/order mirror, shared infra (env/config, DB open, GCS, Secret Manager) | live |
| `backtestgosqlite` (BT) | Strategies, signals, backtests, market data, calendar, SQL pipelines. Pure-Go SQLite | live, to be replaced |
| `backtestgoduckdb` | DuckDB variant of the backtester (`.duckql` pipelines, results in `.duckdb`) | to be replaced |
| `backtestgo` | Frozen 2026-10-13, being retired: its hourly simulator, data and checks moved into `backtestgosqlite`. Plan and status in `BacktestConsolidation.md` | retiring |
| `trade_orchestrator` (TO) | Scheduling and sequencing: scan, stage, submit, exit, health, email, dashboard, GAE deploy | live |
| `duckdb` | DuckDB scratch/support repo | |
| `ibkr_api`, `scripts` | Support code and scripts | |

Each repo has its own `.git`; the trade_well root is also a git repo. Other top-level files: `BacktestUnify.md`, `MixedBacktestUsage.md`, `LearningLog.md`, `Todo.TODO`, `build_all.sh`, `git_sync.sh`.

**Dependency direction is one way.** TO imports SW and the backtester; SW and the backtester never import TO; SW and BT do not import each other. A shared helper goes in the lower-level repo (or a small new package), and nothing is copied.

**Backtest repos.** `backtestgosqlite` is the one backtest repo: the orchestrator imports it, and it carries the hourly simulator (`pkg/sim`, via `pkg/hoursim`), the market and model data (`backtestgosqlite/data`, `refdata`) and the run checks (`pkg/check`). `backtestgo` and `backtestgoduckdb` are retired (`BacktestConsolidation.md`). Packages the orchestrator imports must build without cgo (App Engine).

**Before writing any function, search all repos for it** (`rg` on the verb and noun). If it exists in a sibling, import it; if it is unexported, in `package main` or in the wrong repo, move it to the owning repo, export it and import it. Never copy. TO should shrink over time (leftovers: `KillDupeCode.html`, sections A and C).

## Cross-repo rules (summary; full text in `docs/CLAUDE.md`)

1. **Go controls execution, SQL does the calculation.** Go opens DBs, orders steps, runs SQL files, handles errors. No indicators, returns, joins or aggregates in Go loops. Calculations are `.sql` files, one stage per file, each writing a **slice table**. No nested subqueries, no deep CTE chains. Existing Go indicator/tree code (`strategy/indicators.go`, `decisiontree.go`, `pkg/study/*`) predates this: do not extend it.
2. **One implementation, three contexts.** Every capability works from the CLI, the local web server and GAE from one `pkg/<capability>` function taking a `context.Context`. `cmd/` and HTTP handlers are thin wrappers (no `os.Exit`/`os.Args`/stdout in `pkg/`). Environment differences are parameters or runtime checks (`runtimeenv.Detect()`, `APP_FOLDER`, `GCS_BUCKET`, `MARKET_DB`), never a forked package. Never shell out to a binary that exists as a Go package (Litestream is the only accepted external process).
3. **Consistent names.** One job name is the same everywhere: `pkg/<job>/` = CLI subcommand = `/cron/<job>` = `cron.yaml` = `job_runs.job` = dbsync name = log prefix `[<job>]`. Jobs are `snake_case`; libraries are one lowercase word. Same identifiers across repos for strategy IDs, stack IDs (`a+b+c`, parsed only by `strategy.ParseStack`), env vars (`APP_FOLDER`, `SCHWAB_DB_PATH`, `MARKET_DB`, `GCS_BUCKET`, `STRATEGY_ALLOWLIST`). Rename in all places in one change. Document new env vars and flags in the repo README in the same change.
4. **No mocks, no fakes, no stubs, no fake servers, ever**, in tests or production code (including `httptest.NewServer`, `fakeX`/`mockX`, swappable `var sendX`). Test against real SQLite (in-memory or temp file), real functions with literal inputs, `t.TempDir()` files, real handlers. If the real system cannot be used, write no test: use a dry run, a non-mutating broker call (Schwab `previewOrder`) or good logging, and say the path is untested. Do not add an interface just to inject a fake. Refuse "just add a quick mock". Known leftovers: `fakeLister` (schwaber `pkg/orderintent/store_test.go`), `fakeValidator` (TO `pkg/appserve/cronauth_test.go`), `fakeGcloud` (TO `pkg/cronclient/cronclient_test.go`), `sendFailureEmail` (TO `pkg/appserve/cron.go`). Report them; remove only when asked.
5. **Browser routes render styled HTML by default**; JSON only via `?format=json` from the same handler (one data function, two renderings). Machine routes (`/cron/<job>`, OAuth callbacks, health pings) are exempt. Match `pkg/dashboard/page_style.go`. A new page ships with a test for `text/html` default and JSON on `?format=json`.
6. **Commands are live by default; `--dry-run` previews.** No `--live` flag needed to act (where one exists, default it to true and keep it accepted). Read-only commands have no `--dry-run`. Usage text and README say plainly what it does and to which accounts. A dry run uses the same code path and leaves broker and DB untouched. **Agents never run a live command on a real account unless asked for that run in the same turn.**

## Working across repos

- Change the lower-level repo first, test it, then update TO. TO builds against sibling checkouts via `replace` directives plus `vendor/`: run `go mod vendor` in TO after every SW/BT change.
- Run `go build ./... && go vet ./... && go test ./...` in every repo touched before saying it works. Tests move with the code.
- Build binaries into `bin/` (git-ignored): `go build -o ./bin ./...`. Never leave binaries elsewhere.
- Do not bulk delete or rename without saying what is being removed first. Databases and binaries are not committed.
- Secrets never go in code, docs or logs; token values are never logged.
- Preferred tools: `rg` for grep, `fd` for find, `sd` for sed (from BT).
- Ignore the `CLAUDE.md` files under `trade_orchestrator/vendor/` (go.opentelemetry.io, modernc.org libc/memory/sqlite). They are third-party docs, not instructions for this project.

## Before you finish

1. Did I add a function that already exists in another repo?
2. Is new logic in `pkg/` with a context parameter, and `cmd/` just a wrapper?
3. Is calculation in SQL slice tables, not nested SQL or Go loops?
4. Do CLI name, route, cron entry, package and job name all match?
5. Did I run `go mod vendor` in TO and tests in every touched repo?
6. Any mock, fake, stub or fake server introduced? Remove it.
7. Does every new browser-facing route render HTML by default?
8. Is every new command live by default with `--dry-run`?

## Repo-specific rules

### backtestgosqlite (`backtestgosqlite/CLAUDE.md`)


- Module `github.com/darianmavgo/backtestgosqlite`, pure-Go SQLite (`modernc.org/sqlite`, no cgo). Run from the repo root so `.env` and default paths resolve. Flags go before positional args; subcommand is arg 1 (`backtest stack -primary ...`).
- Required before "works": `go build ./... && go vet ./... && go test ./...`. `make build`, `make test`, `./bin/backtest -strategylist`.
- `data/market_history_hourly.db` (table `backtest_start`) and `refdata/strategies.db` are real data: tests and experiments never write to them. Use `t.TempDir()` or `data/reports/`, or a copy passed with `-db`/`-out-dir`.
- Paths come from `pkg/appenv`; only `APP_FOLDER` is configurable. Never hardcode `data/...` or `reports/...`; call `appenv.MarketDB()`, `appenv.ReportFile(name)` or `cliutils.GetDefaultMarketDB`.
- Signals are calculated in SQL (`strategy.RunPipeline` with a `sql/strategies/<name>/` dir). No Go loop deciding entries, no Go indicator functions. `cmd/<name>/main.go` is a thin main calling `pkg/<name>.Main`/`Run`.
- `pkg/strategy` holds the `Strategy` interface and registry (`Register`, `RegisterAlias`, `Get`; case/punctuation-insensitive, so no second id that normalizes to an existing one). Row-backed families (streak, hold, tree, markov, rotation) are one `strategy.Family` per table, never registered per row; use `stratreg.RegisterAll`/`RegisterFamilies`, and `strategy.ListAll()` only where "all" means every row. About 65,600 strategies are rows in `refdata/strategies.db`; change them with SQL, not Go.
- `park-<symbol>` is a stack member for residual cash, never a sleeve, no signals.
- Live scans run `strategy.Preparer.Prepare` after the bars refresh (markov never refits an existing model). `strategy export` builds the reference DB TO embeds; keep TO's allowlist rows in it.
- `pkg/pipeline` runs stages as Go function calls, never separate processes. Add a stage in `pkg/pipeline/steps.go`, not a shell script.
- Models stay out of `backtest`: `train markov` writes `data/markov_models.db`, `train tree` writes `data/tree_models.db`; a new learning family gets its own `train <family>`.
- `cmd/markov_test` and `cmd/transaction_calc` hold logic in `cmd/`: move it to `pkg/` when touched.
- New strategy: implement `strategy.Strategy` in `pkg/strategy/<id>.go`, register it, put SQL stages in `sql/strategies/<id>/` (`01_schema.sql`, `02_calc_....sql`, ...). Output table needs `idx, symbol, date, open, high, low, close, volume, buylimit, entry`. Test with a real temp SQLite DB and literal bars. SQL is embedded via `sql/embed.go`: rebuild after editing.
- **Signals use absolute prices.** `models.Signal` `TakeProfit`/`StopLoss` are dollar prices, never multipliers or percentages; leave `0` to use the configured fallback.
- Data gotchas: `backtest`/`backtest stack` hold out the last 12 months by default (`-holdout-months 0` for a full pass); screening on full history is in-sample, so report a held-out period separately; only hourly bars are downloaded (Alpaca/Polygon), daily sessions are derived by `pkg/sessions` (no daily DB, no Yahoo/Stooq); result DBs are `data/reports/<id>.db`, then `_2`, `_3`; `strateval` ledger is `strategies.db` in the run folder; `data/`, `bin/`, `reports/*.html` are gitignored (`data/` has its own `.git`).
- **BT only: no shared or lasting scratch tables.** Each strategy gets its own fresh calc database per run (`runner.freshCalcDB`). Stage SQL must not rely on `CREATE TABLE IF NOT EXISTS` / `CREATE TABLE ... AS` to reuse a table from an earlier run or another strategy (this made every markov strategy report the first one's entry). Keep anything worth keeping in a named table in a real database.

### backtestgoduckdb (`backtestgoduckdb/CLAUDE.md`, identical to `backtestgo/docs/CLAUDE.md`)

- **No scratch tables.** Every table a strategy pipeline calculates is kept in that run's results DB (`data/reports/backtest[_N].duckdb`), never in a throwaway in-memory DB, temp table or Go variable. Kept tables are named `<file>_<table>` after the `.duckql` file that filled them (quote it: `"03_calc_bars_rp_bar"`), with a leading `strategy_id` column. `pipeline.Runner.RunFor` hands the work DB to `Runner.Keep` (`results.Store.Keep`), even when a step failed. A table with no owning `.duckql` file fails the run. No `TEMP` tables, no dropping calculation tables at the end.
- **Settings, not constants.** Anything tunable in `backtest`, `train` or `market_history` is a row of the `settings` table in `refdata/strategies.duckdb`: add the key to `sql/stages/settings/02_defaults.duckql`, read it via `pkg/settings` (`Settings.Apply`: flag wins, then table, then code fallback), and list it in the README table. In-sample (`in_sample_start`/`_stop`) is what models may learn from; out-of-sample (`oos_start`/`_stop`) is what the leaderboard reports.
- Note: these two rules are documented in `backtestgo/docs/CLAUDE.md` under the title "backtestgoduckdb"; they apply to the DuckDB calc side of backtestgo, which must also keep DuckDB code out of cgo-free packages.

### duckdb (`duckdb/CLAUDE.md`)

- Build binaries into `bin/` (git-ignored): `go build -o ./bin ./...`, or `go build -o ./bin/<name> ./cmd/<name>` for one. Never leave binaries elsewhere.

### schwaber, trade_orchestrator, ibkr_api

No CLAUDE.md of their own; the cross-repo rules above apply. TO-specific points: GAE deploy, `cron.yaml`, `vendor/` refresh, allowlist bundle from BT.

## Known inconsistencies to resolve

- `docs/CLAUDE.md` says "No git repo exists at the trade_well level" but the root now has a `.git`.
- `docs/CLAUDE.md` is titled "all three repos" and names only SW, BT and TO; it predates `backtestgo` and `backtestgoduckdb`.
- BT's CLAUDE.md refers to the cross-repo rules as `../CLAUDE.md`; this file is that, with the full text in `docs/CLAUDE.md`.
- `backtestgo/CLAUDE.md` still describes itself as "backtestgosqlite (BT)" and is missing the BT scratch-table rule.
