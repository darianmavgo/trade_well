# Trade Orchestrator

> **Moved 2026-10-27.** This was the `trade_orchestrator` repository README. The code is now `pkg/orchestrator/` in the single `trade_well` module; paths in this document are relative to the repo root (`cmd/`, `sql/`, `refdata/`, `data/`, `deploy_*` stay at the root). Build with `make build`; binaries land in `bin/`.

**Trade Orchestrator** is the central execution and monitoring application for your automated trading system. It bridges the gap between intelligence (strategy generation) and execution (API communication).

> [!IMPORTANT]
> This repository strictly adheres to a separation of concerns architectural model. It contains **no strategy logic** and makes **no direct API calls** to Schwab.

> **CLI is the source of truth.** Every scheduled job is a thin wrapper around > `orchestrator <subcommand> …` with the same flags you’d run by hand. > Don’t add a second code path for cron.

## Architecture & Ecosystem

The repo has three layers, each a directory under `pkg/`. Imports point one way, down (a test, `pkg/layers`, fails the build otherwise):

1. **`pkg/schwaber/`** (Foundation, the "Hands"): all connectivity, reads, writes and authentication with the Charles Schwab API, plus shared infrastructure (config, DB open, token sync). The orchestrator imports it to execute trades and fetch balances. Its own README is [../schwaber/README.md](../schwaber/README.md).
2. **`pkg/backtest/`** (Calculation, the "Brain"): all algorithmic strategies and backtesting pipelines. The orchestrator imports `pkg/backtest/strategy`, `pkg/backtest/storage`, `pkg/backtest/models` and `pkg/backtest/runner` to resolve strategies and generate live signals in-process. Its own README is [../backtest/README.md](../backtest/README.md).
3. **`pkg/orchestrator/`** (Orchestration, this document): when jobs run and in what order. It contains no strategy logic and makes no direct Schwab API calls; it wires the two layers above together.

These used to be three repositories (`schwaber`, `backtestgosqlite`, `trade_orchestrator`) joined by `replace` directives and a vendored copy; there is now one module, so no `replace`, no sibling checkouts and no GitHub access needed to build.

`$MARKET_DB` (or `$APP_FOLDER/$APP_DATA/market_history.db`, resolved by `pkg/backtest/appenv`) locates `market_history.db`, the historical bar database strategies scan against.

---

## Strategies that are database rows

A strategy such as `streak-voo-buy-tecl` or `markov_model_amd` is a row of `refdata/strategies.db`, not code. This app cannot carry that 65,000-row file, so `pkg/orchestrator/strategies/deploy/strategies.db` holds just the rows it trades, embedded in the binary. `strategies.ResolveGreenlit` (every job's entry point) writes it to `$APP_FOLDER/refdata/strategies.db` when no database is there, and always on App Engine, then registers the strategy families from it. An allowlist entry that is a row but is not in the bundle fails at start-up with a message naming `strategy export`.

Regenerate the bundle whenever the allowlist gains such a strategy (run from the repo root; then `go mod vendor` if you deploy with a vendored build):

```bash
./bin/strategy export -ids "<every allowlist entry>" -out pkg/orchestrator/strategies/deploy/strategies.db -models-out pkg/orchestrator/strategies/deploy
```

`-models-out` also writes `markov_models.db` and `tree_models.db` there, holding only the trained models of the exported rows' signal symbols (the full `data/markov_models.db` is over 500 MB, `tree_models.db` a few MB). They are embedded the same way and placed at `$APP_FOLDER/data/{markov,tree}_models.db` (always on App Engine, otherwise only when missing). The export fails naming any signal symbol with no trained model: run `train markov -symbols X` or `train tree -symbols X` in `backtestgosqlite` first. Markov models are refreshed on the instance by the scan; tree models are not, so re-export after retraining. `markov_hmm` rows read `hmm_regime.db` (from `study hmm_regime`), which is not bundled yet.

The `hmm` family (`hmm-calm-qqq`, `hmm-calmdown-qqq`) trades QQQ from the QQQ hidden Markov model. The rows travel in the same bundle (`hmm_strategy` table) and the model in `hmm_models.db` (written by `scripts/export_strategies.sh` from `train hmm export`). The scan extends the model itself (`strategy.Preparer`): it downloads QQQ's daily bars from Yahoo and runs the filter with the parameters frozen at `trained_through`, so a cold instance needs nothing but the bundle; `pkg/orchestrator/modelstate` keeps the predictions in Turso, so only sessions nobody has predicted are computed. `hmm-calm-soxl` (the QQQ model's calm rule traded on SOXL) was added to `STRATEGY_ALLOWLIST` and deployed on 2026-10-23 as its own entry at 20% of equity; the two QQQ strategies are not in the allowlist. Adding one is a live-trading change: export the bundle, set the allowlist (`/allowlist` or `orchestrator strategy_allowlist add`) and deploy. The regimes are volatility regimes, so a rule uses the stressed regime, found by volatility and never by state number.

The bundle now holds the clean stack `streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull` (primary `streak-voo-buy-tecl` at 65% of equity, each markov strategy 25%). Markov strategies need predictions through the last completed session. The scan adds them itself: after refreshing the bars it extends each saved markov model (`strategy.Preparer`) with counts frozen at the model's `trained_through`, the same as the out-of-sample backtest, so nothing is refit on the instance and a failure stops the scan with `PREPARE_FAILED`. The scan downloads about one year of daily bars per symbol.

`STRATEGY_ALLOWLIST` lives in the config object, `gs://<bucket>/config/env`, not in a file: `deploy_gae/config.sh` only seeds a new object, and it now seeds `streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull,tsll-daily-one-share`. The object that is live is unchanged, and local runs read it too. To make the clean stack live, set it there with `deploy_gae/config.sh set STRATEGY_ALLOWLIST="streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull,tsll-daily-one-share"` and then deploy this build. The previous entry `sig-voo-buy-tecl+mara_tree+pdd_tree` does not resolve in this build: `sig-voo-buy-tecl` is now `streak-voo-buy-tecl`, and the tree strategies (`mara_tree`, `pdd_tree`) are not bundled. Because every job resolves the whole allowlist, an entry that does not resolve stops all of them, including `manage_exits`, so change the object and deploy together.

`STACK_SCAN_LIVE=1 go test ./pkg/strategies -run TestCleanStackScansEndToEnd -v` runs the cron route's scan on the stack from an empty folder, with real downloads and training. It never touches Schwab and sends no email.

---

## The Job Pipeline

Every job reads and writes the **same single Turso database** (`TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN` in the config object; it is required everywhere, laptop included, and there is no local SQLite file). There is no message queue or separate coordination service — the database rows *are* the state machine. A job run's job is to move rows from one status to the next; the next job picks up wherever the last one left off.

The pipeline deliberately splits **signal detection** from **live order submission** into two separate jobs (`scan_market` and `submit_orders`) connected by a staging table (`staged_orders`). Scanning never talks to the Schwab order-placement API; submission always re-validates against the account's *current* state (cash, existing positions) rather than trusting whatever was true when the scan ran the night before.

The typical recurring daily cadence (see "When they run" below) looks like this:

```
scan_market ──▶ account_state ──▶ submit_orders ──▶ manage_exits ──▶ check_health
 (evening,          (morning     (validate+place)  (age & close)   (audit)
  stage + email)      sync)
                                        │
                                        ▼
                                  active_trades (OPEN)
                                        │
                          held < max_trading_days? ──▶ still OPEN, days_held++
                                        │
                          held >= max_trading_days ──▶ market sell, CLOSED_TIME_EXPIRATION
```

`run_all_jobs` still exists as a manual, on-demand command — it runs `check_health → manage_exits → scan_market → submit_orders` in that order, once, against every resolved account — but it is **not** on the recurring schedule below; each job now runs independently on its own trigger.

### Jobs, in pipeline order

