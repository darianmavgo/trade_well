# Turso end-to-end report

> **Archived 2026-10-27.** Historical record, not maintained. Paths and statements refer to the old three-repo layout (`backtestgosqlite`, `trade_orchestrator`, `schwaber` as separate repositories, Litestream, `schwaber.db`); the code is now under `pkg/backtest`, `pkg/orchestrator` and `pkg/schwaber`.

Run on 2026-10-15 after hours (22:23 to 22:32 ET) from a Mac, against Turso database `tradeorchestrator` (aws-us-east-2), with `orchestrator run_e2e_live` (live, real Schwab accounts), then the local web server on port 18080. The database was the Litestream snapshot loaded the same day (36 tables, row counts verified equal). Branch `turso`. Nothing was deployed.

## Result

`run_e2e_live` **passed** (exit 0, "All checks PASSED"), 482 s. Steps: account_state, scan_market, stage_orders, submit_orders (twice, idempotent), check_health, manage_exits, account_state, check_health. The scan found 0 signals, so **no order was staged, placed or cancelled**. The order-writing paths were therefore not exercised against Turso.

Writes persisted in Turso: 9 new `job_run` rows plus the `run_e2e_live` row (94 in total), `send_email` 63 to 70, `check_health_finding` 374 to 380, `scan_market_trace` 60 to 80, `account_state_account.fetched_at` refreshed. Reads and writes through `database/sql` worked, including date columns after the adapter fix below.

## What broke or is wrong

| # | Finding | Evidence | Severity |
|---|---|---|---|
| 1 | **Everything is about 5 to 10 times slower.** Every query is a remote round trip (about 0.45 to 0.7 s from this Mac with `select 1`). | Recorded job durations vs the earlier average on the local file: account_state 73 s vs 6.9 s, check_health 48 to 52 s vs 5.7 s, manage_exits 35 s vs 5.7 s. Pages: `/ready` 11 s, `/ops` 10 s, `/risk` 8 s, `/manage_exit` 8 s, `/orders` 4.5 s. A large part of every request is `schemas.EnsureAll` and `dashboard.EnsureSchema` running dozens of `CREATE ... IF NOT EXISTS` statements one by one on each request (about 15 s of the 27 s submit_orders request before its first log line). | High for latency, not for correctness. A cron request still finishes far inside the App Engine limit, but each page is slow and any job that loops over rows will multiply it. |
| 2 | **`/live_vs_backtest_winrate` returns 500**: `failed to execute SQL: stream is closed: driver: bad connection`. | Its queries use a pinned connection with temp tables, which a remote database cannot keep. `live_vs_backtest_winrate refresh` has the same problem, plus `ATTACH DATABASE`. | Page broken on Turso. Known before the run. |
| 3 | **check_health email goes out without its attachment**: `appdb not found at /tmp/trade_orchestrator/orchestrator.db ... (email sent without attachment)`. | Logged on both check_health runs. The code attaches a copy of the database file; under Turso there is no file. | Low. The email still sends; the attachment is lost. |
| 4 | **Date columns came back as strings** (found earlier, fixed). | `risk` failed with `Scan error ... fetched_at ... string into *time.Time`. Fixed by the adapter `schwaber/pkg/schwaber/dbutil/turso_time.go`. | Fixed. |
| 5 | **`/dbsync` page** returns an empty record (`job_name ""`, times `0001-01-01`). | Expected: dbsync is off. The page does not say "on Turso". | Cosmetic. |

## Not broken (checked)

- `ready`: database_sync reports "database is on Turso"; killswitch, cash, scheduled_jobs OK.
- `risk --dry-run`, `stage_orders`, `submit_orders` (twice, no duplicate), `manage_exits`, `check_health`, `account_state`: all HTTP 200 and expected results.
- Pages `/`, `/killswitch`, `/config`, `/runtime`, `/ready?format=json`, `/risk?format=json` returned HTML or JSON as designed.
- Report emails (scan_market, submit_orders, check_health) were sent.

## Not tested

- Real order placement, fills, cancellations, partial fills, `exit_market` (no signals today).
- The killswitch write path (`/killswitch` page was only read), `/clear_stale`, `live_vs_backtest_report`.
- Behaviour from App Engine itself. The round trip from the real GAE region may be much shorter than from this Mac.
- Two writers at once. Turso allows it, but the order-intent claim logic was only run by one process.
- The existing `var sendFailureEmail` fake is unchanged.

## Suggested fixes, in order

1. Run the schema ensure once per process (or when a version row in the database differs), not on every request. It removes most of finding 1.
2. Compare the round trip from App Engine; if it is still above 100 ms, batch the multi-query pages.
3. LiveVsBacktestWinrate on Turso: do its calculation on a local temp database, then write the result rows, as the project's SQL-slice rules already prefer.
4. Make the check_health attachment come from a copy exported from Turso, or drop it.
