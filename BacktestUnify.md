# Plan: Unify backtestgosqlite and backtestgoduckdb (rev 3, in progress)

Goal state: `backtestgo` replaces both parent repos once it passes tests and succeeds in production.

## Context
- **backtestgosqlite** (the sqlite repo): ~23 commands and 48 packages. It runs on App Engine through `trade_orchestrator` for live execution. Its daily simulator produced many false results.
- **backtestgoduckdb** (the duckdb repo): 5 commands and 15 packages. Its hourly simulator (`pkg/sim`) is the most valuable improvement to the strategy work. Its pain point is file locks: `pkg/store/store.go:25-125` kills the PID holding a lock, and `market_history_split` writes 7,458 per-symbol files to dodge locks.

Goals:
1. DuckDB calculation speed.
2. No file locks, even for read-only applications.
3. SQLite WAL concurrency.
4. SQLite deployment familiarity on App Engine.

Terminology: "DD" means drawdown in this effort. Say "duckdb repo" or "DuckDB".

## Ground rules (from review of rev 1)
1. **No in-memory databases.** Every calc database is a persisted file, kept for accounting. This matches the duckdb repo's "no scratch tables" rule, so every calculated table is kept.
2. **App Engine stays SQLite-only and cgo-free.** No heavy calculation happens there, only execution of strategies with already-trained models. DuckDB never ships to App Engine.
3. **The hourly simulator is the only simulator.** The daily simulator is not ported or carried forward.
4. **Few packages.** Follow the duckdb repo's simple layout, not the sqlite repo's 48.
5. **A third repo demonstrates the architecture first.** It proves that all refdata kept in SQLite can be attached to DuckDB calc databases.

## Architecture
**SQLite holds all maintained reference data. DuckDB holds all calculation, in persisted per-run files.**

| Data | Home | Writer | Readers |
|---|---|---|---|
| Hourly market bars | SQLite, WAL | downloader | calc runs, attached read-only |
| Strategies, settings, symbol lists, stacks | SQLite, WAL (`refdata/strategies.db`) | maintenance commands | calc runs, attached read-only |
| Trained models (markov, tree) | SQLite, WAL | `train` | calc runs and App Engine |
| Universe | SQLite, WAL | `universe` | calc runs |
| Calc and results | DuckDB file, one per run: `data/reports/<run_id>.duckdb` | exactly one process (that run) | anyone, read-only, after the run ends |

Why this has no lock problem:
- Each calc database is private to one run, with a unique filename. Only one process ever opens it for writing, so there is no contention and nothing to kill.
- The shared reference data is SQLite in WAL mode. DuckDB attaches it `READ_ONLY` through its `sqlite` extension, which takes SQLite read locks. WAL writers (the downloader, the trainer) are never blocked and never block the runs.
- A finished run file is immutable, so any number of read-only apps can open it. Status of in-progress runs lives in a SQLite `runs` table, not by opening the half-written DuckDB file.

How the attach works in a calc file:
```
ATTACH 'refdata/strategies.db' AS ref    (TYPE sqlite, READ_ONLY);
ATTACH 'data/market_history_hourly.db' AS market (TYPE sqlite, READ_ONLY);
```
If scanning the attached bars is too slow, the first stage persists a slice into the calc file: `CREATE TABLE bars AS SELECT ... FROM market.backtest_start WHERE symbol IN (...) AND date BETWEEN ...`. That is also what accounting wants, because the exact bars used are kept with the run.

## Phase 0: the third repo (`backtestgo`)
A small third repo whose job is to prove the architecture on real data, with no mocks (Rule 4):
- **Real SQLite refdata.** A minimal one: hourly bars for a handful of real symbols, strategy rows (streak, hold, tree, markov, rotation), `settings`, `symbol_lists`, a model DB. These are copied from the real databases, never invented.
- **One command, `calc`.** It creates `data/reports/<run_id>.duckdb`, attaches the SQLite files read-only, runs the existing `.duckql` stages, and keeps every calculated table.
- **The hourly simulator** (`sim`) ported from the duckdb repo.
- **Layout target, under 8 packages:** `refdata` (attach and open), `market`, `strategy`, `sim`, `results`, `settings`, `calc` (orchestration), plus `cmd/`.