| # | Command | What it does | Reads | Writes |
|---|---|---|---|---|
| 0 | `auth` | One-time OAuth handshake with Schwab; persists the access/refresh token. Not part of the recurring cycle — run once, then whenever the refresh token needs re-issuing. | — | `token.json` (via schwaber) |
| 1 | `scan_market` | Every requested ID — a plain strategy or a `+`-joined stack — runs through the same stack scanner as one cash ledger; a plain ID is just a stack of 1. For every symbol a strategy requires, generates a signal from recent bars in `market_history.db`. A symbol with no signal is logged as `NO_BUY`. A symbol with an existing open position, **or with an existing `PENDING` staged order for that account**, is skipped — a re-run before `submit_orders` drains the queue (a retried job, a manual re-run) cannot stage a duplicate buy for the same signal. Each member sizes to its own `AllocationPct` of live account equity — primary first, then overlays (if any) on leftover cash only, never selling one to fund another (live v1). No live account balance means no sizing basis, so that member is skipped with a clear reason rather than a guessed dollar amount. Each filled BUY becomes its own `staged_orders` row with that member's TP/SL/hold. Live runs fold every buy into one HTML email. **It never calls Schwab's order-placement API itself.** | `market_history.db`, `active_trades` / broker positions (to skip symbols already held) | `strategy_signals`, `staged_orders` |
| 1a | `stage_orders` | Takes the latest scan's `NEW` rows in `scan_market_buy_signal`, sizes them against each configured account's live cash/equity (primary first, overlays get leftover cash), and writes `stage_order` rows for `submit_orders`. Each buy signal is then marked `STAGED` (linked to its `stage_order`) or `DISMISSED` (expired, superseded, or skipped), so a rerun never stages it twice. Same code from `orchestrator stage_orders [--dry-run|--live]` and `GET /cron/stage_orders[?dry_run=1]`. |
| 2 | `account_state` | Pulls current account/position/order/transaction state from the live Schwab API **for `SCHWAB_ACCOUNT_NUMBERS` only** (not every linked Schwab account) and mirrors it into SQLite. Every other job that needs balances or positions reads what `account_state` last wrote (or, for jobs with live API access, re-fetches directly). | Schwab API | `accounts`, `positions`, `orders`, `order_legs`, `transactions`, `user_preferences`, `raw_responses` |
| 3 | `submit_orders` | For every `PENDING` row in `staged_orders` (oldest first): if a PENDING row for the same symbol was already handled earlier in this run, this one is rejected outright as a duplicate — a defense-in-depth backstop in case duplicates were staged before that guard existed, or slipped in some other way. Otherwise rebuilds the bracket order, re-validates it against the account's cash/positions *as of right now* (not as of when it was staged), and only then submits it live. Safety rejection or an API failure marks the row `REJECTED` with a reason; success marks it `SUBMITTED` and opens a new tracked position. | `staged_orders`, live account state (Schwab API) | `staged_orders`, `strategy_signals`, `active_trades` |
| 3a | `trigger_limits` | For a strategy that puts a day limit on many names at once (`StrategyConfig.EntryLimitPct`, e.g. the sector-pairs rotation: 48 ETFs, each 20% of equity), `stage_orders` stages the orders as `WATCHING` instead of `PENDING`, claiming no cash, because a working buy order reserves its cash at the broker and a Roth IRA cannot fund them all. This job runs every 5 minutes from 09:35 to 16:05 ET. When a watched name's low of the day has reached its limit it sizes the order against the cash left at that moment (alloc % of equity, at most 95% of cash), makes it `PENDING`, and runs `submit_orders` so it is placed as a **DAY** limit bracket (entry expires at the close; the take-profit and stop legs are GTC). At the close it cancels the ones never reached (`CANCELLED`, "day limit expired unfilled"). A name already held, or one the strategy took out of the rotation for the month, is cancelled instead of placed. That rule is `StrategyConfig.OneFillPerMonth`: once a name's entry has filled (a `manage_exit` row opened in that New York calendar month), `stage_orders` skips its signals until the first session of the next month, and this job checks again when the limit triggers. **Seating (pre-open, `Seat`):** at 09:20 ET, and again at 09:31 and every pass until 09:50, it also places the day's watched limits at the broker, nearest to filling first (distance = how far the ask must fall to reach the limit, limits being fixed from the prior close), one at a time through `submit_orders`, until Schwab refuses one for buying power (the refusal, not a computed count, is the signal, so cash, margin and each account's balance all work). The refused row goes back to `WATCHING`. From 09:31 to 15:30 each pass also swaps: if a watched name is more than 0.3% nearer its limit than the farthest resting order, that order is cancelled (waiting for Schwab to report `CANCELED` so the buying power is free), re-watched, and the nearer name seated. If Schwab words its buying-power refusal differently from the phrases in `IsBuyingPowerRefusal`, the full refusal is in the log and the report row. It does nothing when nothing is being watched, and records a `job_run` only when it seated, swapped, placed or cancelled something. | `stage_order` (`WATCHING` rows), broker quotes | `stage_order` |
| 4 | `manage_exits` | For every `OPEN` row in `active_trades`: bind any working SELL LIMIT as `exit_order_id`; if that sell has **filled** or last price is through the +5% target, market-sell (if needed) and close as `CLOSED_PROFIT`; if held ≥ `max_trading_days`, cancel the rest, **submit a market sell**, close as `CLOSED_TIME_EXPIRATION`. PlaceOrder failures stay `OPEN` and are reported in the run's email (see [Email Reports](#email-reports)). Held positions with no `manage_exit` row are **adopted** first (one strategy trades the symbol, it has a hold window, Schwab shows a filled buy): the row gets a fill-based exit date; otherwise they are reported `UNTRACKED` and not sold. Cron route `/cron/manage_exits` admits only App Engine cron (a browser gets `forbidden`); from a browser use `/manage_exits` (dashboard auth): a Preview button (dry run: prints each row, sells and writes nothing) and a Run live button, both through the same handler. | `active_trades`, live orders + quotes | `active_trades` |
| 5 | `check_health` | **Priority: current positions.** First, every held position is audited against the books and its strategy (`pkg/orchestrator/check_health/positions.go`, table `check_health_position`): an OPEN `manage_exit` row exists (else `position_untracked`, CRITICAL), its strategy is known (`position_strategy_unknown`), the entry order and the broker-confirmed fill are recorded, the exit date exists, is not overdue and equals the fill date plus the strategy's hold window (`position_exit_date_mismatch`), and the take-profit and stop-loss Schwab is holding match what the strategy dictates for the entry price. The results show at the top of the `/check_health` page, of the run's report and of the `/cron/check_health` response. Then, three layers of checks, all persisted as `health_check_findings` rows. **Live-data checks** (built into Go, need broker truth): each open position — working take-profit? stop-loss? matching `active_trades` schedule? tracked quantity matching the broker's? a take-profit resting at/below cost? — plus orphaned working sell orders with zero matching shares. **Strategy-contract audit** (`pkg/orchestrator/check_health/contract.go`): every `PENDING` staged order and every `OPEN` active trade has its recorded hold window, take-profit, and stop-loss re-checked against what its originating strategy's own `DefaultConfig()` actually dictates right now — a real mismatch (not float rounding) is a `WARNING` (may be an intentional `-max-days`/`-profit` override), a strategy no longer in the registry is `CRITICAL`, and a `PENDING` order's notional grossly exceeding its strategy's `AllocationPct` of today's equity is flagged too (under-allocation, e.g. a leftover-cash-capped overlay, is normal and never flagged). **Data-driven checks**: every enabled row in the `health_checks` table is run as a SQL query against the local mirror (stale time-expired trades, duplicate `OPEN` rows, stuck/rejected staged orders, stale account snapshots, negative cash, position concentration, orphaned `active_trades`) — add, edit, or `health_checks --enable`/`--disable` these without a code change. Overall status rolls up from the worst finding: `HEALTHY` → `WARNING` → `ACTION_REQUIRED`. Always appends one audit row, whether or not anything is wrong. | `accounts`, `positions`, `active_trades`, `staged_orders`, `health_checks`, the backtest layer's strategy registry, live positions/orders (Schwab API) | `health_audits`, `health_check_findings` |
| — | `health_checks` | Read-only/admin CLI for the `health_checks` table: list definitions (`health_checks`), toggle one (`--enable`/`--disable <name>`), or view recent violations (`--findings`). | `health_checks`, `health_check_findings` | `health_checks` (enabled flag only) |
| — | `run_all_jobs` | Manual/on-demand composite: runs check_health → manage_exits → scan_market → submit_orders against every resolved account, one full cycle. Not scheduled. | (all of the above) | (all of the above) |
| — | `active_trades` | Read-only CLI view of `active_trades`. | `active_trades` | — |
| — | `staged_orders` | Read-only CLI view of `staged_orders` at any status. | `staged_orders` | — |
| — | `strategies` | Read-only CLI view: every strategy `pkg/backtest` has registered, cross-referenced against `STRATEGY_ALLOWLIST` to show which are actually greenlit. | the backtest layer's strategy registry, `STRATEGY_ALLOWLIST` env | — |
| — | `dashboard` | Web UI + REST API over the same database: dumps the four job-lifecycle tables, plus a `/account_state` action that re-syncs the Schwab mirror (without printing it). Scans, submits, and health checks stay CLI/scheduler jobs. | job-lifecycle tables | `accounts`, `positions`, `orders`, `order_legs`, `transactions`, `user_preferences`, `raw_responses` (via `/schwabstate` only; not shown on `/`) |

### Buying power with 40+ limit orders

The sector-pairs rotation puts a day limit on 48 ETFs each session, each worth 20% of equity, and a working buy order reserves its cash at Schwab. `trigger_limits` (job 3a above) manages that:

1. **Stage without cash.** `stage_orders` stores the orders as `WATCHING`; they claim nothing.
2. **Seat, nearest first.** Before the open, place limits one at a time in order of distance to fill until Schwab refuses one for buying power. The refusal is the signal (`IsBuyingPowerRefusal`); no slot count is calculated, so cash, margin and each account's balance all work.
3. **Swap.** After the open, a resting order that is more than 0.3% farther from its limit than a watched name is cancelled (after Schwab reports `CANCELED`) and the nearer name takes its place.
4. **Trigger.** Every 5 minutes a name whose day low reached its limit is sized against the cash left and placed. At the close the unreached limits are cancelled.

Run it by hand with `orchestrator trigger_limits [--account N] [--strategy id,id]`. It places real orders by default (`--dry-run` previews). It first stages the greenlit limit-entry strategies' signals as `WATCHING` (when none are waiting), then seats them at Schwab nearest to filling first, at any hour (Schwab takes a day limit after the close for the next session), printing each as accepted or refused with Schwab's reason. It never cancels anything (no swaps, no expiry pass) and stops submitting after 2 refused or failed orders, a buying-power refusal counting as one. `/cron/trigger_limits` runs the full job instead (seat inside 09:00 to 15:30 ET, swap, trigger, cancel at the close).

Known gaps: the backtest holds all 48 limits at once and takes same-day fills alphabetically, so live can miss fills the backtest booked and the CAGR is an upper bound until that is measured. `orchestrator probe_seat` is meant to measure how many limits an account takes before a buying-power refusal, but its 2026-10-09 run ended on HTTP 429 (too many requests) after 4 orders, so it measured nothing; the `ProbeResult` for account 25474194 left order 1008251139902 not confirmed cancelled, so check that account's working orders by hand. The probe's 1.5 s pace needs to be slower, or its retries need backoff, before it is run again.

### When they run

On App Engine (see [`deploy_gae/cron.yaml`](../../deploy_gae/cron.yaml) for the exact schedule), the cadence is:

| Time (ET) | Days | Job |
|---|---|---|
| 18:00 | Sun-Thu (night before each trading day) | `scan_market` — find tomorrow's signals; sends one `scan_market` report email (every strategy evaluated, buy and no-buy alike) |
| 18:15 | Sun-Thu | `stage_orders` — size the 18:00 signals against live cash and write `stage_order` rows |
| 09:15 | Mon-Fri | `account_state` — sync account/position state before the day starts |
| 09:17, 16:18 | Mon-Fri | `risk` — record equity and set the entry size (full, reduced or halted) from drawdown, daily loss and exposure |
| 09:28 | Mon-Fri | `submit_orders` — submit whatever was staged the night before |
| 09:20, 09:31 | Mon-Fri | `trigger_limits` — seat the watched limits before the open (as many as the broker accepts, nearest first) and, once open, swap far resting ones for nearer names |
| 09:35–16:05, every 5 min | Mon-Fri | `trigger_limits` — place the watched limit entries whose limit the day's low reached; after the close, cancel the ones never reached (no-op when nothing is watched) |
| 09:35 | Mon-Fri | `manage_exits` — take profit if through target / dump expired window at the open |
| 15:50 | Mon-Fri | `manage_exits` — last-chance market sell of any still-open expired window before the close |
| 10:00 | Mon-Fri | `check_health` — audit protective orders and orphans |
| 16:15 | Mon-Fri | `account_state` — final reconciliation after the close |

None of this is hardcoded to App Engine cron, though — every command is a plain CLI invocation and runs the same way from cron or by hand.

### Happy path

A concrete example, one signal through the whole pipeline:

1. **Monday 18:00** `scan_market -strategy sig-voo-buy-tecl+mara_tree+pdd_tree` evaluates the stack for Tuesday's session. `TECL` (primary) is sized to ~65% of equity first; MARA then PDD use leftover cash only. Each child BUY inserts its own `staged_orders` row (`status = PENDING`) with that child's TP/SL/hold for manage_exits. A stack-level `strategy_signals` row is recorded, and the evening `scan_market` report email lists every member evaluated (TECL/MARA/PDD alike, buy or no-buy). Live v1 does not sell an open MARA/PDD lot to fund TECL — the primary sizes to remaining cash instead. **No order has touched Schwab yet.**
2. **Tuesday 09:15** `account_state` confirms account `25474194` has $16,913 cash available and no existing `TECL` position.
3. **09:28** `submit_orders` finds that `PENDING` row, re-checks the account's cash and positions *right now* (not Monday evening's snapshot), passes the safety check, and places the bracket order. `staged_orders` → `SUBMITTED` (with the real order ID). `strategy_signals` → `EXECUTED`. A new `active_trades` row opens with `status = OPEN`, `trading_days_held = 0`.
4. **09:35** `manage_exits` sees the position is brand new (0 days held, no time-window breach) and leaves it `OPEN`.
5. Every subsequent `manage_exits` run bumps `trading_days_held` by one trading day, until either:
   - the take-profit or stop-loss leg fills at the broker and the position is closed there, **or**
   - `trading_days_held` reaches the creating strategy's holding window (`max_trading_days` on the row, from `DefaultConfig().HoldingWindow` or the signal's `HoldDaysOverride`), at which point `manage_exits` cancels the resting profit-taker order, submits a market sell, and closes the row as `CLOSED_TIME_EXPIRATION` with the realized fill price and P/L recorded.
6. **10:00** `check_health` audits whatever's currently held — both its built-in live-data checks and every enabled `health_checks` row — logging one `health_audits` row plus a `health_check_findings` row per violation. **16:15** `account_state` reconciles final account state.

If a signal fails safety validation (insufficient cash, for example) at step 3 instead, `staged_orders` becomes `REJECTED` with a reason and `strategy_signals` becomes `SAFETY_REJECTED` — no position ever opens, and nothing downstream needs to know the difference.

