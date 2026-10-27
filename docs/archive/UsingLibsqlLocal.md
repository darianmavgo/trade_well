# Using libsql locally (experiment, branch `libsql-turso`)

> **Archived 2026-10-27.** Historical record, not maintained. Paths and statements refer to the old three-repo layout (`backtestgosqlite`, `trade_orchestrator`, `schwaber` as separate repositories, `schwaber.db`); the code is now under `pkg/backtest`, `pkg/orchestrator` and `pkg/schwaber`.

Status: **cancelled before any timing was taken.** This records what was learned about swapping `modernc.org/sqlite` for libsql in this repo, so the work can be resumed or dropped.

Goal was to compare one streak backtest (`streak-voo-up5-tqqq`) under both drivers. The correctness gate (identical result DBs) was never passed, so there are no performance numbers.

## Which libsql driver
- Only `github.com/tursodatabase/go-libsql` opens local files. It is **embedded and needs cgo**. This breaks the repo rule of no cgo (and App Engine builds), so it must not be merged as it stands.
- `libsql-client-go` (pure Go) is remote-only and is not comparable.
- Version used: `v0.0.0-20260424063416-3051e37e6e04`. The module is 135 MB (prebuilt libs), and the libsql binary is about 34 MB against 17 MB for modernc.

## Code left in the working tree (uncommitted)
- Hot-path opens now use the seam: `pkg/backtest/storage/sqlite.go` (2), `pkg/backtest/storage/results.go`, `pkg/backtest/strategy/sql_strategy.go`. About 40 other `sqlx.Open("sqlite", ...)` calls were left alone.
- `pkg/backtest/storage/results.go`: `WriteRun` uses `SELECT last_insert_rowid()` instead of `res.LastInsertId()`.
- `scripts/patch_libsql.sh` builds `third_party/go-libsql`, a patched copy of the driver. `third_party/` is in `.gitignore`. `go.mod` has a `replace` pointing at it, so **the default modernc build also needs that directory**; run the script first or remove the replace line.
- The last patch edit (removing the unused `Outerloop:` label in the copied `libsql.go`) was applied by the script and the rebuild was interrupted; it is not known whether it compiles.

## Driver differences found (all break drop-in use)
1. **`Exec` runs only the first statement.** Multi-statement schema scripts and pragma blocks create only the first table or apply only the first pragma. Fix: `ExecScript` splits on `;` for libsql. This is naive (breaks on `;` inside literals or triggers). About 20 other `db.Exec(schema)` sites outside `pkg/backtest/storage` are not covered. The strategy stage SQL is already split per statement in `sql_strategy.go`, so it was fine.
2. **`LastInsertId()` returns 0.** Result run ids came back as 0 and later inserts went to the wrong run. Fix: `SELECT last_insert_rowid()` in the same transaction (works on both drivers).
3. **Text that looks like a date comes back as `time.Time`**, whatever the column type, even through `CAST(Date AS TEXT)`. Scanned into a `string`, `2021-01-01 00:00:00` becomes `2021-01-01T00:00:00Z`. The hourly bar loader (`pkg/backtest/runner/intraday.go`) then failed to parse, logged "no hourly bars", and silently played sessions as daily bars, so the libsql run was a different workload. Every string-typed date in the repo is affected, so call-site fixes are not practical. Workaround: patched driver copy that drops the date-parsing block (`scripts/patch_libsql.sh`).
4. **DSN options differ.** modernc takes `_pragma=...(...)` and `_txlock=immediate`; libsql takes none of them, so pragmas must be run as statements. `_txlock=immediate` has no libsql equivalent found, so write locking behaviour differs.
5. Pragmas run via `Exec` apply to one pooled connection only. The existing `ATTACH` in `sql_strategy.go` already relies on a single connection, and libsql was not checked for that.

## Benchmark setup (reusable)
- The real market DB is 19 GB. A trimmed copy with only VOO and TQQQ (32,831 rows, same schema and indexes) was built with `sqlite3` using `ATTACH ... ?mode=ro&immutable=1`. The file name must end in `_hourly` (`appenv.RequireHourly`), e.g. `market_voo_tqqq_hourly.db`.
- modernc baseline: 17 trades, about 0.24 s wall including process startup. One strategy is too short to time reliably; use a batch of streak rows (only 2 exist for VOO to TQQQ) or the stage-SQL benchmark.
- Running the backtest or `sqlite3` against `refdata/strategies.db` modified that tracked file (it showed as changed). It was restored with `git checkout -- refdata/strategies.db`. Check `git status refdata` after any run, or point `APP_FOLDER` at a copy.
- Result comparison to use: dump `runs`, `trades`, `signals`, `equity_curve`, `performance_summary`, `run_metrics` from both result DBs and compare. The one comparison made (before the date fix) showed `run_metrics` identical and the rest different, but the differences were not inspected; `runs` may hold timestamps that always differ.

## What was not done
- No timing for either driver, no pprof comparison, no stage-level benchmark, no `docs/libsql_experiment.md`.
- Tests were not run under `-tags libsql`.

## To resume
1. `./scripts/patch_libsql.sh`, fix any compile error in the patched copy, rebuild both binaries.
2. Rerun the gate on the trimmed DB. Both runs must log the same bar source and produce identical result tables.
3. Only then time (alternate drivers, discard a warm-up, at least 7 runs).

## To drop
