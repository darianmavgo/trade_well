# Orchestrator API

One binary, `cmd/orchestrator`, has three ways in. Each row below is one capability, joined across them:

- **Subcommand**: `orchestrator <subcommand>` on a laptop or in CI.
- **Web endpoint**: a page or action on the dashboard server (`orchestrator serve`, and the App Engine service). Browser routes render HTML; `?format=json` returns the same data where the page supports it.
- **Cron endpoint**: `GET /cron/<job>`, called by App Engine cron on the schedule in `deploy_gae/cron.yaml`. All times are Eastern.

A subcommand and its `/cron/<job>` route call the same `pkg/orchestrator/<job>` function (rule 2 in `../../docs/CLAUDE.md`). A dash means the capability has no entry point of that kind.

Derived on 2026-10-26 from `cmd/orchestrator/main.go` (subcommands), `pkg/orchestrator/appserve` and `pkg/orchestrator/dashboard` (web routes), `pkg/orchestrator/appserve/cron.go` (`cronJobs`) and `deploy_gae/cron.yaml` (schedules). `TestScheduleMatchesCronYAML` keeps `cron.yaml` and `pkg/orchestrator/job_runs/schedule.go` in step; this table is not checked by a test, so re-derive it when a route is added.

## The table

| Description | Subcommand | Web endpoint | Cron endpoint (schedule) |
|---|---|---|---|
| **Scheduled jobs, in pipeline order** | | | |
| Mirror the configured Schwab accounts, positions, orders and transactions into Turso | `account_state` | `GET /account_state` (runs the sync and streams its progress) | `/cron/account_state` (09:15 and 16:15, Mon-Fri) |
| Record each account's equity and set what new entries may do from drawdown, daily loss and exposure (exits never limited) | `risk` (`--reset-peak` after a withdrawal) | `GET /risk` (read-only) | `/cron/risk` (09:17 and 16:18, Mon-Fri) |
| Run the allowlisted strategies over the market data and record buy signals (never places orders) | `scan_market` | `GET /scan` (why each strategy did or did not enter) | `/cron/scan_market` (18:00, Sun-Thu) |
| Size the latest buy signals against live cash and write staged bracket orders | `stage_orders` | `GET /stage_orders` (dry-run preview, never writes) | `/cron/stage_orders` (18:15, Sun-Thu) |
| Re-validate staged orders, place the brackets, confirm which entries filled | `submit_orders` | - | `/cron/submit_orders` (09:28, 09:45 and 16:20, Mon-Fri) |
| Seat the limit entries `stage_orders` held back as WATCHING, nearest to filling first; swap far ones for near ones; cancel after the close | `trigger_limits` | - | `/cron/trigger_limits` (09:20, 09:31, then every 5 minutes 09:35-16:05, Mon-Fri) |
| Check open trades against their hold window and target, and sell when due | `manage_exits` | `GET /manage_exits` (Preview and Run live buttons), `GET /manage_exit` (the table: open, pending, closed) | `/cron/manage_exits` (09:35 and 15:50, Mon-Fri) |
| Audit protective orders, scheduled exits, the strategy contract and every enabled `check_health` row | `check_health`; `health_checks` lists, enables or disables rows (`--findings` for recent results) | `GET /check_health` (last results), `POST /check_health/run` (run it) | `/cron/check_health` (10:00, Mon-Fri) |
| Pre-flight: allowlist resolves, models and market data current, Schwab token good, Turso reachable | `ready` (`--static` for the bundle only, `--email` to send when not ok) | `GET /ready` (`?strict=1` answers 503 when bad) | `/cron/ready` (09:00 and 17:30, every day; emails only when something is wrong) |
| Saturday summary email: signals, orders, job health, positions, readiness | `weekly_digest` | - | `/cron/weekly_digest` (Sat 09:00) |
| **Reading and reporting** | | | |
| Every greenlit strategy, one summary row each; one strategy's signal check, positions, hold windows and problems | `strategies` (registered vs greenlit) | `GET /strategy`, `GET /strategy/`, `GET /status/{strategy-id}` | - |
| Every Schwab order, brackets unfolded, with the real fill price beside the limit and stop sent | `orders` | `GET /orders` | - |
| Backtest in-sample, out-of-sample and live win rate side by side | `live_vs_backtest_winrate` | `GET /live_vs_backtest_winrate` | - |
| Copy the newest in-sample and held-out backtest of each greenlit strategy into the archive | `live_vs_backtest_sync` | - | - |
| Give closed trades the broker's real fill prices and re-measure P&L (places nothing) | `backfill_fills` | - | - |
| Capture the trades that actually happened into `trade_history` and print the ongoing live win rate against the backtest | `trade_history` | - | - (also runs at the end of every `manage_exits` run) |
| Compare each live decision with a one-share backtest replay | `live_vs_backtest_trades` | - | - |
| Entry and exit slippage and fill delay from `live_vs_backtest_trade` | `live_vs_backtest_slippage` | - | - |
| Run both reconciliations, write `live_vs_backtest_report.db` and an HTML page | `live_vs_backtest_report` | `GET /` on the separate `live-vs-backtest-report` service (`deploy_gae/live_vs_backtest_report.yaml`), not on the trading service | - |
| Open strategy-tracked trades | `active_trades` | `GET /manage_exit` (a superset) | - |
| Staged orders at any status | `staged_orders` | `GET /` table viewer (reads `GET /api/tables`, table `stage_order`) | - |
| **Configuration and strategy deployment** | | | |
| Show or change `STRATEGY_ALLOWLIST` in the config object (`show`, `set`, `add`, `remove`) | `strategy_allowlist` | `GET /allowlist`, `POST /allowlist`; `GET /config` (config with secrets hidden, and a way to try a list before saving) | - |
| What the process is running with (secrets masked), including `STRATEGY_ALLOWLIST` | - | `GET /env` | - |
| Put one strategy row live: export the bundle, build, check, deploy, add it to the allowlist, restart | `deploy_strategy` (`--dry-run` prints the plan, `--no-restart` stops before the last deploy) | - | - |
| **Operating the deployed service** | | | |
| Board of every cron job: last run, duration, next slot. A Run button posts to `/ops/run`, which calls the same `/cron/<job>` handler a schedule uses | - | `GET /ops`, `POST /ops/run` | (calls the routes in the rows above) |
| Trigger a job on the deployed service by hand with your Google ID token | `cron <job>` or `cron list` (`--url`, `--dry-run`) | - | calls `/cron/<job>` |
| Run the whole daily pipeline once, live, and check it worked | `run_e2e_live` (no dry run) | `GET /run_e2e_live`, `POST /run_e2e_live` (one-time token required) | calls the `/cron/*` handlers in-process; not scheduled |
| Run check_health, manage_exits, scan_market and submit_orders in sequence | `run_all_jobs` | - | - |
| Turn every `/cron` route off (they answer 503) and cancel running jobs, or turn them back on | `killswitch on\|off\|status` | `GET /killswitch`, `POST /killswitch` | affects every `/cron/*` route |
| Cancel all working orders and sell every long position at market | `exit_market` (`--dry-run` previews; never scheduled) | `GET /exit_market`, `POST /exit_market` | - |
| Retire staged orders and orphan trades of strategies no longer greenlit (local database only; the broker is not contacted) | `clear_stale` (`--dry-run` previews; never scheduled) | `GET /clear_stale`, `POST /clear_stale` | - |
| Rest day-limit buys far below the market until Schwab refuses one for buying power, report how many, cancel them all (no dry run) | `probe_seat` | - | - |
| **Authentication and plumbing** | | | |
| One-time Schwab OAuth setup | `auth` | `GET /auth/schwab` (Basic Auth, redirects to Schwab), `GET /auth/schwab/callback` (Schwab's redirect target) | - |
| Sync the local Schwab token with the shared copy in GCS | `token pull\|push` | - | - |
| Print the detected runtime (`local` or `gae`), project, service and version | `runtime` | `GET /runtime` | - |
| Run the dashboard server alone (no `/cron`), or the full server (dashboard, `/cron/*`, OAuth) | `dashboard`, `serve [--port]` | serves every web endpoint above (`dashboard` omits `/cron/*`) | serves every cron endpoint above |
| App Engine health and warm-up | - | `GET /_ah/warmup`, `GET /_ah/health` | - |
| Usage text | `help`, `--help`, `-h` | - | - |

## Who may call what

| Surface | Access |
|---|---|
| Web pages and actions | HTTP Basic Auth with `DASHBOARD_USERNAME` and `DASHBOARD_PASSWORD`. `GET /auth/schwab/callback` and `/_ah/*` are open (they reveal nothing). `POST /run_e2e_live` also needs the one-time token its page hands out. |
| `/cron/<job>` | App Engine cron, or a Google account listed in `CRON_ALLOWED_EMAILS` presenting an ID token (what `orchestrator cron` sends). Refused with 503 while the killswitch is on. |
| Subcommands | Whoever can run the binary with the config object's credentials. Jobs are live by default and `--dry-run` previews (rule 6); `probe_seat` and `run_e2e_live` have no dry run. |

## Notes

- `scan_market` on the cron route only records signals in `scan_market_buy_signal`. The CLI form still stages orders inline as well; `stage_orders` is the step that sizes them on the deployed service.
- `/cron/trigger_limits` records a `job_runs` row only when it placed or cancelled an order, because it runs every five minutes and usually finds nothing to do.
- `deploy_gae/cron.yaml` lists `submit_orders` three times: 09:28 submits, 09:45 confirms fills, 16:20 closes out the day's entries before the 18:15 staging.
- `dashboard` and `serve` take `--port` (default 8080); on App Engine the binary serves automatically when started with no arguments.