What it must show, as automated checks:
1. **Attach works:** the `sqlite` extension loads from the Go driver, offline, with no `INSTALL` that needs the network. If it does not, bundle or vendor the extension.
2. **No locks:** while `calc` runs, a downloader keeps writing the SQLite market DB, and a separate read-only process reads it. Nothing errors and nothing is killed.
3. **Speed:** a full strategy batch against attached SQLite, compared with the same batch on the duckdb repo's native DuckDB market file. Report both numbers. If attached scans lose badly, the slice-into-calc-file stage above is the fix.
4. **Write back:** a finished run's key results (`runs`, performance summary) can be recorded in SQLite, so apps can list runs without opening DuckDB files.
5. **Reproducible accounting:** the calc file alone explains a run (bars used, params, signals, trades, equity).

**Gate:** if 1 to 3 fail, stop and reassess. Do not start the merge.

## Phase 1: absorb the duckdb repo into the backtestgo repo
- It becomes the backtest engine. Port the remaining duckdb-repo packages (`backtestcmd`, `gridsearch`, `train`, `sessions`, `pipeline`) while merging them into the 7-8 package layout rather than copying them over one to one.
- Delete `pkg/store/store.go` kill-and-retry, `history_reader`'s merge-from-many-files logic, and the per-symbol file split. One WAL market DB with a `(symbol, Date, timeframe)` index replaces the 7,458 files.
- Migrate with a one-shot `cmd/migrate` (live by default, `--dry-run` to preview, per Rule 6): `market_history_hourly.duckdb` and the per-symbol files into one SQLite market DB, and `strategies.duckdb`, models and universe into their SQLite homes.
- Dedupe sources: the sqlite repo's DB has 674 symbols and the duckdb repo's has 1,873. Keep the larger set. The first source listed wins on overlapping hours, as `history_reader` does today.

## Phase 2: slim the sqlite repo into the execution side
- Remove what moved: simulator, gridsearch, walk-forward, stackopt, backtest and pipeline commands, and their packages. Keep what App Engine and live use need: `livescan`, `runner/download`, `refdb` export, `appenv`, the SQLite strategy and signal path, `storage`.
- Livescan signal generation still runs `.sql` on SQLite on App Engine, so the `.sql` and `.duckql` stage twins must produce the same signals. Add a parity test that runs each strategy family through both on real data and compares signals exactly.
- The calc repo's `train` writes SQLite model DBs. `refdb.Export` / `ExportModels` bundle only the allowed rows for App Engine, unchanged.
- `trade_orchestrator` keeps its `replace` and vendor of the slimmed sqlite repo (`go.mod:53`, `deploy_gae/app.yaml` unchanged, still `go126` with no cgo).

