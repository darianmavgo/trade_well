# Architecture

Map of the whole `trade_well` repository as of 2026-10-27, after the day's changes: the three repos merged into one module, SQL read from files instead of embedded, schema creation moved into `.sql` files, vendoring removed, and a round of dead-code and table cleanup. Rules are in [CLAUDE.md](CLAUDE.md); per-layer detail is in [backtest/Architecture.md](backtest/Architecture.md), [orchestrator/README.md](orchestrator/README.md) and [schwaber/README.md](schwaber/README.md).

## One module, three layers

`github.com/darianmavgo/trade_well` is a single Go module (Go 1.26). It was `schwaber`, `backtestgosqlite` and `trade_orchestrator`, joined by `replace` directives and a vendored copy. There is now no `replace`, no sibling checkout and no `vendor/`: every dependency is a public module, so App Engine's Cloud Build downloads them itself.

| Layer | Directory | Owns |
|---|---|---|
| Foundation | `pkg/schwaber` | Schwab API client, OAuth, token storage and sync, order intent, account and order mirror, shared infra (config, DB open, GCS) |
| Calculation | `pkg/backtest`, `sql/stages`, `sql/strategies`, `sql/studies`, `sql/validation`, `sql/views` | Strategies, signals, backtests, market data, calendar, SQL pipelines |
| Orchestration | `pkg/orchestrator`, `sql/orchestrator` | When jobs run and in what order: scan, stage, submit, exit, health, email, dashboard, GAE deploy |

`cmd/<name>/` holds thin `main` wrappers and may import anything. Imports point one way: orchestration imports Foundation and Calculation; those two never import orchestration or each other. `go test ./pkg/layers` (`TestImportsPointDown`) fails the build when that breaks, because a single module no longer enforces it.

```
cmd/orchestrator   cmd/schwaber   cmd/backtest   cmd/strategy   cmd/train   cmd/cleanup ...
        │                │              │
        ▼                ▼              ▼
pkg/orchestrator ──▶ pkg/schwaber    pkg/backtest
        └──────────────────────────────▶ (imports both, never the reverse)
        │                                   │
        ▼                                   ▼
sql/orchestrator/*.sql                sql/{stages,strategies,...}/*.sql
```

## Execution model

- **Go controls execution, SQL does the calculation.** Go opens databases, orders steps, runs `.sql` files and handles errors. Each pipeline stage is one `.sql` file that writes a slice table; the next stage reads it.
- **One implementation, three contexts.** Each capability is a `pkg/<job>` function taking a `context.Context`. The CLI subcommand, the local web server and the `/cron/<job>` route on App Engine are wrappers around it. Environment differences are runtime checks inside the one package, never a fork.
- **One name per job** across package, CLI subcommand, `/cron/<job>`, `cron.yaml`, `job_runs.job`, dbsync name and log prefix `[<job>]`.

## SQL is read from files, not embedded

`sql/sqlfiles.go` serves the `sql/` folder from the file system (`sqlfiles.FS`). Nothing is compiled into the binary, so editing a `.sql` file takes effect on the next run with no rebuild. The folder is found by searching the working directory and its parents for a `sql/` containing `stages/` or `orchestrator/`; if none is found, every read fails with an error saying where it looked. A deploy uploads `sql/` (it is on the `.gcloudignore` allowlist next to `cmd/`, `pkg/`, `go.mod` and `go.sum`).

Layout:

| Folder | Used by | Contents |
|---|---|---|
| `sql/stages/<stage>/` | `pkg/backtest` | Numbered stage files per capability (`pipeline`, `markov_train`, `tree_features`, `streak_slice`, `bar_sma`, `hmm_*`, `check`, `refdb_export`, ...) |
| `sql/strategies/<id>/` | `pkg/backtest` | Ordered pipeline per strategy |
| `sql/studies`, `sql/validation`, `sql/views` | `pkg/backtest` | Study SQL, walk-forward summary, report views |
| `sql/orchestrator/<job>.sql` | `pkg/orchestrator` | One file per job or package: its schema and its queries |

Today's SQL changes:

- The orchestrator's many one-statement files (`check_health/*.sql`, `account_state/*.sql`, `appconfig/*.sql` and so on) were merged into one `sql/orchestrator/<job>.sql` per package, holding the schema DDL and every query for it.
- The Go-side `schema.go` files for `account_allowlist`, `dashboard` and `runtimeenv` were deleted along with `sql/orchestrator/runtimeenv.sql`; `schemas.ensureAll` no longer creates `account_allowlist` or `runtimeenv`.
- Legacy `.duckql` files (strategies, studies, search, seed, validation, models) were deleted or converted to `.sql`. `sqliteguard`'s inline-SQL test now checks that no SQL text lives in Go.

