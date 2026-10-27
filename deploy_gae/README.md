# App Engine Standard deploy (`deploy_gae`)

> **CANONICAL SCHEDULER:** `cron.yaml` here is the one market-hours clock.

Deploys `trade_orchestrator` to **App Engine Standard (Go)** with your existing `gcloud` auth: the
dashboard HTTP service plus the `cron.yaml` market-hours job triggers. The operational database is
**Turso** (libSQL); there is no per-instance SQLite file to sync. Deploying **replaces the running
version** of the default service.

## Layout

| File | Purpose |
|------|---------|
| `app.yaml` | Go 1.26 Standard service, max 1 instance. No `env_variables` (a test enforces it): configuration is the GCS object below |
| `config.sh` | The app's whole configuration is `gs://<bucket>/config/env` (replaces Secret Manager, `app.yaml` env and `.env`): `migrate`, `keys`, `list`, `edit`, `set`, `unset`, `pull`, `push` |
| `clean_deploy_env.sh` | Scans the code and deploy scripts for every environment variable they read, pulls the live config object, drops the variables nothing refers to and uploads the result (the old object stays as `config/env.prev`). `--dry-run` shows the removals without changing anything; `--scan-only` and `--from-file F [--out G]` work offline. Names only are ever printed. |
| `cron.yaml` | Market-hours schedule → `/cron/*` |
| `dispatch.yaml`, `index.yaml` | Routing and datastore index config |
| `live_vs_backtest_report.yaml`, `deploy_live_vs_backtest_report.sh` | The separate `live-vs-backtest-report` service |
| `enable_apis_service_accounts.sh` | Enable APIs + `gcloud app create` |
| `setup_billing.sh` | Verify billing linked |
| `setup_cron_access.sh` | Allow your Google account to trigger `/cron/<job>` by hand (`orchestrator cron <job>`): adds your email to `CRON_ALLOWED_EMAILS` in the config object; `--deploy`, `--verify`, `--dry-run` |
| `setup_storage.sh` | Bucket IAM for the App Engine and Compute service accounts (the bucket holds the config object and the Schwab token) |
| `setup_project.sh` | Full bootstrap |
| `setup_turso.sh` | Writes `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN` into the config object. Token from `$TURSO_AUTH_TOKEN` or a hidden prompt. `--dry-run` previews |
| `turso_check.sh` | Read-only: `SELECT 1` against Turso over HTTPS using the config object's credentials; lists tables |
| `seed_turso.sh` | One-time `sqlite3 .dump` of a local DB into a Turso database (writes to Turso; `--dry-run` previews) |
| `deploy.sh` | `gcloud app deploy` app + cron, behind a Turso gate: both keys present and the database answers (`SKIP_TURSO_CHECK=1` skips the query). Cache in `.deploy_cache/gae_turso` |
| `manage_gae.sh` | `status`, `logs`, `open`, `cron`, `versions`, `turso` (runs `turso_check.sh`) |

Local launchers live beside this folder: `deploy_live_local/servelocal.sh` (the app against live Turso
on port 8082) and `deploy_report_local/servelocal.sh` (the report service on port 8081).

## Order for a new project

1. `turso db create` (or reuse the existing database), `turso db tokens create <db>`.
2. `deploy_gae/setup_project.sh` (billing, APIs, storage), then `deploy_gae/config.sh migrate`.
3. `deploy_gae/setup_turso.sh`
4. `deploy_gae/seed_turso.sh <orchestrator.db> <db> --dry-run`, then without `--dry-run`.
5. `deploy_gae/turso_check.sh`
6. `deploy_gae/deploy.sh`, then open `/ready`.

Overrides for `deploy.sh`: `FORCE_SETUP=1` re-checks APIs and storage; `FORCE_DEPLOY=1` redeploys app and
cron even when the source hashes match; `FORCE_DEPLOY=app` redeploys the app only.

There is no fallback to a local database: without Turso a version loses its data when the instance
stops, and `/ready` reports it as bad.

## Runtime model

- **Entrypoint:** `cmd/orchestrator` (same CLI binary; on GAE with no args it auto-serves dashboard + `/cron/*`).
- **Database:** Turso, over HTTPS with the pure-Go `libsql-client-go` (no cgo). The instance's disk
  (`/tmp/trade_orchestrator`) holds only the market file, bundled models and the token, all rebuilt on start.
- **Market data:** `scan_market` keeps the daily bars of the allowlisted strategies' symbols (last 2 years)
  in the `market_bars` table and reloads them into the local file before each scan. See
  `docs/MarketHistoryDependencies.md`.
- **Configuration:** the App Engine default service account reads `gs://<bucket>/config/env`.
  `main()` loads it into the environment at process start (`config.Bootstrap`); no Secret Manager.
  The Schwab token is `gs://<bucket>/schwab-token/<env>.json`.
- **Cron auth:** handlers require `X-Appengine-Cron: true` (sent by App Engine Cron).

## Schedule

The schedule is `cron.yaml` (America/New_York). `pkg/job_runs/schedule.go` mirrors it and a test parses
the file and fails on any difference, so edit both together. Jobs and their routes are listed in the
repo `README.md`.