## Phase 3: old results and docs
- Mark every run from the daily simulator as suspect in the sqlite repo's `data/reports` (a marker file and a column in the runs table). Do not delete them. Do not mix them into rankings.
- Update `CLAUDE.md`, READMEs and `docs/Architecture.md`. Fix stale claims found: about 65,600 strategy rows (actual about 18.3k), the "snapshot copy is read instead" line (no such code), symbol counts, and the missing `../CLAUDE.md` reference.
- Archive the duckdb repo and the heavy sqlite repo commands once the backtestgo repo has parity.
- Fix the Rule 4 `httptest.NewServer` tests (`polygon_test.go`, `alpaca_test.go`, the duckdb repo's `market_test.go`) only when those files are touched.

## Critical files
- duckdb repo: `pkg/pipeline/pipeline.go` (`OpenWork`, the attach pattern), `pkg/backtestcmd/backtestcmd.go`, `pkg/sim/run.go` and `orders.go`, `pkg/results/*`, `sql/stages/**/*.duckql`, `pkg/store/store.go` (delete)
- sqlite repo: `pkg/storage/sqlite.go`, `pkg/refdb/export.go` and `export_models.go`, `pkg/runner/download.go`, `pkg/strategy/sql_strategy.go`, `sql/embed.go`
- Deploy: `trade_orchestrator/deploy_gae/app.yaml`, `trade_orchestrator/go.mod:53`

## Verification
- Phase 0 checks 1 to 5, with the speed numbers reported.
- Lock test with real processes: downloader writing the SQLite market DB, 4 parallel calc runs, one read-only viewer. No errors, no killed PIDs.
- Signal parity between `.sql` (SQLite, App Engine path) and `.duckql` (DuckDB) for each strategy family on real data.
- Hourly simulator results in the new repo match the duckdb repo's for the same strategy and period.
- `go test` in each repo using real SQLite and DuckDB files in `t.TempDir()`. No mocks, and never touch `data/market_history_hourly.db` or `refdata/strategies.db`.
- App Engine: `go build` of the orchestrator succeeds with `CGO_ENABLED=0`; deploy a test version and run a `livescan` on exported models.

## Open questions
- Whether the calc repo or the slimmed sqlite repo owns `train`'s model output format (the plan assumes the calc repo trains and exports).

## Status (2026-10-12)

Done in `backtestgo/` (not committed):
- **Phase 0 spike passed.** DuckDB's `sqlite` extension is bundled in `go-duckdb` v2.4.3 and loads offline. Four DuckDB readers ran against a SQLite WAL file while a separate process wrote to it: 139 inserts, 0 errors. Writing run summaries back to SQLite through the attach works.
- **Speed.** A window query over 3.9M bars takes 566 ms attached to SQLite and 73 ms once the bars are copied into the calc file, so scanning attached SQLite is about 8x slower. A native DuckDB file is about 5x faster per row than the attach. Sessions are therefore built into the calc file once per run, limited to the symbols the selection reads (17 s instead of 51 s for 7,843 symbols).
- **Migration** (`cmd/migrate`, live by default, `-dry-run`, `-market=false`): 67,673,748 hourly bars and 7,843 symbols into `data/market_history_hourly.db` (WAL, unique index on symbol, Date, timeframe), plus `refdata/strategies.db`, `refdata/universe.db`, `data/markov_models.db` and `data/tree_models.db`. Settings paths repointed to `.db`. Row counts are in `data/reports/migrate*.duckdb` (`migrate_log`). One per-symbol file (`SD.duckdb`) was skipped because the duckdb repo's downloader held its lock; that is the lock problem this design removes.
- **Ported to SQLite storage:** settings, market download (Alpaca to SQLite, no lock-killing), trainers (model DBs are SQLite), sessions, workspace, backtest command. The `store` lock-killer and `history_reader` are gone.
- **Parity:** `spy-buy-hold` gives identical results to the duckdb repo (16.0% total, 16.1% CAGR, 9.1% max drawdown, 1.22 Sharpe). The scoped hold run gives the same numbers for all six ranked strategies.
- **Tests:** all `go test ./pkg/...` pass. The Alpaca fake in `pkg/market` was dropped (it broke the no-fakes rule); the download's storage, coverage log and dry-run planning are tested on a real SQLite file instead.

Constraint found: two file-backed DuckDB instances in one process cannot both attach the same file, even read-only (in-memory instances can). So each run has one calc file with one DuckDB instance (`pipeline.Calc`); the sessions database and the model SQLite files are attached to it once, and every worker is a connection of that instance with its own schema. Kept calculation tables still go to the results database through `Keep`.

Left to do before the parents can be retired:
1. **Execution side for App Engine** (SQLite only, no cgo): `trade_orchestrator` imports `pkg/strategy`, `models`, `calendar`, `stratreg`, `appenv` from the sqlite repo. These need to be ported into `backtestgo` behind a pure-Go build, the orchestrator's `replace` repointed, and a test version deployed. Signal parity between `.sql` (SQLite) and `.duckql` (DuckDB) must be tested for every strategy family.
2. **Parity across families** (streak, markov, tree, rotation, portfolios), not only hold.
3. `gridsearch`, `train` end-to-end runs on real data, then the remaining commands.
4. Re-run the market migration in append mode once the duckdb repo's downloader is stopped (it is still adding bars), then stop it.
5. Move the old daily-simulator run history aside, mark it suspect, and update `CLAUDE.md` and the READMEs.