## The daily pipeline (orchestration)

Every job reads and writes the same Turso database (`TURSO_DATABASE_URL`, `TURSO_AUTH_TOKEN`, required everywhere, no local SQLite file). The rows are the state machine; each job moves rows from one status to the next.

```
scan_market ─▶ stage_orders ─▶ submit_orders ─▶ (confirm fills) ─▶ manage_exits ─▶ check_health
 18:00 Sun-Thu   18:15           09:28            09:45              09:35 / 15:50    10:00
 signals only    size vs cash    place orders     record position    close on rules   audit
```

`account_state` (09:15 and post-market), `risk` (09:17, 16:18), `clear_stale` (16:20), `ready` (09:00, 17:30) and `weekly_digest` (Saturday) surround it. Scanning never calls the Schwab order API; submission re-validates against the account's current cash and positions. `deploy_gae/cron.yaml` is the schedule; `run_all_jobs` is a manual one-shot sequence only.

Strategies are database rows (`refdata/strategies.db`), not code. App Engine runs from the subset bundled under `pkg/orchestrator/strategies/deploy/`, written by `strategy export` (see [orchestrator/README.md](orchestrator/README.md)).

## Data stores

| Store | Location | Holds |
|---|---|---|
| Turso (libsql) | `TURSO_DATABASE_URL` | All live state: `staged_orders`, `active_trades`, account mirror (`account_state_*`), `job_runs`, health, risk, allowlist, `live_vs_backtest_*` |
| `market_history.db` | `data/` (`MARKET_DB`) | Daily and intraday bars, option chains |
| `strategies.db` | `refdata/` | Strategy tables, universes, symbol lists |
| Run folders | `data/reports/<run>/` | One fresh calculation database per run, plus HTML reports |
| GCS config object | `gs://<bucket>/config/env` | Secrets and settings, including `STRATEGY_ALLOWLIST` |

Litestream was removed; Turso is the only live database. Backtest SQLite uses `modernc.org/sqlite` (no cgo) so packages the orchestrator imports build on App Engine. `mattn/go-sqlite3` is now only an indirect dependency.

### Turso table cleanup

`scripts/turso_drop_deprecated_tables.sql` drops tables no code touches any more: Litestream bookkeeping, the old `audit_slippage`, `reconcile_trade` and `backtest_winrate`, the orphaned `dashboard`, `runtimeenv` and `account_allowlist`, and the plain-named Schwab mirror (`accounts`, `positions`, `orders`, `order_legs`, `transactions`, `raw_responses`, `user_preferences`). It has not been run. Run it only after the new build is deployed, since the old app recreates some of them with `CREATE TABLE IF NOT EXISTS`. `DROP TABLE` cannot be undone; read the script's checks first.

## Deploy targets

| Folder | Target |
|---|---|
| `deploy_gae/` | App Engine (`go126`, entrypoint `./cmd/orchestrator`, `cron.yaml`, `dispatch.yaml`, setup and config scripts). No `GOFLAGS=-mod=vendor`, no `GOPRIVATE`; `deploy.sh` no longer vendors |
| `deploy_live_local/` | Local live orchestrator (`air.toml`, `build.sh`, `servelocal.sh`) |
| `deploy_report_local/` | Local live-vs-backtest report server |

`build_all.sh` was removed; use `make build` (binaries into `bin/`) and `make check` (build, vet, test).

## Code removed today

Packages deleted from `pkg/backtest` because nothing used them: `bdc_edgar`, `regime_score`, `runview`, `stackopt`, `stratlist`, `transaction_calc`, plus `appenv` helpers and a `.env.example` entry for removed env variables. A new `cmd/cleanup` (`pkg/backtest/cleanup`) tidies `data/reports`: `report`, `runs`, `sql` (lists `sql/` files nothing refers to; `-remove-sql` deletes them), and `all`. Every mode takes `-dry-run`.

## Known gaps

- The root `CLAUDE.md` still describes a multi-repo workspace ("each repo has its own `.git`") and needs the single-module update that `docs/CLAUDE.md` already has. `docs/CLAUDE.md` Rule 4 still lists known test fakes (`fakeLister`, `fakeValidator`, `fakeGcloud`, `sendFailureEmail`) that have not been removed.
- `docs/orchestrator/README.md` still describes some pre-merge workflow details (for example `backtestgosqlite` train commands).
- Several references to Litestream remain in older docs and are stale.
