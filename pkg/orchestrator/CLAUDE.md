# trade_orchestrator: CLAUDE.md

Orchestration layer. The cross-repo rules in `../CLAUDE.md` apply and are not repeated here. This file only adds what is specific to this repo.

## Layout

- `cmd/orchestrator`: the one binary (CLI, local server, and the App Engine entrypoint). Thin wrapper over `pkg/<job>`.
- `pkg/<job>`: one package per job or capability, named as its CLI subcommand, `/cron/<job>` route and `job_runs.job`.
- `deploy_gae/`: the only App Engine deploy (Turso backend). `cron.yaml` is the one scheduler; `pkg/job_runs/schedule.go` mirrors it and a test compares them.
- `deploy_live_local/servelocal.sh`: the app on this machine (port 8082) against the **live** config object, token and Turso database.
- `deploy_report_local/servelocal.sh`: the live-vs-backtest-report service locally (port 8081), read only.
- `docs/`: notes and `Endpoints.html`. `scripts/export_strategies.sh`: bundle export.

## Rules for this repo

- **Turso is the only database.** No Litestream, no replica, no local operational SQLite outside `go test`. Do not reintroduce a sync layer.
- **Configuration is one GCS object**, `gs://<bucket>/config/env`. No `env_variables` in any `app.yaml` (`TestAppYAMLHasNoEnv`). Edit it only through `deploy_gae/config.sh`.
- **Never point `APP_FOLDER` at a sibling repo** (for example `backtestgosqlite`). The embedded bundle (`pkg/strategies/deploy/*.db`) is copied over files in `APP_FOLDER` and overwrites the real `refdata/strategies.db` and model databases there.
- **The strategy bundle is data.** Change it with `orchestrator deploy_strategy` or `scripts/export_strategies.sh`, then `go mod vendor`. The live `STRATEGY_ALLOWLIST` is changed with `orchestrator strategy_allowlist` or the `/allowlist` page; a running instance keeps its list until it restarts or redeploys.
- **Deploys and live commands are the user's call.** `deploy_gae/deploy.sh` replaces the running version. Agents use `--dry-run`, `ready --static` and read-only checks, and run a live deploy or any command on a real account only when asked in the same turn.
- Refresh `vendor/` after any change in `../schwaber` or `../backtestgosqlite`. `vendor/` is not committed.
- **Orchestrator SQL is under `sql/orchestrator`**: a package's statements are `-- name:` blocks in `sql/orchestrator/<package>.sql`, read with `orchestratorsql.Q("<package>/<name>")`; a script of stages or views is a folder `sql/orchestrator/<package>/`, read with `orchestratorsql.Dir` or `File`. It is read from disk, not embedded in the binary, and not written inline in Go. SQL that is built at run time (a table name, an `IN` list) is the one exception, and `TestOrchestratorSQLLivesInFiles` lists each such place so the list can only shrink. A deploy must upload `sql/` (`.gcloudignore` keeps it); `/ready` shows `sql_files` with the folder an instance found. The backtest layer may not import `sql/orchestrator` (`pkg/layers`).
- **SQLite is visible by name.** The operational database is Turso. This repo opens a SQLite file only for one of two reasons, and says which in the file name and the function name (`pkg/sqliteguard` enforces it; read its `doc.go`): an **Asset** built in `backtestgosqlite` (a strategy definition, a trained model, a backtest result database; files `sqlite_strategy_asset_*.go`, `sqlite_model_asset_*.go`, `sqlite_backtest_result_asset_*.go`, function names contain `Asset`), a **WorkingCopy** of data that lives in Turso, built for `backtestgosqlite`'s SQL stages (files `sqlite_working_copy_*.go`, names contain `WorkingCopy`), or a **Scratch** database for one calculation or report (files `sqlite_scratch_*.go`, names contain `Scratch`). Opening SQLite anywhere else, or importing the SQLite driver outside those files, fails the test. `rg --files -g 'sqlite_*.go'` lists every place.
