# market_history.db: what trade_orchestrator needs from it

Found by searching `pkg/orchestrator` and `pkg/backtest` (they were separate repositories, `trade_orchestrator` with a vendored `backtestgosqlite`, when this was written) for `MARKET_DB`, `appenv.MarketDB`, `storage.*` and `runner.*`. Nothing here was run against a live system.

## What the file is

- SQLite file of daily session bars, table `backtest_start` (`runner.DefaultBarsTable`). One row per symbol per session.
- **Location:** `$MARKET_DB` if set (`appconfig.Config.MarketDB`), else `appenv.MarketDB()` (`$APP_FOLDER/data/market_history_hourly.db`). On App Engine the config object sets `MARKET_DB=/tmp/trade_orchestrator/market_history.db`.
- **Not read-only from this repo.** The README says nothing here writes it; that is wrong. `scan_market` writes it on every run (see below).
- Only the symbols the allowlisted strategies require are kept, about two years of bars (a few hundred kilobytes).

## Jobs that depend on it

| Consumer | Code | Reads | Writes | If the file is missing or stale |
|---|---|---|---|---|
| `scan_market` (18:00 ET Sun-Thu cron; also the scan inside `run_e2e_live`) | `pkg/orchestrator/scan_market/scan.go`, `signal_scan.go` → `runner.RunLiveScan` | Recent bars for every required symbol; the last bar sets the scan's `as_of` and next session | **Yes.** `AutoDownload` refreshes the live window from the data source before scanning (`RunDownloadContext`); creates the file and the table if absent | A missing file is created and filled. A download failure aborts the scan (`MARKET_DATA_REFRESH_FAILED`). A stale tip aborts it too (`STALE_MARKET_DATA`) |
| Markov `Prepare` (inside every live scan) | `pkg/backtest/markov_strategy/strategy.go` | `SymbolLastDate`, then bars after the saved model's last date | `markov_models.db` (extends the model's scores), not the market file | Errors `no daily bars for <sym>` or `the model is behind the market data` |
| `ready` / `/ready` page / `ready` cron (09:00 and 17:30 ET) | `pkg/orchestrator/ready/ready.go` `marketCheck`, `modelsCheck` | Last daily bar per required symbol | none | `market_data`: **warn** if the file is absent or empty ("the scan creates it"); **bad** if a symbol has no bars or is behind. `markov_models` uses the market tip to tell "will be caught up by the next scan" (warn) from stale (bad) |
| `live_vs_backtest_trades` | `pkg/orchestrator/live_vs_backtest_trades/run.go` `LoadBars`, `assemble.go` `applyModel` | Bars of table `backtest_start` (`--market-db`, default `MARKET_DB`) replayed against the live ledger | A throwaway calc database only | Missing file: live side still written, `model_outcome` = `NO_BARS` |
| `live_vs_backtest_report` job and service (`deploy_gae/live_vs_backtest_report.yaml`) | `pkg/orchestrator/live_vs_backtest_report/run.go`, `serve.go` | Same replay as `live_vs_backtest_trades` | Its own report DB. Must differ from the market file (`samePath` check) | Same as above |
| `run_e2e_live` | `pkg/orchestrator/run_e2e_live/run_e2e_live.go:407` | A temp `market_history.db` under its own temp folder | Same temp file | Not the live file |

## Jobs that do not depend on it

`account_state`, `risk`, `stage_orders`, `submit_orders`, `manage_exits`, `exit_market`, `trigger_limits`, `check_health`, `weekly_digest`, `live_vs_backtest_winrate` and the dashboard/ops pages never open it. Staging and submitting use the scan's rows in the orchestrator database (`strategy_signals`, `staged_orders`) and broker quotes. So a missing market file stops **the scan** (and with it tomorrow's entries), not exits, risk or account sync.

## External services it pulls in

- **Bars source** (`MARKET_DATA_SOURCE`): `alpaca_daily` (default on a deployment), `alpaca` (hourly, local default) or `schwab` (lags a night; fails an evening scan with `STALE_MARKET_DATA`). Alpaca needs `ALPACA_KEY` and `ALPACA_SECRET` in the config object; `schwab` needs a valid Schwab token.
- **Other databases the same scan needs**, all rewritten from the compiled-in bundle on every App Engine start (`pkg/orchestrator/strategies/bundle.go`): `refdata/strategies.db`, `data/markov_models.db`, `data/tree_models.db`. Markov scores added during a scan are lost on a cold start and are recomputed from the market file by `Prepare`; that only works if the market file reaches back past each model's `trained_through`.

## On Turso: bars are kept in `market_bars`

Nothing restores the market file from GCS. Instead `scan_market` (`pkg/orchestrator/scan_market/market_bars.go`, hooked into `Run` and `RunSignalScan`) keeps the daily bars in the `market_bars` table of the orchestrator database:

- Before `RunLiveScan`, `HydrateBars` copies the stored bars of the **required symbols only** (`runner.RequiredSymbolsFor` of the scanned strategies, the same set the scan downloads) from the last 2 years into the local `backtest_start`.
- After it (also after a failed scan, so a finished download is kept), `PersistBars` writes back only sessions newer than what Turso already has for each symbol, starting at the newest stored session in case it was provisional. Batches of 200 rows.
- It runs only when `TURSO_DATABASE_URL` is set (never under `go test`). A failure is logged in the scan details and never fails the scan: the table is a cache of the data source.
- Size: about 250 rows a year per symbol, 8 columns, so a handful of symbols is a few thousand rows once, then a few rows per symbol per night. A symbol no strategy requires is never copied.
- Still true: the first scan on a brand-new database downloads the window from Alpaca (needs `ALPACA_KEY` / `ALPACA_SECRET`), and `/ready` shows `market_data` as warn on a cold instance until a scan has run, because the ready check looks at the local file, not Turso.
- Not covered: the Markov models' extended scores are still lost on a cold start (the bundle is rewritten) and are recomputed from these bars on the next scan.

## Environment variables

`MARKET_DB`, `MARKET_DATA_SOURCE`, `ALPACA_KEY`, `ALPACA_SECRET`, `APP_FOLDER`, `GCS_BUCKET`, `TURSO_DATABASE_URL`.
