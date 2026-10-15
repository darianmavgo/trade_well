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
| Strategy allowlist | `STRATEGY_ALLOWLIST` names deployed strategies; promotion is manual. Whether the scan enforces it was **unverified**. | `trade_orchestrator/pkg/strategy_allowlist`, `pkg/backtest_sync` | package only |
| Sizing | Uses leftover cash only, never evicts a position, caps each order at 95% of remaining cash, skips held or already-staged symbols. | `trade_orchestrator/pkg/stage_orders/sizing.go` | yes |
| Duplicate-order protection | Same symbol and instruction rejected within a run; `PENDING` to `SUBMITTING` claim; durable order intent before each broker call; ambiguous results are reconciled, not re-placed. | `submit_orders.go:264`; `schwaber/pkg/schwab/place_idempotent.go`; `schwaber/pkg/orderintent`; `ibkr_api/pkg/ibkr/orders.go` | yes |
| Submit-time validation | Re-checks cash and positions; `ValidateOrder` checks buying power, blocks selling more than held, warns on a limit more than 15% from market. | `schwaber/pkg/trader/safety.go:30` (also used by IBKR) | yes |
| Dry-run / preview | Schwab CLI and IBKR have dry-run (IBKR uses what-if); cron has dry-run and `SIMULATED` modes. `-y` confirms live CLI orders. | `schwaber/cmd/schwaber/main.go`, `ibkr_api/pkg/orders/orders.go:78`, `submit_orders.go` | yes |
| Brackets and crisis stop | Entry plus take-profit plus stop as one order structure. If the strategy has no stop, a -20% crisis stop is added. | `submit_orders.go:306` (`trader.CrisisStopFraction`), `schwaber/pkg/trader/order_tree.go` | order tree yes |
| Hold window exits | `manage_exits` at 09:35 and 15:50 ET; exit date counted in NYSE trading days from the actual fill. | `trade_orchestrator/pkg/manage_exits` | yes |
| `check_health` | Audits each position: tracked, strategy known, exit date, broker stop and take-profit match. Rolls up to HEALTHY / WARNING / ACTION_REQUIRED and is emailed. | `trade_orchestrator/pkg/check_health` | yes |
| Live vs model | Slippage in basis points, weekly digest, strategy status. | `pkg/reconcile_trades`, `pkg/audit_slippage`, `pkg/strategy_status` | yes |
| Cron auth | Google ID token with email in `CRON_ALLOWED_EMAILS`. | `trade_orchestrator/pkg/appserve/cronauth.go` | not checked |
| Failure alerts | `job_runs` records failures; report emails from `scan_market`, `stage_orders`, `manage_exits`, `check_health`; `account_state` retried. | `pkg/appserve/cron.go` | partly |
| Refresh token guard | Prevents re-arming a consumed Schwab refresh token. | `schwaber/pkg/tokensync/tokensync.go` | not checked |
| Rejection loop stop | `MaxFailures` stops repeated failures, but only where set. | `trade_orchestrator/pkg/trigger_limits/seat.go` | not checked |

## 3. Fail-open behaviours

These let an order through when a control cannot do its job.

1. **Buying power of 0 or unreadable passes `ValidateOrder`.** The check is `requiredCash > availableCash && availableCash > 0` (`schwaber/pkg/trader/safety.go`). This is a bug.
2. **Unreadable risk state does not block.** `submit_orders.go:350` logs "risk state unreadable, not blocking"; `stage_orders/run.go:100` behaves the same.
3. **Unreadable killswitch config or database reads as OFF** (`trade_orchestrator/pkg/appserve/killswitch.go:21-40`). The code comment says this is deliberate, because the jobs need the same database.
4. **Price collar is a warning only.** A limit more than 15% from market still goes out.

## 4. Gaps

Ranked by risk reduction per effort; each names the owning layer.

| # | Missing control | Owner |
|---|---|---|
| 1 | Fix the buying-power bypass (reject on 0 or unreadable) | `schwaber/pkg/trader/safety.go` |
| 2 | Fail closed on unreadable risk state and killswitch config for live submits; dry-run may continue | `submit_orders`, `stage_orders`, `appserve/killswitch.go` |
| 3 | Make the 15% price collar a rejection (configurable) | `safety.go` |
| 4 | Per-order dollar cap, per-account daily order count, max open positions (none exist; `safety.go` says so deliberately) | `pkg/risk`, `RISK_*` env |
| 5 | Auto-killswitch on N consecutive job failures, a hard drawdown breach, or an order-count spike (killswitch is manual only) | `pkg/risk` calling `pkg/killswitch` |
| 6 | Hard-drawdown flatten step (for example 30%) calling `exit_market`; off by default, needs a decision | `pkg/risk` |
| 7 | Stale-data gate at submit: block when scan data or account equity is too old | `submit_orders`, `pkg/risk` |
| 8 | Leverage-aware exposure: weight 2x/3x/inverse ETFs (TECL, TSLL, MULL) in gross exposure; per-symbol and per-sector caps. The 100% cap ignores leverage. | `pkg/risk` plus a leverage-factor table |
| 9 | Cross-account aggregate exposure (risk state is per account) | `pkg/risk` |
| 10 | Schwab refresh-token expiry alert (about 7 days) and a dead-man's switch for crons that did not run (email is sent by the job itself) | `check_health`, `tokensync` |
| 11 | Liquidity cap at order time (share of ADV); only backtests check it | `stage_orders` sizing |
| 12 | Retry/backoff on 429/5xx and partial-fill handling | `schwaber` client |
| 13 | Failure emails for every job, not four | `appserve/cron.go` |

## 5. Untested paths

- `schwaber/pkg/killswitch` and `ibkr_api/pkg/killswitch` have no test files. Under the no-mocks rule they can only be covered by dry-run on real data; until that exists they are untested.
- Broker order paths are covered only by dry-run and preview, never by a live test.
- Plaintext OAuth token logging was recorded as a gap on 2026-09-23. `schwaber/pkg/schwab/client.go` appears to log only a pull message, but it was not audited in full.
- The Litestream restore drill has never been recorded.

## 6. Related

`docs/LearningLog.md` section 5 (earlier gap list; its daily-loss and max-positions line is now partly out of date), `Purpose.md`, `Purpose-Goals.md`.