> [!NOTE]
> Untracked broker positions (e.g. a VOO lot that was never a staged strategy buy) are **not** auto-sold. `CLOSED_STOP_LOSS` / `CLOSED_MANUAL` remain reserved.

---


**Market data for the evening scan.** `MARKET_DATA_SOURCE` picks where `scan_market` refreshes bars: `alpaca_daily` (default on App Engine; Alpaca daily bars, available as soon as a session closes), `schwab` (Schwab daily history, which lags a night behind: at 20:25 ET it still ended on the previous session, so an 18:00 ET scan fails with `STALE_MARKET_DATA`) or `alpaca` (hourly bars, the local default). Needs `ALPACA_KEY` and `ALPACA_SECRET` in the config object.

## Safety Controls

- **Strategy greenlighting**: `scan_market`/`run_all_jobs` only ever run IDs listed in `STRATEGY_ALLOWLIST` (comma-separated). A `+` id is a **stack** (one cash ledger, primary first, overlays on leftover cash) — e.g. `sig-voo-buy-tecl+mara_tree+pdd_tree`. Overlays in a stack are not independently tradable unless listed on their own. Requesting a non-greenlit id fails explicitly. Run `strategies` to see registered vs greenlit.
- **Multi-account, capped at 5**: `--account` (single) or `--accounts` (comma-separated, max 5) select which Schwab accounts a job run targets, defaulting to `SCHWAB_ACCOUNT_NUMBERS` (comma-separated list) in the config object; with neither, the job stops and says so. `account_state` only pulls those configured accounts, not every account linked on the Schwab login. There is no default account — an unconfigured run fails explicitly rather than guessing.
- **Stage-then-submit**: see the pipeline description above — `scan_market` can never place a live order.
- **Pre-trade safety guard** (in `schwaber`): every order is checked against available buying power and (for sells) actual held quantity before submission.
- **One report email per job run**: see [Email Reports](#email-reports) below.
- **Dashboard authentication**: the web dashboard requires `DASHBOARD_USERNAME`/`DASHBOARD_PASSWORD` in the config object and gates every route behind HTTP Basic Auth (constant-time comparison). It's a single shared credential, not per-user accounts — fine for a solo operator, but there's no audit trail of who did what. It refuses to start at all if either variable is unset, rather than serving account data unauthenticated.

## Allocation: how the backtest's share of equity reaches live

A live position is sized from the strategy row: `allocation_pct` of the account's equity, at most 95% of the cash left. Nothing else carries the allocation a backtest ran with, so the bundle in `pkg/orchestrator/strategies/deploy/` is exported at it:

```bash
EXPORT_ALLOC=0.20 scripts/export_strategies.sh        # default 0.20; use the -alloc the backtest ran with
go mod vendor
```

`strategy export -alloc` sets `allocation_pct` on every exported row and records it in the bundle's `bundle_meta` table. The backtest it matches is `backtest stack -alloc 0.20`: every position is that share of equity, and a signal's own allocation (the Markov pipeline's 5 to 30% by signal strength) is ignored in the run, as it is live (`scan_market` takes the row's allocation and never the signal's). A strategy sized by its own rule (`tsll-daily-one-share`, one share) is not a share of equity and is left alone.

- **`ready` checks it** (`allocation`): bad when a percent-sized allowlist member would trade a share other than the one the bundle was exported at, warn when the bundle records none. `deploy.sh`'s static check runs it too.
- **No guessed default.** A signal with no valid allocation (0, negative, above 1) is skipped with the reason `no valid allocation`; `stage_orders` used to size it at 65%, `scan_market` at 20%.
- The `risk` job's drawdown ladder scales this share down, and its exposure cap limits the total. They do not change what the bundle says the strategy is.

## Risk limits (`risk`)

`orchestrator risk [--dry-run] [--reset-peak]`, `GET /cron/risk[?dry_run=1][&reset_peak=1]` (09:17 and 16:18 ET, right after each `account_state` sync) and the `/risk` page (HTML; `?format=json`). It reads the `account_state` mirror, so it makes no broker call, and writes `risk_equity` (one row per account per session) and `risk_state` (the latest assessment). **It limits new entries only. Exits, stops and take-profits always run**, which is why this is not the killswitch (the killswitch stops every job, exits included).

For each account with equity it measures drawdown from the highest recorded equity and the change since the last recorded session, then sets what entries may do:

| Limit | Default | Effect |
|---|---|---|
| `RISK_DRAWDOWN_LADDER` | `0.10:0.5,0.20:0` | drawdown:scale pairs. 10% below the high-water mark, new entries are sized at half; 20% below, at zero (halted). Empty turns the ladder off. |
| `RISK_DAILY_LOSS_PCT` | `0.05` | Equity down this much since the last session halts new entries. `0` turns it off. |
| `RISK_MAX_GROSS_PCT` | `1.0` | Long market value may not exceed this fraction of live equity; `stage_orders` trims the cash a new entry may use. `0` turns it off. |
| `RISK_MAX_ORDER_PCT` | `0.60` | One entry order may cost at most this fraction of live equity (`submit_orders` refuses it as `RISK_LIMIT`). `0` turns it off. |
| `RISK_MAX_ORDERS_PER_DAY` | `40` | Entry orders placed per account per ET session. `0` turns it off. |
| `RISK_MAX_POSITIONS` | `60` | Open long positions per account; a buy past it is refused. `0` turns it off. |
| `RISK_AUTOKILL_DRAWDOWN` | `0.35` | The `risk` job engages the killswitch (and emails) when any account is this far below its high-water mark. Sells nothing. `0` turns it off. |
| `RISK_AUTOKILL_FAILURES` | `6` | Same, after this many failed runs in a row of `stage_orders` or `submit_orders`. `0` turns it off. |

Set them with `deploy_gae/config.sh set RISK_DRAWDOWN_LADDER=...` like any config value. A bad value stops every job at start-up rather than being ignored.

Who reads the state: `stage_orders` skips every signal when halted, scales the equity a position is sized against by the ladder step, and applies the exposure cap; `submit_orders` rejects a staged buy (`RISK_HALTED`) when halted, as the last gate before the broker; `trigger_limits` seats no new limits when halted. Live runs fail closed: if the risk state cannot be read, `stage_orders` stages nothing and `submit_orders` leaves buys PENDING (`RISK_UNREADABLE`); dry runs only warn. Tables and columns are documented in `pkg/orchestrator/risk/README.md`. An account never assessed is treated as unrestricted, and one whose mirror is more than four days old is skipped with a warning. An email goes out when an account's entry size changes. A withdrawal looks like a drawdown: run `orchestrator risk --reset-peak` afterwards to restart the high-water mark at the current equity.

Not covered yet: the state is set twice a day, so a loss during the session does not halt that day's later entries, and a halt is lifted only by equity recovering (or `--reset-peak`), not by hand.

## Database on Turso (`TURSO_DATABASE_URL`)

The orchestrator database (job runs, risk state, orders, account mirror, order intents, the scan's `market_bars`) is a remote Turso database. `TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN` come from the config object (`deploy_gae/setup_turso.sh`); the GCS bucket holds only that object and the secrets. Nothing is replicated to GCS. It is read in `pkg/schwaber/dbutil`.

- **Remote only, pure Go** (`libsql-client-go`, no cgo, builds for App Engine). `libsql://` URLs are used over HTTPS. Every query is a network call, so jobs are somewhat slower than against a local file.
- **`/ready` is the proof.** Its `database` check is bad without Turso (on any platform), bad when Turso does not answer or a table the jobs use is missing, and otherwise reports the job runs it can read.
- **Tests never use Turso.** `dbutil.Turso()` returns off under `go test`, so exported variables cannot make a test write the real database. Tests use real SQLite in a `t.TempDir()` or `:memory:` (the only local SQLite the orchestrator opens); `appconfig.Load` sets `Config.DBPath` only under `go test`, and `dbutil.Open` refuses a local file otherwise.
- **`live_vs_backtest_winrate -reports-dir …` works on Turso.** It reads each backtest asset file read-only, for only the columns the comparison needs (`pkg/orchestrator/live_vs_backtest_winrate/sql/asset_*.sql`), and upserts them into `live_vs_backtest_winrate` in one transaction. The comparison itself runs in Turso: every stage writes a slice table named for the run (`winrate_compare__r20261026t…`), the newest 3 runs are kept for audit and older ones dropped (`pkg/orchestrator/runtables`). Nothing is staged in a local database, and `--dry-run` reads the assets and writes nothing.
- The market DB (`MARKET_DB`) and the other read-only files (strategies, backtest reports) are local files on the instance; the scan rebuilds the market file from Turso `market_bars` and the data source.

## End-to-end live run (`/run_e2e_live`)

`/run_e2e_live` runs the whole daily pipeline once, in scheduler order, and checks that it worked: `account_state`, `scan_market`, `stage_orders`, `submit_orders` (twice, to confirm it is idempotent), `check_health`, `manage_exits`, `account_state`, `check_health`. **It is a live run: it stages, submits and manages real orders**, exactly like the scheduler. It replaces `scripts/run_e2e_live.sh`. The logic is `pkg/orchestrator/run_e2e_live`; the same function backs the endpoint and `orchestrator run_e2e_live --live [--oracle]`.

- **Auth:** the same Basic Auth as the dashboard (`dashboard.RequireAuth`, `DASHBOARD_USERNAME` / `DASHBOARD_PASSWORD`). It is mounted by `serve` (GAE and `deploy_live_local/servelocal.sh`), not by the dashboard-only `orchestrator dashboard`.
- **GET shows a page; only a POST runs it.** Loading the page runs nothing, so a reload, prefetch or link cannot place orders. The button submits a POST that needs the page's one-time token (Basic credentials alone are not enough: Chrome attaches cached Basic credentials to a POST sent by another site) and a same-origin request (`Sec-Fetch-Site` / `Origin`). No typing is required. One run at a time.
- **In-process, same code path:** each step calls the real `/cron/<job>` route through the server's own handler with the App Engine cron header, so it goes through the same `job_run` recording and failure emails as a scheduled run. Output streams as it runs; closing the tab does not stop it (the run continues and finishes).
- **Checks:** each response matches what the job promises; `scan_market_buy_signal`, `stage_order` and `check_health_audit` grew when they should have; no CRITICAL finding from the final audit (this includes the `job_pipeline` findings that name a job that did not run); timing per call. It records a `run_e2e_live` `job_run` row (shown under "Recent runs" on the page).
- **Oracle (opt-in):** scans again over a scratch market database and fails if the signal count differs from the deployed scan's. Off by default: it downloads market data.
- **Changed from the script:** the persistence check (row counts saved to `data/e2e_last_counts.json`) is gone, since GAE's disk is ephemeral and a count stored in the same database cannot detect that database being lost; the `job_pipeline` findings and per-step row counts cover it. There is no separate health or dashboard-login step (the request being authenticated proves it), and the oracle runs in-process instead of `go run`.

## Running locally as GAE (`deploy_live_local/servelocal.sh`)

`deploy_live_local/servelocal.sh [--port N] [--no-reload] [--fresh] [--dry-run]` runs `orchestrator serve` the way App Engine does, with the **live** configuration, **live** token and the **live** database, plus `air` hot reload. Only the HTTP endpoint is local (`http://localhost:PORT`). It needs `gcloud auth application-default login`. `--help` prints the full description.

- **Configuration:** the app reads the live config object `gs://<bucket>/config/env` itself at startup (`config.Bootstrap`), exactly as on GAE; edit it with `deploy_gae/config.sh`. The script adds only the `GAE_*`, `GOOGLE_CLOUD_PROJECT` and `PORT` variables the runtime injects (so `runtimeenv.Detect()` reports GAE). The process starts with a scrubbed environment (`env -i`), because a variable already set beats the config object. There is no `.env`, no Secret Manager and no `env_variables` in the app.yaml files (`TestAppYAMLHasNoEnv` fails if one appears).
- **Token:** the live `gs://<bucket>/schwab-token/shared.json` is copied to `SCHWAB_TOKEN_PATH` when missing; then the app's own `tokensync` pulls newer and pushes refreshes, as on GAE. A refresh here updates the token GAE uses.
- **Database:** the live Turso database named in the config object. There is no replica to restore and nothing to push back: every read and write goes to the live database, from here and from GAE alike. Only the working folder under `APP_FOLDER` (bundled models, market file, token) is local; `--fresh` deletes it and the app places the bundled models again.
- **Build and reload:** `deploy_live_local/build.sh` runs `go mod vendor` when `vendor/` is missing or `go.mod`/`go.sum` changed, then `GOFLAGS=-mod=vendor go build ./cmd/orchestrator` (GAE's build). `air` (config `deploy_live_local/air.toml`) rebuilds and restarts on changes in this repo. (The scripts still contain the old watcher for the two sibling repos; with one module it has nothing to watch. The launchers have not been re-run since the 2026-10-27 merge.)
- **Not the same:** no nginx or HTTPS in front; no scheduler (trigger jobs with `ORCHESTRATOR_URL=http://localhost:8082 bin/orchestrator cron <job>`); the Schwab OAuth callback stays `https://mavgo.com/schwabcallback`, so re-authorising lands on the deployed app; your gcloud credentials, not the GAE service account, authorise GCS.
- **Live means live:** real Schwab credentials; `/cron/submit_orders` here places real orders.
- The report page has its own launcher, `deploy_report_local/servelocal.sh` (port 8081, its own data folder, read only). See "Report service" below.

## Strategy allowlist (`STRATEGY_ALLOWLIST`)

`STRATEGY_ALLOWLIST` (comma-separated strategy IDs; `a+b+c` is one stack) is a line in the config object `gs://<bucket>/config/env`, like every other setting. Change it at the dashboard's `/allowlist` page (edit the list as text, or tick strategies from every one this build can resolve; each change can be previewed first, and saving keeps the old object at `config/env.prev`), with `orchestrator strategy_allowlist set|add|remove` (the same `strategy_allowlist.Update`), or with `deploy_gae/config.sh set STRATEGY_ALLOWLIST=...` (or `edit`) and restart/redeploy so a fresh instance reads it. It is not in `app.yaml` and not a secret. `strategies.ResolveGreenlit` rejects an entry that is not a registered strategy.

`orchestrator deploy_strategy <id> [--dry-run] [--no-restart]` puts one strategy row live in one command. It checks that the id is a row in `refdata/strategies.db`, reads the live allowlist, and runs, stopping at the first failure: `scripts/export_strategies.sh` (the bundle holds the live list, the new row and every row it already held), `go mod vendor`, a build of `bin/orchestrator`, `orchestrator ready --static` on the new bundle with the new id in the list (run with a fresh `APP_FOLDER` under `data/deploy_check`, emptied first, so a cached older bundle cannot hide the row), `deploy_gae/deploy.sh` (the bundle goes live while the live list is unchanged, so no instance sees an id it cannot resolve), `strategy_allowlist add <id>`, and a final `FORCE_DEPLOY=1 deploy_gae/deploy.sh` so the running service reads the new list. It is live by default and replaces the running version: `--dry-run` checks and prints the steps and runs none, and `--no-restart` stops before the last deploy. Every step can be run again, so after a failure run the same command again. An id already in the allowlist only refreshes its row in the bundle. It needs this checkout, the sibling `backtestgosqlite` checkout, gcloud and the project credentials, so it runs from a developer machine, not on App Engine.

To change it from the CLI: `orchestrator strategy_allowlist` (or `show`) prints the stored list; `set "a+b,c"`, `add ID...` and `remove ID...` change it. It reads and writes the config object itself, not the process environment, checks that every entry (stack members included) resolves before writing, and saves the old object to `config/env.prev`. Changes are live; `--dry-run` shows the resulting list and writes nothing. A running instance keeps the list it started with, so redeploy after a change.

## Resting exits on positions (`account_state_position.profit_taker`, `stop_loss`, `exit_date`)

Every row of `account_state_position` carries the take-profit and stop **Schwab is actually holding** for that position. They are read from Schwab's own order data during `account_state`, never calculated from an entry price, a strategy or a percentage.

- **`profit_taker`** is the price of a working SELL `LIMIT` on the symbol; **`stop_loss`** is the trigger price of a working SELL `STOP` (or `STOP_LIMIT`). Both are read from the order legs, including the OCO children a filled bracket carries. `NULL` (shown as `none`) means Schwab shows no such order, so nothing is invented.
- **Which orders count:** only live ones (`WORKING`, `QUEUED`, `PENDING_ACTIVATION`, `ACCEPTED`, and the `AWAITING_*_CONDITION` states). Cancelled and filled history, buys, `SELL_SHORT`, and the exits of an entry that has not filled yet (`AWAITING_PARENT_ORDER`) are ignored. With several exits on one symbol the nearest wins: the lowest limit price and the highest stop price.
- **Refreshed every sync:** each account's exits are cleared and re-read, so an exit cancelled since the last sync disappears rather than lingering. Other accounts are not touched.
- **`exit_date` is the one calculated column.** Unlike the two prices it is not read from Schwab (a broker knows nothing about a strategy's hold window): it is the exit date the pipeline stores on the managed position, the entry fill date plus the strategy's hold window (see "Exit dates" below), written onto the position row by `account_state`. A position the pipeline does not track (no `OPEN` `manage_exit` row) shows `none`. With two `OPEN` trades on one symbol the earlier date wins, and a trade from before exit dates existed gets the date derived from its entry time. It clears when the trade closes.
- **Existing databases upgrade in place** (the columns are added to a table created before they existed).
- **The `/account_state` page's Positions table has the two columns.** It used to be streamed as soon as positions arrived, before their orders were known, so it could not show exits. It is now streamed after the orders are parsed (and before the transactions), with all its old columns plus `profit_taker`, `stop_loss` and `exit_date`. Verified against the live accounts: MARA showed the 14.02 take-profit and 12.28 stop resting at Schwab, and each TSLL position showed its own.

## Exit dates (`manage_exit.exit_date`)

Every managed position has a stored **exit date**: `max_trading_days` trading days after the **New York date of the entry order's fill**. Before this, nothing stored a date; `manage_exits` recomputed "days held" every run from `entry_time`, which was whenever a job happened to notice the fill.

- **`submit_orders` generates it at submission.** When the broker accepts an entry that is not yet confirmed filled, `manage_exits.RegisterPending` writes a `PENDING_FILL` row with a **provisional** exit date (`exit_date_source = provisional`), computed from the session the order is expected to fill on (that day if submitted before the 16:00 ET close on a session day, else the next session). `PENDING_FILL` is not `OPEN`, so nothing treats it as a held position.
- **It is corrected from the real fill.** When the fill is confirmed (`submit_orders` reconcile, or `manage_exits` finding the entry order FILLED in the broker's list by its **entry order id**), the same row becomes `OPEN`, `entry_time` and `entry_filled_at` are set to the broker's fill time, and the exit date is recomputed (`exit_date_source = fill`). A fill the broker gave no time for stays `provisional`. A gap-up that fills on Monday instead of Friday moves the exit date accordingly.
- **`manage_exits` exits by that date:** a position is due when today (New York) is on or after its exit date. It also corrects any `OPEN` row whose date is still an estimate, and gives a row from before this feature a provisional date. An entry that ended `CANCELED`/`REJECTED`/`EXPIRED` becomes `CANCELLED`.
- **`check_health` judges by it:** `stale_time_expired_trade` (CRITICAL) is an `OPEN` row past its exit date; `exit_date_not_from_fill` (WARNING) is an `OPEN` row still on an estimate a day after entry; `stuck_pending_fill` (WARNING) is an accepted entry never confirmed after 3 days.
- **Existing databases upgrade in place.** `EnsureSchema` is `CREATE TABLE IF NOT EXISTS`, which never adds a column to a table that exists, so the new columns (`exit_date`, `exit_date_source`, `entry_filled_at`) are added by `pkg/schwaber/dbutil.AddColumn`, which only ever adds (it lives in schwaber because `accountstore`, which owns the position table, needs it too). `active_trades` shows the exit date and its source.

## `/manage_exit`: the managed-trades table

`/manage_exit` (same Basic Auth as the dashboard, mounted next to `/check_health`) prints the `manage_exit` table: every column, newest first, split into **Open positions**, **Entries waiting to fill** (`PENDING_FILL`, provisional exit date) and **Closed and cancelled**. `exit_date` and `exit_date_source` follow `status`. The reading order also puts `strategy_id`, `as_of_date`, `entry_fill_price`, `exit_fill_price`, `filled_qty`, `fees`, and `exit_reason_code` up front. It is read-only (it never calls Schwab and never writes), escapes every cell, and shows a column added to the table later without a code change. The page is named for the table (`manage_exit`), not the job (`manage_exits`).

## `/killswitch`: turn cron off and kill running jobs

`/killswitch` (same Basic Auth as the dashboard; GET shows the state, a POST with the page's one-time token flips it) or `orchestrator killswitch on|off|status [reason]`.

- **Engaged:** the flag is stored in the database (so it survives restarts, being stored in Turso), every `/cron/*` route answers **503** naming the switch (the scheduler and manual triggers alike; nothing starts, no `job_run` row is written), `/run_e2e_live` is refused, and every job running **right now** has its context cancelled, so an in-flight broker call is abandoned. A placement cut off mid-request is left for `submit_orders` to reconcile, never blindly re-placed.
- **It does not sell.** Positions and the broker-side take-profit and stop orders are untouched; `/exit_market` is the tool that sells. `/exit_market` and `/killswitch` keep working while it is engaged.
- **`check_health` and `/check_health`** report `killswitch_engaged` (WARNING) instead of every job as missed, so a deliberate stop is not mistaken for a failure.
- The scheduler itself keeps calling; the routes refuse. The CLI form only sets the flag on the database that process opens; cancelling running jobs happens inside the server.

## `/clear_stale`: stop reporting on strategies you no longer run

`/clear_stale` (same Basic Auth) or `orchestrator clear_stale [--dry-run]`. A strategy that is not greenlit (not on `STRATEGY_ALLOWLIST`, directly or as a member of a greenlit stack) can no longer stage, submit or manage anything, so its leftovers only add warnings to `check_health`. This retires them, for every account, in the **local database only: the broker is never contacted and nothing is deleted.** It is live unless given `--dry-run`, and it refuses to run with an empty allowlist.

- **REJECTED and PENDING staged orders** of a strategy that is not greenlit become `CLEARED`, however recent; their `reject_reason` is kept with `[cleared as stale]` appended.
- **OPEN trades of a strategy that is not greenlit and that the broker mirror no longer holds** (`orphan_active_trade`) become `CLOSED_MANUAL` with reason `STALE_CLEARED`, but only when `account_state` was refreshed *after* the entry filled. A fill the mirror has not seen yet is kept and listed as `kept_trade`; run `account_state` and clear again.
- **Never touched:** anything belonging to a greenlit strategy (its rejected orders and orphans still warn, which is the point), SUBMITTED orders, and any position the broker still holds, greenlit or not. A held position of a retired strategy keeps warning until it exits.
- **The dashboard (`/`, via `/api/tables`) shows what is current:** cleared orders and trades, rows of strategies that are not greenlit, and all but the latest `check_health` run are hidden. `/api/tables?all=1` returns every row, history included. `check_health` itself only changes when it next runs (09:00 ET) or you run it, because it needs the broker.
- `GET /clear_stale` previews (HTML, or JSON with `?format=json`) and writes nothing. A POST needs the page's single-use token and `mode=dry|live`.
- It is not a cron job (no `/cron/clear_stale`, not in `cron.yaml`; a test enforces this). A live run is recorded as a `clear_stale` `job_run`.

## `/exit_market`: liquidate every managed account

`/exit_market` (same Basic Auth) or `orchestrator exit_market [--dry-run] [--killswitch]`. For every account in `SCHWAB_ACCOUNT_NUMBERS`: **cancel every working order** (a bracket's take-profit and stop legs reserve the shares, and a working buy would fill after the sell), verify with the broker that none is left, then **sell every long stock/ETF position at market** in whole shares. Shorts, options, mutual funds, cash equivalents and fractional remainders are listed and skipped, never guessed at.

- **It is not a cron job.** It is not in `cronJobs` or `cron.yaml` and has no `/cron/exit_market` route (a test enforces this for it, `killswitch` and `run_e2e_live`). It only runs when an operator submits the page's form or runs the CLI command, which is live unless it is given `--dry-run`.
- **Preview first.** The page has a **Preview** button (dry run: prints what would be cancelled and sold, changes nothing) and a **Sell everything at market** button; the CLI is live unless you pass `--dry-run`. Nothing runs when the page loads.
- **Bookkeeping so the pipeline does not undo it:** the sold symbols' `OPEN` `manage_exit` rows become `CLOSED_MANUAL` (`EXIT_MARKET`), pending entries are cancelled, and staged orders still waiting for `submit_orders` are rejected. The option **Then engage the killswitch** (checked by default; `--killswitch`) also stops the next cron from staging and buying again.
- One implementation of "sell at market": `manage_exits.ExitAtMarket`, used by `manage_exits` too. It now cancels **every** working sell on the symbol (the old code cancelled only the take-profit leg and left the stop leg holding the shares), checks with the broker that none remain, then sells and confirms the broker accepted it.
- Live runs are logged, recorded as an `exit_market` `job_run`, and emailed in the standard report.

## Readiness, the ops board and the weekly digest

- **`ready`** (`pkg/orchestrator/ready`, `/cron/ready`, `orchestrator ready`, `/ready`) runs at 09:00 and 17:30 ET every day and **emails only when something is not ok**, so a message from it always means look. `orchestrator ready --static` checks only that every entry of the live `STRATEGY_ALLOWLIST` resolves from the bundle in this source tree; `deploy_gae/deploy.sh` runs it before it uploads and refuses to deploy a bundle that cannot trade the live allowlist (`SKIP_READY_CHECK=1` skips it). After the deploy, `deploy.sh` reads `/ready?format=json` and prints every check that is not ok; it exits 1 only if the new version cannot resolve the live allowlist; other problems (a failed scan from yesterday, an expiring token) are printed and left for you.
- **`weekly_digest`** (`pkg/orchestrator/weekly_digest`, `/cron/weekly_digest`, `orchestrator weekly_digest [--dry-run]`) emails a Saturday 09:00 ET summary: signals by strategy and day, orders staged and submitted, runs and failures per job, open positions with unrealized P&L, and the readiness status. It has no live-versus-backtest P&L: the database keeps one current account snapshot, not an equity history.
- Every operator page gets a navigation bar and a footer with the deployed version, service and strategy-bundle hash (`withChrome`), so you can tell which deploy you are looking at.
- Both jobs are in `job_runs.Schedule` (after `check_health`; nothing depends on them), so `/ops` and `check_health` notice a missed run. `trigger_limits` is not, on purpose: it records a run only when it acts (`job_runs.NotRecordedEachRun`).

## Which job did not do its job

Every cron hit leaves a `job_run` row (`OK`, `EMPTY` or `FAILED`), including failures, with the handler's real error text as the summary. A job that records its own richer row (`scan_market`, `stage_orders`, `submit_orders`) is not recorded twice. A run that dies before its handler starts, such as a failure to open the database, is recorded as `FAILED` too (`pkg/orchestrator/appserve/runlog.go`).

`check_health` reads that history first, before anything that needs the broker, so it still reports when the token or the account list is what is broken. For each scheduled job it compares the latest scheduled slot (`job_runs.Schedule`, which a test keeps identical to `deploy_gae/cron.yaml`) with what was recorded and writes `check_health_finding` rows in category `job_pipeline`, account `all`, severity `CRITICAL`:

| check | meaning |
|---|---|
| `job_never_ran` | no `job_run` row at all: cron never reached it, the database was recreated, or it died before recording |
| `job_missed_slot` | its last recorded run is older than the latest slot (shows that run and its outcome) |
| `job_failed` | it ran for the latest slot and failed (shows the error) |
| `pipeline_root_cause` | the first broken job in pipeline order (`account_state`, `scan_market`, `stage_orders`, `submit_orders`, `manage_exits`) and the jobs after it that depend on it |
| `pipeline_diagnosis_error` | the diagnosis itself could not run; never reported as all-clear |

**`/check_health` page.** The dashboard serves the same verdicts in the browser at `/check_health` (Basic Auth with `DASHBOARD_USERNAME` / `DASHBOARD_PASSWORD`, like every other dashboard route; on GAE it is at `https://<app>.appspot.com/check_health`). It lists each scheduled job with its latest slot, verdict, last recorded run and summary, then the latest audit and the findings of the latest run per account. It only reads stored rows, so reloading it never calls Schwab and never writes; a fresh health run is `orchestrator cron check_health`.

A job gets 15 minutes after its slot before it counts as missed. `check_health` is not checked by itself. Every table exists from the moment the database is opened (`pkg/orchestrator/schemas`), so a job that has not run shows 0 rows in the dashboard rather than "no such table".

## Email Reports

Every job that can act on live money (`scan_market`, `submit_orders`, `manage_exits`, `check_health`) sends **exactly one** report email per invocation — never a scattered stream of per-event alerts. All four render through the same `email.BuildJobReport(jobName, rows)` (`pkg/orchestrator/send_email/report.go`), so every operator email has the same predictable shape:

- **Subject**: the job name, verbatim — `scan_market`, `submit_orders`, `manage_exits`, or `check_health`. No emoji, no editorializing; the subject tells you which job, the body tells you what happened.
- **Body**: one HTML table, one row per strategy/signal/outcome —

  | Account | Strategy | Symbol | Signal | Status | Detail |
  |---|---|---|---|---|---|

  The row set covers *everything evaluated that run*, not only the noteworthy parts — a `NO_BUY` strategy or a `HOLD` position gets its own row alongside any `STAGED`/`SUBMITTED`/`REJECTED`/`CLOSED_PROFIT` outcome. Silence in an inbox is never load-bearing: a "quiet" run still produces an email, so a crashed job (no email at all) is distinguishable from a genuinely uneventful one (an email full of `NO_BUY`/`HOLD`/`HEALTHY` rows). Rows are color-coded by `Status` (green success, amber skip/warning, red error/critical) for fast visual triage.
- **`submit_orders` exception**: it renders through `BuildJobReportSummary` and its subject carries the outcome after the job name (`submit_orders -- no staged orders`, `submit_orders -- 3 found, 1 filled, 1 pending fill, 1 rejected`, `submit_orders -- FAILED (1 error(s)), ...`), so it still starts with the bare job name for inbox filters. The body opens with the run time (ET and UTC), the totals (found / filled / accepted-but-unconfirmed / rejected / unresolved), an explicit "nothing was pending" line for an empty run, and an `ACTION REQUIRED` line for every failed account or filled position that lost its `manage_exit` row.
- `submit_orders` logs `[submit_orders] start ...`, one line per account and detail, and a final `[submit_orders] done status=... job_run=... email=...` line, from both the CLI and `/cron/submit_orders` (`pkg/orchestrator/submit_orders.RunJob`). Every live run also writes a `job_run` row (`OK` / `EMPTY` / `FAILED`), and a partial failure returns HTTP 500 after the report email has gone out (no second failure email).
- `scan_market`/`submit_orders`/`manage_exits` only send on live runs (the default; never `--dry-run`, to avoid noise while testing). `check_health` has no dry-run mode and always sends, including on a fully healthy account (one `HEALTHY` row) — so its silence is never mistaken for "didn't run."
- Uses `SMTP_HOST`/`SMTP_PORT`/`SMTP_USER`/`SMTP_PASS`/`EMAIL_RECIPIENT` in the config object. **These are typically real credentials** — `pkg/orchestrator/send_email/send_email_test.go`'s live-send test is opt-in only (`EMAIL_LIVE_TEST=1`) specifically so `go test ./...` never has the side effect of emailing the configured inbox.

## Commands (`cmd/`)

There is one binary, `cmd/orchestrator`. It is a thin dispatcher: every subcommand parses flags and calls the same `pkg/` function that the matching `/cron/<job>` route calls, so a job behaves the same from the CLI, a local server and App Engine. With no arguments it serves HTTP when it detects App Engine, and prints usage otherwise.

```bash
go run ./cmd/orchestrator <command> [flags]
```

Job flags: `--dry-run` previews and writes nothing. Jobs are live by default (Rule 6 in `../docs/CLAUDE.md`); `--live` is still accepted and `--live=false` also previews. `run_e2e_live` and `probe_seat` have no dry run.

**The scheduled jobs**, in pipeline order (each also exists as `GET /cron/<job>`):

| Command | Package | What it does | Key flags |
|---|---|---|---|
| `account_state` | `pkg/orchestrator/account_state` | Mirrors the configured Schwab accounts, positions, orders and transactions into the local DB. | `--account`, `--accounts` |
| `scan_market` | `pkg/orchestrator/scan_market` | Runs the allowlisted strategies over the market DB and finds buy signals. The cron route only records them in `scan_market_buy_signal`. The CLI still stages orders inline as well (live by default; `--dry-run` previews); use `stage_orders` for staging. | `-strategy`, `-strategies`, `-symbols`, `--dry-run` |
| `stage_orders` | `pkg/orchestrator/stage_orders` | Sizes the latest scan's buy signals against live cash and equity and writes `stage_order` rows. | `--dry-run`, `--account`, `--accounts`, `-profit`, `-max-days` |
| `submit_orders` | `pkg/orchestrator/submit_orders` | Re-validates staged orders, places the bracket orders, and confirms which entries filled (recording each position at its real fill time). `--dry-run` builds each order, runs the safety check and sends it to Schwab's `previewOrder` (nothing is placed); a row shows `PREVIEW_REJECTED` with Schwab's reason when the preview refuses it. A take-profit of 0 (hold-day strategies such as `hmm-*`) places the entry with a stop only. | `--account`, `--accounts`, `--dry-run` |
| `manage_exits` | `pkg/orchestrator/manage_exits` | Checks open trades against their holding window and profit target and sells when due. | `--account`, `--accounts`, `--dry-run` |
| `check_health` | `pkg/orchestrator/check_health` | Audits protective orders, scheduled exits, the strategy contract, and every enabled `check_health` row. | `--account`, `--accounts` |

The validation commands (`validate walk`, `validate verdict`, `live_vs_backtest_trades`, `live_vs_backtest_slippage`, `live_vs_backtest_report`) are documented in [Validation](#validation). They have no cron route.

**Inspecting and controlling**

| Command | What it does |
|---|---|
| `run_all_jobs` | Runs check_health, manage_exits, scan_market and submit_orders in sequence from the CLI. |
| `active_trades` | Prints the open, strategy-tracked trades. |
| `staged_orders` | Prints staged orders at any status (pending, submitted, rejected). |
| `health_checks` | Lists, enables or disables `check_health` rows, or shows recent findings (`--findings`). |
| `strategies` | Lists the strategies registered in `pkg/backtest` and which are on `STRATEGY_ALLOWLIST`. |
| `runtime` | Prints the detected runtime (local, gae, ...). |

Every subcommand, web page and cron route is cross-referenced in [`docs/OrchestratorAPI.md`](OrchestratorAPI.md).

**Auth and the deployed service**

| Command | What it does |
|---|---|
| `auth` | One-time Schwab OAuth setup (schwaber's `oauthcli`), logged to the `auth` table. |
| `token pull\|push` | Syncs the local Schwab token with the shared copy in GCS. |
| `cron <job>\|list` | Triggers a job on the **deployed** service by hand with your Google ID token (see "Triggering a job on the deployed service by hand"). `--dry-run` previews `stage_orders`. |

**Servers**

| Command | What it does |
|---|---|
| `dashboard` | The local web UI only (no `/cron/*`). |
| `serve` | The full HTTP server, as on App Engine: dashboard, `/cron/*`, Schwab OAuth, warmup and health. |

## Validation

These commands compare a live strategy with its backtest. Each one is a `pkg/` function with a thin `cmd/` wrapper. Run them from the CLI. They have no cron route. Walk-forward and the curve-fit check live in `backtestgosqlite`. Trade reconciliation, the slippage audit, and the HTML report are orchestrator subcommands.

| Command | Package | What it does | Key flags |
|---|---|---|---|
| `validate walk` | `pkg/backtest/walk_forward` | Rolling in-sample and out-of-sample windows. Writes `walk_forward_fold` and `walk_forward_summary`. | `-strategy` (required), `-market-db`, `-table`, `-db`, `-train-months`, `-test-months`, `-step-months`, `-capital` |
| `validate verdict` | `pkg/backtest/check_overfit` | Reads that summary and writes `check_overfit`. Does not re-simulate. | `-db`, `-min-oos-trades`, `-trial-cutoff`, `-decay` |
| `live_vs_backtest_trades` | `pkg/orchestrator/live_vs_backtest_trades` | One row per decision in `live_vs_backtest_trade`: the live fill against a one-share replay of that signal. | `--account`, `--market-db`, `--table` (default `backtest_start`) |
| `live_vs_backtest_slippage` | `pkg/orchestrator/live_vs_backtest_slippage` | Averages entry and exit slippage, the worst entry slippage, slippage versus the limit, fill delay, and fees into `live_vs_backtest_slippage`. Run `live_vs_backtest_trades` first. | none |
| `live_vs_backtest_report` | `pkg/orchestrator/live_vs_backtest_report` | Runs `live_vs_backtest_trades` and `live_vs_backtest_slippage`, then writes `live_vs_backtest_report.db` and an HTML page. The report file is separate from `orchestrator.db`. | `--account`, `--market-db`, `--table` (default `backtest_start`), `--db`, `--html` |
| `live_vs_backtest_winrate` | `pkg/orchestrator/live_vs_backtest_winrate` | Backtest in-sample, backtest out-of-sample and live win rate for every strategy, side by side, with the live win rate after each closed trade charted against the in-sample and out-of-sample lines, the live 95% Wilson interval and a verdict (BELOW, IN_LINE, ABOVE; TOO_EARLY under 10 closed live trades; NO_BACKTEST). Also shows the backtest replaying the same trades that traded live, so a live shortfall reads as fills or as edge. Live is counted in decisions: a strategy buying one symbol on one day is one decision however many accounts it filled in, and wins when the `manage_exit` fills' `pnl` adds up above zero (so a stop loss in three accounts is 0/1); the replay comes from `live_vs_backtest_trade`. Backtest rates come from `live_vs_backtest_winrate`; any strategy not stored there falls back to `pkg/orchestrator/live_vs_backtest_winrate/baseline.json`, compiled into the binary so a fresh deploy shows them with no refresh. Regenerate it with `orchestrator live_vs_backtest_winrate -export-baseline pkg/orchestrator/live_vs_backtest_winrate/baseline.json` after `live_vs_backtest_sync`, then rebuild. `live_vs_backtest_winrate` which a refresh fills from `-reports-dir` (each run folder's in-sample pass and its `oos/` held-out pass, newest run wins), `-ref-db` (whole-window `strategy_last_backtest`) or `-evals-db` (`strategy_evals`), limited to `STRATEGY_ALLOWLIST`. Page: `/live_vs_backtest_winrate` (HTML, `?format=json` for JSON, `?days=N` sets how far back the closed-trades list goes, default 3), listing the greenlit strategies and any other that has traded live. Only a run that writes updates the database. | `-export-baseline <file>` (reads only), `-reports-dir <dir>`, `-ref-db <file>`, `-evals-db <file>` (each writes `live_vs_backtest_winrate`), `--dry-run` (writes nothing) |
| `live_vs_backtest_sync` | `pkg/orchestrator/live_vs_backtest_sync` | For every `STRATEGY_ALLOWLIST` strategy, finds the newest backtest run that has both an in-sample pass (`<run>/*.db`) and a held-out pass (`<run>/oos/*.db`), copies those result databases into an archive folder that mirrors the reports layout (a `VACUUM INTO` snapshot, skipped when the source size and modification time are unchanged), records the run, window, trades and win rate in `live_vs_backtest_deployed`, then runs the `live_vs_backtest_winrate` refresh from the archive. Says when no single run has both passes (`split_runs`) and which strategies have no result. | `-reports-dir` (default env `BACKTEST_REPORTS`), `-dest` (default `live_vs_backtest_deployeds` beside `orchestrator.db`), `--dry-run` (copies and writes nothing) |
| `orders` | `pkg/orchestrator/orders` | Every order Schwab lists for the `SCHWAB_ACCOUNT_NUMBERS` accounts, bracket children unfolded (each under its parent), with the price it really executed at (`FILL`, from its execution legs) beside the `LIMIT` and `STOP` that were sent. Read-only: places and cancels nothing, writes no database, so no `--dry-run`. Page: `/orders` (HTML, `?format=json`; `?symbol=`, `?status=`, `?days=` 1 to 58, default 7). | `-symbol`, `-status`, `-days` |

### Walk-forward

From the repo root. Rolls an in-sample window forward and simulates each out-of-sample window from a flat book. Signals are generated once on the full history. Only that window's dates are simulated. Returns in the tables are fractions (`0.05` is five percent).

```bash
go run ./cmd/validate walk -strategy tsll-daily-one-share -market-db data/market.db -table backtest_start -db reports/walk_forward.db -train-months 24 -test-months 6 -step-months 6 -capital 100000
```

| Flag | Default | Meaning |
|---|---|---|
| `-strategy` | required | Strategy id, or a comma-separated list |
| `-market-db` | `MARKET_DB` | Bars SQLite |
| `-table` | `backtest_start` | Bars table |
| `-db` | `reports/walk_forward.db` | Where fold rows and the summary are written |
| `-train-months` | 24 | In-sample window, calendar months immediately before each out-of-sample window |
| `-test-months` | 6 | Out-of-sample window |
| `-step-months` | 6 | How far the next fold moves forward |
| `-capital` | 100000 | Starting capital for each window |

### Curve-fit check

Reads `walk_forward_summary` and writes `check_overfit`. It does not re-simulate. Run `validate walk` first. Verdicts are `HOLDS`, `DECAYS`, `CURVE_FIT`, and `INSUFFICIENT`.

```bash
go run ./cmd/validate verdict -db reports/walk_forward.db -min-oos-trades 8 -trial-cutoff 20 -decay 0.25
```

| Flag | Default | Meaning |
|---|---|---|
| `-db` | `reports/walk_forward.db` | The database `walk_forward` wrote |
| `-min-oos-trades` | 8 | Fewer out-of-sample trades than this is `INSUFFICIENT` |
| `-trial-cutoff` | 20 | A parameter grid at least this large can be `CURVE_FIT` |
| `-decay` | 0.25 | Out-of-sample Sharpe below this fraction of in-sample Sharpe has collapsed |

`INSUFFICIENT` means too few out-of-sample trades. `CURVE_FIT` means many trials, an in-sample Sharpe above 0.5, and an out-of-sample Sharpe below the decay fraction of in-sample. `DECAYS` means the out-of-sample Sharpe collapsed. `HOLDS` means the out-of-sample Sharpe stays positive and at least half of in-sample.

### Trade reconciliation

`live_vs_backtest_trades` rebuilds `live_vs_backtest_trade`: one row per account, strategy, symbol, and signal date. The live fill is compared with a one-share replay of that same signal through the backtest's entry and exit rules.

```bash
go run ./cmd/orchestrator live_vs_backtest_trades --account 123 --market-db /path/market.db --table backtest_start
```

`--market-db` defaults to `MARKET_DB`. If that file is missing, the live side is still written and `model_outcome` is `NO_BARS`. `--account` limits staged orders and managed trades to one account. Signals that were never staged are still included.

### Slippage and latency

`live_vs_backtest_slippage` summarizes that ledger into `live_vs_backtest_slippage`: average entry and exit slippage, the worst entry slippage, slippage versus the limit sent, average sessions from the intended session to the fill, and fees. Run `live_vs_backtest_trades` first.

```bash
go run ./cmd/orchestrator live_vs_backtest_slippage
```

### Live versus backtest report

`live_vs_backtest_report` is the command that gathers the inputs and writes the report. The CLI wraps it in the same database sync as `live_vs_backtest_trades`, so a normal run restores `orchestrator.db` and the market file from GCS before it reads them. It then runs `live_vs_backtest_trades` and `live_vs_backtest_slippage` and writes a separate SQLite file plus an HTML page.

The report's tables are in Turso: `live_vs_backtest_report_meta`, `_note` and `_round_trip`, each named for the build (`…__r20261026t…`, newest 3 kept). The decisions and slippage it shows are read from `live_vs_backtest_trade` and `live_vs_backtest_slippage`, so nothing is copied from the account mirror or the market file. The HTML file (`--html`, default `live_vs_backtest_report.html` in the OS temp folder) is rendered from those tables.

Cash on each closed round trip is the sum of the Schwab `TRADE` net amounts on the buy order and the sell order. Those nets already include fees. A nested bracket sell is paired with its entry. A separate sell is paired with the oldest unmatched buy of the same account and symbol. A strategy on `STRATEGY_ALLOWLIST`, including a member of a greenlit stack, is marked greenlit and included in the greenlit cash total.

It reads the broker mirror `account_state` already stored. When the order mirror is empty, the page says to run `account_state` first. When `live_vs_backtest_trade` is empty, `live_vs_backtest_slippage` is cleared and the page says the summary was not run.

The last lines of the command are the outcome. `live_vs_backtest_report OK` means the report files were written and there were no warnings. `live_vs_backtest_report OK, with warnings` means the files were written and lists each warning under that line. `live_vs_backtest_report FAILED` is a non-zero exit. A failed database restore stops there, before the report is written.

### Report service

The same page is the App Engine service `live-vs-backtest-report`, deployed on its own with `deploy_gae/deploy_live_vs_backtest_report.sh`. It is not the default trading service and it has no cron. Open:

```text
https://live-vs-backtest-report-dot-<project>.uc.r.appspot.com/
```

Sign in with the same `DASHBOARD_USERNAME` and `DASHBOARD_PASSWORD` as the trading dashboard. The page rebuilds from the live Turso database and then serves that copy for two minutes. `?refresh=1` rebuilds immediately.

On startup the process opens the Turso database and the market file. The page heading is `live_vs_backtest_report OK` or `live_vs_backtest_report OK, with warnings`. The bars it replays are the `market_bars` kept in Turso and the market file rebuilt from them: daily bars for the symbols the scanner already keeps (MARA, PDD, TECL, TSLL, VOO, about two years, a few hundred kilobytes). The research market database under `data/` is a different file and is not part of this deploy. Strategy code is compiled into the service binary.

### Report service locally

`deploy_report_local/servelocal.sh [--port N] [--folder DIR] [--no-reload] [--fresh] [--dry-run]` runs that same service on this machine. The default port is 8081, so `deploy_live_local/servelocal.sh` (default 8082) can run beside it. `--help` prints the full description. It needs `gcloud auth application-default login`.

The binary starts with no arguments and `GAE_SERVICE=live-vs-backtest-report` (the `service:` line in `deploy_gae/live_vs_backtest_report.yaml`). That is how App Engine starts it (`entrypoint: main`). `main` then calls `live_vs_backtest_report.Serve`. Hot reload uses `deploy_live_local/build.sh` and `air` (`deploy_report_local/air.toml`).

The process reads the live config object. It sets `APP_FOLDER` to `/tmp/trade_orchestrator_report` (or `--folder` / `REPORT_FOLDER`), which wins over the config object's folder. The operational database is the live Turso database; the folder holds only the market file and bundled models. `--fresh` deletes the report folder first. The folder has to be its own directory under `/tmp`, and the script refuses one that overlaps the config `APP_FOLDER` (the trading copy). The process only reads the database.

Sign in with the dashboard basic auth from the live config. `?refresh=1` rebuilds the page. `--dry-run` prints the plan and the config variable names.

```bash
go run ./cmd/orchestrator live_vs_backtest_report --market-db /path/market.db --table backtest_start --db /path/live_vs_backtest_report.db --html /path/live_vs_backtest_report.html
```

| Flag | Default | Meaning |
|---|---|---|
| `--account` | every account | Limits the ledger rows this run replaces, and the cash table, to one account |
| `--market-db` | `MARKET_DB` | Bars SQLite for the replay. A missing file still writes the live side |
| `--table` | `backtest_start` | Bars table inside the market database |
| `--db` | `live_vs_backtest_report.db` in the OS temp folder | Report SQLite. Use a file other than the orchestrator database and `MARKET_DB` |
| `--html` | the report database path with a `.html` suffix | The page, rendered from the report database |

### How to read a row

- Returns are fractions. `0.05` is five percent.
- `entry_slippage_bps` is positive when the live entry paid more than the model. `exit_slippage_bps` is positive when the live exit sold higher than the model.
- `fill_delay_sessions` counts trading sessions from `next_session` to the entry fill. A negative number means the fill was before that session. A fill price of `0` means the broker price is still unknown.
- Only `mara_tree`, `pdd_tree`, and `sig-voo-buy-tecl` enter on the next session's limit. Every other strategy, including `tsll-daily-one-share`, is replayed on the signal bar, which is how that strategy's backtest fills. The row shows that difference.

### What the live book stores

`manage_exit` keeps the decision key (`strategy_id`, `as_of_date`, `next_session`, the signal ids, and the stage id) plus `entry_fill_price`, `exit_fill_price`, `filled_qty`, `fees`, and `exit_reason_code`. The Schwab order mirror stores `execution_price`. The transaction mirror stores `fees` and `symbol`. Reconciliation copies the broker fill off the order, including a sell nested under the entry, when the managed trade does not already have one. The managed-trades page shows the new columns.

## Local Web Dashboard

```bash
go run cmd/orchestrator/main.go dashboard -port 8080
```

Every request is gated by HTTP Basic Auth (`DASHBOARD_USERNAME` / `DASHBOARD_PASSWORD` in the config object); the server refuses to start if either is unset. `/` is a single-page dump of orchestrator job state only (`strategy_signals`, `staged_orders`, `active_trades`, `health_audits`, `health_check_findings`, `health_checks`) — it does not print the Schwab raw-mirror tables, which stay in SQLite. The only write action the dashboard exposes is a live Schwab re-sync (`/account_state`); scans, submits, and health checks stay CLI/scheduler jobs.

### Triggering a job on the deployed service by hand

`/cron/<job>` only runs for App Engine cron, or for a Google account you allowlist. To allow yourself, add your Google email to `CRON_ALLOWED_EMAILS` in the config object (not in git):

```
CRON_ALLOWED_EMAILS=you@example.com
```

Or run `deploy_gae/setup_cron_access.sh --deploy --verify`, which edits the config object, redeploys and checks it with a dry run. Then, from a machine where you are signed in with `gcloud auth login`:

```bash
orchestrator cron list                      # the job names
orchestrator cron scan_market               # same as the scheduled 18:00 run
orchestrator cron stage_orders --dry-run    # preview: sizes and reports, writes nothing
```

The service URL defaults to `https://<project>.uc.r.appspot.com` for the project `gcloud` is set to (`gcloud config get-value project`). Override it with `ORCHESTRATOR_URL` or `--url`. It is the same as:

```bash
curl -H "Authorization: Bearer $(gcloud auth print-identity-token)" https://<project>.uc.r.appspot.com/cron/scan_market
```

The server verifies the token's Google signature, expiry and audience, requires a verified email, and matches it exactly against `CRON_ALLOWED_EMAILS`. With that variable unset, manual triggering is off and only App Engine cron is accepted. If a token is rejected for its audience, the service log says so; set `CRON_ID_TOKEN_AUDIENCE` to the value it names.

### HTTP routes

| Method | Path | What it does | What it returns |
|---|---|---|---|
| `GET` | `/`, `/index.html` | Serves the job-state dashboard HTML. The page fetches `/api/tables` on load. The re-sync lives at `/account_state`. | `text/html` — the embedded `ui.html` SPA |
| `GET` | `/runtime` | Diagnostics: detected runtime. Dashboard Basic Auth like every route here (the server refuses to start with no credentials set). Only `/_ah/warmup` and `/_ah/health`, which show nothing, are open. | `text/plain` / `application/json` |
| `GET` | `/env` | Every environment variable the app is running with, grouped (app, market data, Schwab, email, access; platform variables such as `GAE_*`, `CNB_*` and `PATH` are hidden unless `?all=1`) (the config object is loaded into it, so `STRATEGY_ALLOWLIST` shows). Values of names containing PASS, PWD, SECRET, TOKEN, KEY, CREDENTIAL, AUTH, CLIENT_ID, USERNAME or ACCOUNT are masked. Dashboard Basic Auth. | `text/html` |
| `GET` | `/ops` | The board of every cron job: latest slot and verdict, last run, how long it took, next slot (ET), and what it said. A **Run** button per job posts to `/ops/run`, which calls the same `/cron/<job>` handler a schedule uses (so the killswitch and `job_run` still apply) behind a one-time token. **Dry run** is the CLI's `--dry-run` where a job has one. A job that can place or change real orders (`stage_orders`, `submit_orders`, `trigger_limits`, `manage_exits`) runs for real only after you type its name. | `text/html`; `?format=json` for the data |
| `GET` | `/ready` | Pre-flight checks: the allowlist resolves from the bundle compiled into this binary, the Markov models and market data are current, the Schwab refresh token has days left, funds, the killswitch and Turso (forward-looking only; past job failures are `check_health`). `?strict=1` answers `503` when the status is bad (deploy.sh uses it). | `text/html`; `?format=json` |
| `GET` | `/scan` | What the evening scans found over the last `?days=N` (default 7): buy candidates and their status, and a no-buy reason per strategy and day. | `text/html`; `?format=json` |
| `GET` | `/config` | The config object (`gs://<bucket>/config/env`) with secrets masked, `STRATEGY_ALLOWLIST` checked against the bundle, and `?try=a+b,c` to check a list before saving it (changes nothing). | `text/html`; `?format=json` |
| `GET` | `/api/tables` | Dumps the four job-lifecycle tables (see below), most-recent row first, up to 200 rows per table. Cells longer than 300 characters are truncated. Schwab mirror tables are not included. | `application/json` `{ "tables": [ TableDump, ... ] }` |
| `GET` | `/account_state` | Live Schwab sync for `SCHWAB_ACCOUNT_NUMBERS` only. Streams an HTML page: a table is flushed as soon as each account's details/positions, orders, or transactions response returns. Client init is capped at 15s; that failure is a `500` `ERROR:` body. Mid-sync errors appear as red HTML blocks without waiting for the rest. | `text/html` streamed tables; `500` if accounts are unset or the Schwab client cannot be created |
| — | anything else | — | `404` |

`TableDump` fields:

| Field | Meaning |
|---|---|
| `name` | SQLite table name |
| `label` | Human-readable heading |
| `job` | Which pipeline job writes this table |
| `columns` | Column names, in schema order |
| `rows` | Up to 200 most-recent rows as string arrays (one value per column) |
| `error` | Present only if that table could not be queried |

### Tables served by `/api/tables`

Job-lifecycle state only. The Schwab raw-mirror tables (`accounts`, `positions`, `orders`, `order_legs`, `transactions`, `user_preferences`, `raw_responses`) stay in SQLite and are refreshed by `/schwabstate`, but are not dumped here.

| Table | Written by | What the rows show |
|---|---|---|
| `strategy_signals` | `scan_market` | One row per strategy per trading day: BUY/NO_BUY, traded symbol, price, status |
| `staged_orders` | `scan_market` → `submit_orders` | Bracket orders queued for (or already sent by) submission: qty, entry/target/stop, status, reject reason |
| `active_trades` | `submit_orders` → `manage_exits` | Strategy-opened positions still being managed, plus closed outcomes (days held, P/L) |
| `health_audits` | `check_health` | One append-only row per audit: protected/unprotected/orphan counts, critical/warning finding counts, overall status |
| `health_check_findings` | `check_health` | One append-only row per violation found that run (data-driven `health_checks` queries + live-broker-data checks) |
| `health_checks` | seeded by `jobs.InitTradeTables`; `enabled` toggled by `health_checks` | The data-driven check definitions themselves: name, category, severity, SQL query, enabled |

See [Database & Table Reference](#database--table-reference) for status values and schema details.

```bash
curl -u "$DASHBOARD_USERNAME:$DASHBOARD_PASSWORD" http://localhost:8080/api/tables
curl -u "$DASHBOARD_USERNAME:$DASHBOARD_PASSWORD" http://localhost:8080/account_state
```

---

## Database & Table Reference

Everything lives in one Turso database (`TURSO_DATABASE_URL`, required in the config object), opened and schema-initialized by `bootstrap.OpenDB()` in `pkg/orchestrator/bootstrap`, which both the CLI and the HTTP surface (`pkg/orchestrator/appserve`) call. It has two schema groups: a raw mirror of what Schwab reports, and the orchestrator's own job-lifecycle state.

### Raw Schwab mirror — populated by `account_state`

| Table | Contents |
|---|---|
| `accounts` | Cash balance, buying power, liquidation value per account number |
| `positions` | Long/short quantity, average price, market value per symbol per account |
| `orders` / `order_legs` | Every order Schwab reports, parent row + per-leg detail |
| `transactions` | Settled activity (fills, dividends, fees) |
| `user_preferences` | Streamer connection metadata |
| `raw_responses` | Every raw API response body, for audit/replay |

### Job-lifecycle state — each table is created by its owning package's `EnsureSchema`

| Table | Written by | Purpose | Status values |
|---|---|---|---|
| `strategy_signals` | `scan_market` (upsert + update), `submit_orders` (update) | One row per strategy per trading day (`signal_date`) | `EVALUATED` (no buy) → `PENDING`/`SIMULATED` → `STAGED`/`ERROR_STAGE_ORDER` → `EXECUTED`/`SAFETY_REJECTED`/`FAILED_SUBMIT`/`ERROR_BUILD_ORDER` |
| `staged_orders` | `scan_market` (insert), `submit_orders` (update) | Bracket orders queued for submission, decoupled from live placement | `PENDING` → `SUBMITTED` / `REJECTED` (`CANCELLED` reserved, not yet wired to a command) |
| `active_trades` | `submit_orders` (insert), `manage_exits` (close) | Every strategy-opened position still being managed, and its outcome once closed | `OPEN` → `CLOSED_PROFIT` / `CLOSED_TIME_EXPIRATION` (`CLOSED_STOP_LOSS`, `CLOSED_MANUAL`, `CANCELLED` reserved, not yet set by any job) |
| `health_audits` | `check_health` (insert only, append-only log) | One row per audit run, plus a `critical_findings`/`warning_findings` count | `HEALTHY`, `WARNING`, `ACTION_REQUIRED` (assigned whenever any finding that run was `CRITICAL`) |
| `health_checks` | Seeded/updated by `check_health.EnsureSchema` on every open; `enabled` toggled by `health_checks --enable`/`--disable` | One row per data-driven check: a name, category, severity, and a SQL query (parameterized by `account_number`) that returns one `(subject, detail)` row per violation. Code upgrades refresh a built-in check's query/description but never its `enabled` flag — see `defaultHealthChecks` in `internal/jobs/trade_tracker.go`. Insert your own row here to add a check with no code change. | n/a |
| `health_check_findings` | `check_health` (insert only, append-only log) | Every violation from a `check_health` run — both the data-driven `health_checks` queries and the live-broker-data checks built into `RunHealthCheck` — one row per violation, per run. `health_checks --findings` reads this. | n/a |

`market_history.db` (a separate SQLite database from `pkg/backtest`, read-only from this repo's point of view) is **not** part of this state — it's historical bar data `scan_market` reads to generate signals, and is not written by anything in this repository.

---

## Packages (`pkg/`)

Each job is one package, and the job has one name everywhere: the package directory, the CLI subcommand, the `/cron/<job>` route, the `cron.yaml` entry, the job-run record and the log prefix. The other packages are libraries.

**Jobs**

| Package | Purpose | Tables |
|---|---|---|
| `account_state` | Pulls accounts, positions, orders and transactions from Schwab and mirrors them (shared code is schwaber's `accountstore`). Also serves the streamed `/account_state` page and the ingestion summary. | `account_state_raw_response`, `_account`, `_position`, `_order`, `_order_leg`, `_transaction`, `_preference` |
| `risk` | The pre-trade circuit breaker (see [Risk limits](#risk-limits-risk)). Records each account's equity per session and sets what new entries may do. Reads the `account_state` mirror, no broker call. | `risk_equity`, `risk_state` |
| `scan_market` | Runs the backtest layer's live signal scan (`runner.RunLiveScan`) over the allowlisted strategies and stores the buy signals, the pipelines' reason for each verdict on the last five sessions (`scan_market_trace`), and the Markov predictions scored since each model's training cutoff (`markov_prediction_live`, kept because the model file on the instance is rebuilt from the bundle on every cold start). Never touches Schwab for orders. | `scan_market_buy_signal`, `scan_market_signal`, `scan_market_trace`, `markov_prediction_live` |
| `stage_orders` | Sizes signals against an account's live cash (primary strategy first, overlays get leftover cash) and stages bracket orders. `RunJob` is the job; it marks each signal staged or dismissed so a rerun never stages twice. | `stage_order` |
| `submit_orders` | Re-checks each staged order against current cash and positions, places the bracket, confirms fills, and opens the managed trade. Reconciles orders left mid-submit. | `submit_order` (attempt log), `manage_exit`, updates `stage_order` |
| `manage_exits` | Tracks open trades, counts trading days held (the backtest layer's `calendar`), and sells when the holding window or target is reached. | `manage_exit` |
| `check_health` | Live-data checks, a strategy-contract audit, and data-defined checks stored in the DB. Records findings and emails a report. | `check_health`, `check_health_audit`, `check_health_finding` |
| `live_vs_backtest_trades` | Rebuilds the live-versus-replay ledger. Not a cron job. | `live_vs_backtest_trade` |
| `live_vs_backtest_slippage` | Summarizes that ledger. Not a cron job. Run `live_vs_backtest_trades` first. | `live_vs_backtest_slippage` |
| `live_vs_backtest_report` | Runs those two commands and writes the report database and HTML. Not a cron job. | `live_vs_backtest_report.db` (separate file) |

**Server and interfaces**

| Package | Purpose |
|---|---|
| `appserve` | The HTTP server. Mounts `/cron/<job>` (App Engine cron, or an allowlisted Google account with an ID token), the dashboard, Schwab OAuth, warmup and health, and the login-protected `/runtime`. Loads secrets on App Engine. |
| `dashboard` | The web UI: the job-state page, `/api/tables`, the streamed `/account_state` sync and the `/stage_orders` dry-run preview, all behind Basic Auth. Exports `RequireAuth` for other operator routes. |
| `cronclient` | The client behind `orchestrator cron <job>`: calls a deployed `/cron/<job>` with a Google ID token. |
| `send_email` | The SMTP sender and the one report renderer every job's email goes through. |

**Configuration and support**

| Package | Purpose | Tables |
|---|---|---|
| `appconfig` | Resolves the environment into one `Config` and opens the database with schwaber's `dbutil`. | `appconfig` |
| `account_allowlist` | Parses `SCHWAB_ACCOUNT_NUMBERS` (max 5) and loads an account from the local mirror when Schwab is unreachable. | `account_allowlist` |
| `strategies` | Parses `STRATEGY_ALLOWLIST` and resolves allowlisted ids, including `a+b+c` stacks, to `pkg/backtest` strategies. | `strategies` |
| `auth` | Audit log of auth events, and `token pull\|push` through schwaber's `tokensync`. | `auth` |
| `job_runs` | Records that a job ran and whether it produced rows. | `job_run` |
| `runtimeenv` | Detects local vs App Engine and validates App Engine's cron header. | `runtimeenv` |

Other directories: `deploy_gae/` (`app.yaml`, `cron.yaml`, `deploy.sh`, setup scripts) and `scripts/` (local dev server and end-to-end checks).

## Running Locally

The configuration is the GCS object `gs://<bucket>/config/env` (Schwab API keys, `STRATEGY_ALLOWLIST`, `DASHBOARD_USERNAME`/`DASHBOARD_PASSWORD`, account numbers, paths). Every binary loads it at startup, so locally you only need `gcloud auth application-default login`; a variable you export in your shell overrides it. There is no `.env`.

**Plain `go run`** — the right default for day-to-day code changes:

```bash
go run ./cmd/orchestrator dashboard -port 8080
go run ./cmd/orchestrator scan_market -strategy sig-voo-buy-tecl+mara_tree+pdd_tree --dry-run
```

## Configuration: one GCS object (no Secret Manager, no `.env`, no `app.yaml` env)

All configuration, secrets and plain settings alike, is one object: **`gs://trade-orchestrator-data-<project>/config/env`**, in `.env` format (`KEY=VALUE` lines, `#` comments, one pair of quotes stripped). Every binary reads it first thing in `main()` (`pkg/schwaber/config.Bootstrap`), before anything reads the environment, on GAE, from the CLI and from `servelocal.sh`. It replaces the Secret Manager secret `schwab-env`, the `env_variables` of `app.yaml`, and `.env`.

- **Bootstrapping:** the app must find the bucket before it can read the object, so the bucket is derived, not configured: `GCS_BUCKET` if set, else `trade-orchestrator-data-<project>`, where the project is `GOOGLE_CLOUD_PROJECT` (App Engine sets it), `GAE_APPLICATION`, or, on a laptop, the compiled-in default `trade-orchestrator-508722`. Access is your Application Default Credentials locally and the App Engine service account on GAE (which has `storage.objectAdmin` on the bucket).
- **Precedence:** a variable already in the environment wins over the object: a shell `export`, the `GAE_*` variables App Engine sets. The object fills in the rest. `TZ` also sets the process's local time zone.
- **Failure is loud:** if the object cannot be read, the binary exits with the bucket, object and fix in the message. It never falls back to an old source.
- **Editing:** `deploy_gae/config.sh` (`keys` lists variable *names* only; `edit`, `set KEY=VALUE`, `unset KEY`, `pull`, `push`; each write keeps the previous copy as `config/env.prev`). A running instance keeps the values it started with: redeploy, or wait for the idle instance to stop. `config.sh migrate` builds the object once from the old `schwab-env` secret plus the old `app.yaml` settings (the `app.yaml` values won, as they did at run time).
- **Other variables the code reads:** `MARKET_DATA_SOURCE` (`alpaca_daily`, `schwab` or `alpaca`; unset means `alpaca_daily` on App Engine and `alpaca` on a laptop), `SCHWAB_TOKEN_ENV` (names the token object `schwab-token/<name>.json`; default `shared`, set it only when two deployments must not share one token), `LIVE_VS_BACKTEST_REFDB` (path of the strategies database the live_vs_backtest_winrate dashboard page reads; unset uses `$APP_FOLDER/refdata/strategies.db`), `BACKTEST_REPORTS` (default for `--reports-dir` of the report commands), `DRY_RUN_DEFAULT`, `SCHWAB_DEBUG`, `SCHWAB_LOG_FILE`, `SCHWAB_BASE_URL` (schwaber client settings), `CRON_ALLOWED_EMAILS` (who may call `/cron/*`).
- **Not in the object:** the Schwab refresh token stays its own object, `gs://<bucket>/schwab-token/<env>.json` (rotated on every refresh; synced only by `pkg/schwaber/tokensync`).
- **Keep the bucket private.** It holds Schwab credentials and account data. Enforce it: `gcloud storage buckets update gs://<bucket> --public-access-prevention`.
- **No automated test reads GCS** (Rule 4: no fakes). `ParseEnv`, `Project` and `Bucket` are tested; the first live proof is `deploy_live_local/servelocal.sh --dry-run`, which reads the object and lists its variable names.
- **Deploy-time only:** `deploy_gae/deploy.sh` needs a GitHub token to vendor the private `schwaber` module: `GITHUB_TOKEN`, else `gh auth token` (run `gh auth login` once); with neither the deploy stops and says so.

## Deploying to App Engine

```bash
./deploy_gae/deploy.sh
```

See [`deploy_gae/README.md`](../../deploy_gae/README.md). `cron.yaml` is the only scheduler.

### Fill prices

`manage_exit` records what a position really cost and sold for, not just the order prices. The entry limit stays in `entry_price`; the broker's average execution price goes in `entry_fill_price` when `manage_exits` sees the entry fill, and a stop or take-profit exit settled from the broker's record goes in `exit_fill_price`. `exit_price`, `pnl` and `pnl_pct` are measured from the fills when known, from the limit and the trigger only when the order list showed no execution (the row says so). `manage_exits` also corrects closed rows that were recorded without fills, from the orders of the last 30 days. The fills come from `schwab.Order.FillPrice` (`pkg/schwaber/fills`), which reads `orderActivityCollection`; an order's own `price` and `stopPrice` are never treated as a fill, because a buy limit of 271.76 can fill at 256.69 and a stop at 217.41 fills lower on a gap.
