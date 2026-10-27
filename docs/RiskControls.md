# Risk controls

Written 2026-10-15 from a read-only scan of `schwaber`, `trade_orchestrator`, `backtestgosqlite` and `ibkr_api`. Paths are relative to `trade_well/`. "Tested" means a test file exists for the control; no tests were run for this document. Items marked **unverified** were not confirmed in code.

## 1. Targets vs reality

- Target (`Purpose-Goals.md`): a stable 20 to 30% CAGR with under 10% drawdown.
- Real IBKR account: 41.1% drawdown over the 12 months to 2026-10-01 (`Purpose.md`).
- The default risk ladder (`trade_orchestrator/pkg/risk/risk.go:50`) halves new entries at 10% below the high-water mark and halts them at 20%. That is looser than the target, and its last step only stops new entries. Nothing flattens positions automatically.

## 2. Existing controls

| Control | What it does | Where | Tested |
|---|---|---|---|
| Killswitch | Cron refuses every job while engaged; `KillAll` cancels running jobs. Does not touch positions. | `trade_orchestrator/pkg/killswitch`, `pkg/appserve/cron.go:113`, web `/killswitch` | yes |
| `exit_market` | Cancels working orders, then market-sells whole-share longs. Skips and reports shorts, options, funds, fractions. Has dry-run; can engage the killswitch. | `trade_orchestrator/pkg/exit_market` | yes |
| Risk breaker | Drawdown ladder (halve at 10%, halt at 20%), daily loss halt (5%), long exposure cap (100% of equity). Entries only; exits keep running. Env overrides `RISK_*`. | `trade_orchestrator/pkg/risk/risk.go`; enforced in `stage_orders/run.go:96`, `submit_orders/submit_orders.go:336`, `trigger_limits/seat.go:74` | yes |
| Account allowlist | Caps and parses the account list (`MaxAccounts`). | `trade_orchestrator/pkg/account_allowlist` | yes |
| Strategy allowlist | `STRATEGY_ALLOWLIST` names deployed strategies; promotion is manual. Whether the scan enforces it was **unverified**. | `trade_orchestrator/pkg/strategy_allowlist`, `pkg/live_vs_backtest_sync` | package only |
| Sizing | Uses leftover cash only, never evicts a position, caps each order at 95% of remaining cash, skips held or already-staged symbols. | `trade_orchestrator/pkg/stage_orders/sizing.go` | yes |
| Duplicate-order protection | Same symbol and instruction rejected within a run; `PENDING` to `SUBMITTING` claim; durable order intent before each broker call; ambiguous results are reconciled, not re-placed. | `submit_orders.go:264`; `schwaber/pkg/schwab/place_idempotent.go`; `schwaber/pkg/orderintent`; `ibkr_api/pkg/ibkr/orders.go` | yes |
| Submit-time validation | Re-checks cash and positions; `ValidateOrder` checks buying power, blocks selling more than held, warns on a limit more than 15% from market. | `schwaber/pkg/trader/safety.go:30` (also used by IBKR) | yes |
| Dry-run / preview | Schwab CLI and IBKR have dry-run (IBKR uses what-if); cron has dry-run and `SIMULATED` modes. `-y` confirms live CLI orders. | `schwaber/cmd/schwaber/main.go`, `ibkr_api/pkg/orders/orders.go:78`, `submit_orders.go` | yes |
| Brackets and crisis stop | Entry plus take-profit plus stop as one order structure. If the strategy has no stop, a -20% crisis stop is added. | `submit_orders.go:306` (`trader.CrisisStopFraction`), `schwaber/pkg/trader/order_tree.go` | order tree yes |
| Hold window exits | `manage_exits` at 09:35 and 15:50 ET; exit date counted in NYSE trading days from the actual fill. | `trade_orchestrator/pkg/manage_exits` | yes |
| `check_health` | Audits each position: tracked, strategy known, exit date, broker stop and take-profit match. Rolls up to HEALTHY / WARNING / ACTION_REQUIRED and is emailed. | `trade_orchestrator/pkg/check_health` | yes |
| Live vs model | Slippage in basis points, weekly digest, strategy status. | `pkg/live_vs_backtest_trades`, `pkg/live_vs_backtest_slippage`, `pkg/strategy_status` | yes |
| Order caps | One order up to 60% of equity, 40 entry orders a session, 60 open positions. Read from `risk_state`. | `trade_orchestrator/pkg/risk/orders.go`, `submit_orders.go` `riskGate` | yes |
| Auto-killswitch | Engages the killswitch on a 35% drawdown or 6 failed runs in a row of `stage_orders` / `submit_orders`. | `trade_orchestrator/pkg/risk/autokill.go` | yes |
| Cron auth | Google ID token with email in `CRON_ALLOWED_EMAILS`. | `trade_orchestrator/pkg/appserve/cronauth.go` | not checked |
| Failure alerts | `job_runs` records failures; report emails from `scan_market`, `stage_orders`, `manage_exits`, `check_health`; `account_state` retried. | `pkg/appserve/cron.go` | partly |
| Refresh token guard | Prevents re-arming a consumed Schwab refresh token. | `schwaber/pkg/tokensync/tokensync.go` | not checked |
| Rejection loop stop | `MaxFailures` stops repeated failures, but only where set. | `trade_orchestrator/pkg/trigger_limits/seat.go` | not checked |

## 3. Fail-open behaviours

Updated 2026-10-15 after the first round of fixes.

| Behaviour | Status |
|---|---|
| Buying power of 0 or unreadable passed `ValidateOrder` | **Fixed**: a buy with no reported buying power is refused (`schwaber/pkg/trader/safety.go`, test added) |
| Unreadable risk state did not block | **Fixed** for live runs: `stage_orders` stages nothing and `submit_orders` leaves the buy PENDING as `RISK_UNREADABLE`. Dry runs only warn. |
| Unreadable killswitch config or database reads as OFF (`appserve/killswitch.go`) | **Left as is, on purpose**: every job needs the same database, so if it cannot be read the jobs fail on their own. |
| Price collar was a 15% warning only | **Changed**: warns at 50% and refuses at 100% (`PRICE_COLLAR_WARN_PCT`, `PRICE_COLLAR_REJECT_PCT`). A price below the market is at most 100% away, so a too-low typo can only warn; the per-order cap in `pkg/risk` is what bounds it. |
| A canceled or expired order that partly filled was reported as "never filled, no position opened" | **Fixed**: reported as `PARTIAL_FILL_ACTION_NEEDED` on every run, shares not hidden. The shares are not adopted automatically (see gaps). |

## 4. Gaps

| # | Control | Status |
|---|---|---|
| 1 | Buying-power bypass | Done |
| 2 | Fail closed on unreadable risk state | Done (killswitch left open, see above) |
| 3 | Price collar | Done, 50% warn / 100% reject |
| 4 | Per-order, per-day and open-position caps | Done: `RISK_MAX_ORDER_PCT` 0.60, `RISK_MAX_ORDERS_PER_DAY` 40, `RISK_MAX_POSITIONS` 60 |
| 5 | Auto-killswitch | Done: `RISK_AUTOKILL_DRAWDOWN` 0.35, `RISK_AUTOKILL_FAILURES` 6. Runs in the `risk` job (09:17 and 16:18 ET). It stops every cron job, exits included, and sells nothing. |
| 6 | Hard-drawdown flatten | **Not built, undecided.** The auto-killswitch emails the `orchestrator exit_market` command at 35% instead of selling. |
| 7 | Stale-data gate at submit | Detection exists (`check_health` `stale_account_snapshot` at 20h, the `risk` job skips a mirror older than 4 days, `scan_market` names a stale source). Nothing blocks `submit_orders` on stale data. `submit_orders` reads live balances from the broker, so equity is current at submit. A stale `risk_state` (the `risk` job stopped) is not blocked. |
| 8, 9 | Leverage-aware exposure, cross-account exposure | Skipped by decision |
| 10 | Token expiry warning, dead-man's switch | Already existed: `ready` warns under 48 hours of refresh-token life and `/cron/ready` emails when anything is not OK; `check_health` pipeline rows flag jobs that did not run. |
| 11 | Liquidity cap | Not in live trading. The 5% of bar volume cap (`max_bar_volume_share` 0.05) was a setting in the retired `backtestgo`; `backtestgosqlite` has the field but it is **off by default** (`docs/BacktestGoSettings.md`). So backtests may not apply it either, depending on the strategy config. |
| 12 | Retry/backoff on 429/5xx | Done for GET requests only: 3 retries at 0.5s, 1.5s, 4s, honouring `Retry-After` up to 10s (`schwaber/pkg/schwab/retry.go`). Order placement is never retried. The loop itself is untested (no fake servers); the decision functions are tested. |
| 12b | Partial fills | Detected and flagged (above). **Open:** no automatic adoption of the filled shares into `manage_exits`, and it is not known whether Schwab activates the take-profit and stop legs of a bracket for a partly filled parent. |
| 13 | Failure emails for every job | `writeJobErr` already emailed every cron failure. They are now HTML, built on the same report template as each job's own email. |

## 5. Untested paths

- `schwaber/pkg/killswitch` and `ibkr_api/pkg/killswitch` have no test files. Under the no-mocks rule they can only be covered by dry-run on real data; until that exists they are untested.
- Broker order paths are covered only by dry-run and preview, never by a live test.
- The GET retry loop in `schwaber/pkg/schwab/retry.go` has no test of the loop itself (a test would need a fake server, which the project forbids). The partial-fill branch in `reconcilePendingFill` is tested only through its decision function `endedPartlyFilled`.
- `trade_orchestrator/pkg/appserve/cron.go` has `var sendFailureEmail`, a swappable function used by `cron_test.go`. That is a fake under rule 4. It predates this work and was left in place; remove it only if you ask.
- Plaintext OAuth token logging was recorded as a gap on 2026-09-23. `schwaber/pkg/schwab/client.go` appears to log only a pull message, but it was not audited in full.
- No restore from a Turso backup has been recorded.

## 6. Related

`docs/LearningLog.md` section 5 (earlier gap list; its daily-loss and max-positions line is now partly out of date), `Purpose.md`, `Purpose-Goals.md`.
