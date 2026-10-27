# Trading Stack Review — backtestgosqlite / trade_orchestrator / schwaber

> **Archived 2026-10-27.** Historical record, not maintained. Paths and statements refer to the old three-repo layout (`backtestgosqlite`, `trade_orchestrator`, `schwaber` as separate repositories, Litestream, `schwaber.db`); the code is now under `pkg/backtest`, `pkg/orchestrator` and `pkg/schwaber`.

Date: 2026-09-21 (historical: written when the database was Litestream-replicated SQLite; the orchestrator is Turso-only now)
Scope: all three repos that make up the live-trading pipeline (research → signal → order → broker), reviewed end to end, plus a look at how mature open-source trading frameworks (Backtrader, NautilusTrader, Zipline, Freqtrade, QuantConnect Lean) handle the same problems.

## Bottom line

The pipeline works and the individual pieces are each reasonably well built for what they are. But almost everything examined — the strategies, the simulator, the strateval gating system, and the GAE migration itself — is **one to two weeks old**, per git history, and several of the connections *between* the three repos are advisory conventions rather than enforced code paths. That's the theme underneath every finding below: nothing here is "wired wrong" so much as "not wired at all yet." Before this is rock solid for unattended live-money operation, one finding needs fixing today regardless of anything else:

> **`trade_orchestrator`'s `/cron/*` endpoints accept a hardcoded bypass header (`X-Cron-Auth: gae-cron`) instead of only trusting GAE's own `X-Appengine-Cron` header** (`internal/runtimeenv/runtimeenv.go:90-100`, gate at `internal/appserve/cron.go:51-61`). The literal string is baked into the binary, isn't derived from any secret, and these routes aren't behind the dashboard's Basic Auth. If the app is reachable at its public URL, anyone who extracts that string can POST to `/cron/manage_exits` or `/cron/submit_orders` and trigger a real market sell or live order submission. This is a live-money vulnerability, not a hardening nice-to-have.

## What we have now

### backtestgosqlite — research & strategy evaluation
- Clean `Strategy` interface (Go or SQL-pipeline backed), a process-wide registry, ~34 strategies (many are template variants: N-day-decline → buy a leveraged ETF).
- `pkg/simulator`: day-by-day portfolio sim with stop-loss/take-profit/time-up exits, multiple position sizers, a shared-account mode for running strategy stacks off one cash ledger. Exit ordering is conservative (stop checked before target on the same bar), and gap-through-stop/target is modeled by comparing against the bar's open — a genuinely good realism touch.
- `pkg/analytics`: full metrics suite (CAGR, Sharpe/Sortino, Calmar, Ulcer, Alpha/Beta vs. benchmark).
- `cmd/livescan` reuses the exact same `GenerateSignals` code path as the backtest engine (`pkg/runner/signal_scan.go`), so signal-logic parity between backtest and live is structural. `pkg/runner/asof.go` hard-fails a live scan if the market DB's newest bar isn't current, which is a real safeguard against trading on stale data.
- `cmd/strateval` (added **2026-09-20**, i.e. yesterday relative to this review): IS/OOS split, Tier A/B/C/D gating (win rate, drawdown, Sharpe, trade count thresholds), a lifecycle ledger. Already needed a path bugfix and a threshold retune within a day of being added.
- Options support is narrow but honest: `pkg/options/coveredcall.go` models one specific covered-call cycle well and documents its own limitation (priced from last-trade bars, not bid/ask).

### trade_orchestrator — live orchestration (now GAE-only, Cloud Run just decommissioned)
- Single binary, both CLI and (on GAE) an HTTP server. Cron routes: `account_state`, `scan_market`, `submit_orders`, `manage_exits`, `check_health`.
- Order flow is sound in shape: staged orders → re-validated against live account state → submitted → **confirmed via `ConfirmPlacedOrder`/order-status polling before being recorded as filled** (never trusts a 201 as "done"). Exit manager cross-checks broker-held quantity before computing any sell math, so it won't sell against a position it doesn't actually see at the broker.
- Dry-run is a first-class, well-threaded concept through every job function — but only the **CLI** entry points use it; the deployed cron handlers hardcode `dryRun=false` with no override.
- Litestream replicates `schwaber.db` to GCS so state survives GAE's scale-to-zero between invocations; `EnsureReady`/`BeforeJob`/`AfterJob` wrap every cron handler.
- Duplicate-order protection exists and was clearly earned the hard way (`e933203 "Ending dupe order submissions in the db"`) — there's an in-memory `seen` map per run plus a "held" check before re-staging.
- Test coverage on the actual job logic is decent: duplicate-rejection, manage_exits aging, phantom-position skipping, and stack-sizing edge cases all have tests.

### schwaber — the Schwab API client
- Solid OAuth handling: proactive token refresh, reactive 401 retry, cross-process file locking, a distinct sentinel error for a dead refresh token.
- Order confirmation is done right: it polls `GetOrder` after placing and treats HTTP 201/Location as "accepted," not "filled," before declaring success — the same discipline trade_orchestrator uses on top of it.
- A `killswitch` command exists that defensively cancels open orders before flattening positions, to avoid OCO-child rejection races.
- Safety guards exist for per-order value ceiling, buying power, and no-short-selling.
- Unit tests cover order/bracket spec building, safety-guard math, and the auth/refresh path.

## What's missing or risky, by area

### 1. The safety net between repos is advisory, not enforced
`cmd/strateval`'s Tier A/B/C/D gating in backtestgosqlite is explicitly documented as additive-only: `sync-deployed` only writes a local ledger and **never touches `STRATEGY_ALLOWLIST`**. On the trade_orchestrator side, the only actual live-trading gate is whether a strategy ID string is present in that hand-edited env var — with zero code-level link to strateval's tier output. Nothing stops an unevaluated or Tier-D strategy from being pasted into the allowlist and trading real money. The eval pipeline you built is a dashboard right now, not a circuit breaker.

This matters concretely for the TSLL ops-check strategy from earlier in this session (`tsll-daily-one-share`, committed as `e415844 "Strategy used for checking ops not for making money"`) — it's fine as a smoke test, but there's currently no mechanism that would stop it (or anything like it) from ending up in `STRATEGY_ALLOWLIST` by mistake and trading with real capital indefinitely.

### 2. No alerting on the paths that actually run in production
Only `handleCronAccountState` emails on failure. `scan_market`, `submit_orders`, `manage_exits`, and `check_health` — the four jobs that can act on live money — never call the email package on the deployed GAE cron path; that alerting code exists but is only wired into the **CLI** entry points, which cron never calls. README.md's claim that "every job that can act on live money sends exactly one report email per invocation" is currently false for the live deployment. A CRITICAL check_health finding (e.g. negative available funds) or a failed manage_exits market sell today produces **zero human notification** beyond Cloud Logging — no SMS, Slack, or paging integration exists anywhere in the stack.

### 3. Idempotency is real but not structural
- **trade_orchestrator**: duplicate-order protection is an in-memory `seen` map scoped to one process run, with no `UNIQUE` constraint in SQLite backing it up. Two overlapping requests to the same instance (a hung request plus a new cron trigger) could still race past the check.
- **schwaber**: `PlaceOrder` sends no client-order-ID / idempotency key to Schwab at all, and nothing durably records "I'm about to place this order" before the HTTP call. If the process is killed after Schwab accepts the order but before the response is read, re-running the same command double-places it. This is the single biggest live-order risk in schwaber.

### 4. No kill switch, no aggregate risk limits
Across all three repos there is no max-concurrent-positions limit, no daily-loss/drawdown circuit breaker, and no market-hours/holiday calendar (`pkg/runner/asof.go` and trade_orchestrator's cron both treat every weekday as a trading session — a real holiday could either false-flag staleness or, worse, get silently accepted). The only existing kill switch is schwaber's CLI `killswitch` command, which is manual, local, and not reachable from the dashboard or triggerable automatically off a check_health CRITICAL finding — check_health is detective-only today; it writes findings, it doesn't act on them.

### 5. Backtest realism understates live cost exactly where it matters most
Several live-candidate strategies (`gld-decline`, `sig-voo-buy-tecl`, `sig-voo-buy-spxu`, and the new `tsll-daily-one-share`) are configured with **zero slippage and zero commission**. The simulator has no bid/ask spread model at all — every fill is priced off OHLC bar values with a flat percentage slippage. For the leveraged/inverse ETFs this stack actually trades (TECL, SPXU, TQQQ, SQQQ, TSLL), real spread and market-impact costs are almost certainly understated, which inflates exactly the strategies most likely to look good enough to greenlight.

### 6. Selection-on-backtest-performance overfitting risk
The commit history and `pkg/strategy/failed_training/` (nine archived strategies explicitly kept for the record of "failed win rate <50% / CAGR <10%") show the normal workflow here is build → backtest → discard if it doesn't score well, across dozens of variants. strateval's single 12-month IS/OOS split is good discipline but doesn't fully protect against this kind of multiple-comparisons risk. The most recent commit message is a direct admission of this from you: *"I have looked at so many strategies built on a market gains as buy signal that it is suspicious that I haven't a good one."*

### 7. Data durability has an unconfirmed failure window
Litestream's post-write replication confirms by polling GCS for up to 30 seconds; if that times out it just logs and returns — the caller has no way to know the write didn't durably land. Combined with GAE Standard's `min_instances: 0` (scale-to-zero), there's a real if narrow window where a completed job's writes could be lost on the next cold-start restore. No backup/restore drill or DR runbook was found.

### 8. Secrets are bundled, not scoped
On GAE, `STRATEGY_ALLOWLIST`, Schwab API keys, SMTP credentials, and the dashboard password all live in one Secret Manager blob (`schwab-env`, a verbatim copy of `.env`). Changing the allowlist means republishing — and an instance re-pulling — the same secret as your trading credentials. There's no least-privilege separation.

### 9. Other concrete gaps worth fixing
- schwaber has no 429/5xx retry or backoff anywhere, and no circuit breaker — a transient Schwab hiccup mid-bracket-order sequencing (e.g. inside `killswitch`) can leave state half-configured.
- schwaber's `Order` struct and confirmation logic never read filled/remaining quantity — a partial fill is invisible to the live order-placement path.
- schwaber logs full request/response bodies, including live OAuth tokens in plaintext, to `schwab_api.log` on every call.
- schwaber's deployed systemd timer (`deploy/schwaber-jobs.service`) invokes `run-all-jobs`/`health-check` subcommands that don't exist in the current binary — stale config left over from before this logic moved into trade_orchestrator.
- Cmd-level test coverage is 0% across `cmd/livescan`, `cmd/strateval`, `cmd/backtest` in backtestgosqlite — the actual entry points a cron job invokes are untested; the safety logic underneath (stale-data rejection, gate thresholds) is only unit-tested one layer down.
- Stooq's data source doesn't actually adjust for splits/dividends despite carrying an `AdjClose` field — a latent correctness gap if it's ever used for a `TotalReturnProvider` strategy.
- ETF universe discovery only considers `active=true` tickers — classic survivorship bias; delisted/liquidated leveraged ETFs are invisible to backtests by construction.

## What the open-source trading world does differently

None of this argues for migrating onto a different framework — a bespoke Go/SQLite stack against one broker for one account is a reasonable architecture, and a rewrite would cost far more than fixing these specific gaps. But a few patterns from mature systems are worth deliberately adopting:

- **Idempotent order submission.** The standard fix for schwaber's biggest live risk (#3 above) is a client-generated idempotency key sent with the order and durably recorded *before* the HTTP call, so a retry after a timeout replays the original result instead of double-submitting. This is table stakes in payment and order APIs generally, and shows up explicitly in retail trading tooling (e.g. OpenAlgo's `client_order_id` pattern). ([openalgo PR #1991](https://github.com/marketcalls/openalgo/pull/1991))
- **Backtest/live reconciliation as a first-class report, not a one-off.** QuantConnect's Lean engine runs a fresh backtest over the exact window an algorithm just traded live and overlays the two equity curves, specifically to catch fill-model or data divergence between backtest and reality. ([QuantConnect docs](https://www.quantconnect.com/docs/v2/cloud-platform/live-trading/reconciliation)) You already have the two ingredients this needs — backtestgosqlite's simulator and trade_orchestrator's `active_trades` fill history — just not a job that diffs them. This would directly surface finding #5.
- **A framework-level kill switch and dry-run posture that the *deployed* path actually respects**, not just the CLI. Freqtrade treats dry-run as a config-level mode the whole bot loop honors, and pairs it with a risk-guard/kill-switch layer that fails closed on missing or malformed state. ([Freqtrade bot-basics](https://www.freqtrade.io/en/stable/bot-basics/)) Your dry-run threading in trade_orchestrator's job functions is already built to this standard — it's only the cron handlers that don't use it.
- **Same code path for backtest and live, enforced by the interface, not by convention.** NautilusTrader's headline feature is that identical strategy code runs in both modes against a shared event model. ([comparison writeups](https://autotradelab.com/blog/backtrader-vs-nautilusttrader-vs-vectorbt-vs-zipline-reloaded)) backtestgosqlite already does this at the signal-generation layer (`GenerateSignals` is shared) — the gap is one level up, in the fact that a strategy's simulated fill assumptions (slippage/commission) have no equivalent enforcement against what trade_orchestrator/Schwab actually deliver.

## Recommended priority order

**P0 — do before any more live trading**
1. Remove the `X-Cron-Auth: gae-cron` bypass in `runtimeenv.IsGAECronHeader`; trust only the real GAE header, and put IAP or equivalent in front of `/cron/*` as defense in depth.
2. Wire real email/alerting into the four GAE cron handlers that currently have none (scan_market, submit_orders, manage_exits, check_health) — reuse the `email.BuildJobReport` path already built for the CLI.
3. Add a durable idempotency key to schwaber's `PlaceOrder` (record intent locally before the HTTP call; replay on retry) and back trade_orchestrator's in-memory dedup with a `UNIQUE` constraint on `staged_orders`/`active_trades`.
4. Make the kill switch reachable and automatic: expose it from the dashboard (authenticated) and trigger it automatically on a CRITICAL check_health finding instead of only recording one.

**P1 — near-term correctness**
5. Make strateval's gating enforceable: at minimum, a startup/cron check that logs (and, ideally, refuses) any `STRATEGY_ALLOWLIST` entry that isn't Tier A in the ledger.
6. Add max-concurrent-positions, aggregate exposure, and a daily-loss circuit breaker; add a real market holiday calendar to `pkg/runner/asof.go` and trade_orchestrator's cron gating.
7. Add 429/5xx retry-with-backoff and a circuit breaker to schwaber's HTTP client; add filled/remaining-quantity fields to its `Order` type and react to partial fills.
8. Delete or fix schwaber's stale systemd deploy files referencing nonexistent subcommands.

**P2 — durability & observability**
9. Make Litestream's post-write GCS confirmation a hard failure/alert on timeout, not a silent log line; run and document one real restore drill.
10. Split the `schwab-env` secret so `STRATEGY_ALLOWLIST` can change without touching trading credentials.
11. Redact tokens from schwaber's `schwab_api.log`; move to structured logging with correlation IDs.

**P3 — backtest realism & overfitting hygiene**
12. Require a nonzero slippage/commission floor for any strategy that can be greenlit; model spread and volatility-scaled slippage instead of a flat percentage.
13. Move strateval to walk-forward/multi-regime OOS validation and account for the number of strategy variants tried, given the documented try-many-discard-most workflow.
14. Add tests at the `cmd/` entry-point level in backtestgosqlite (livescan, strateval, backtest) and for schwaber's `PlaceOrder`/`ConfirmPlacedOrder`/`CancelOrder`/killswitch — currently the highest-risk, least-tested code in either repo.
15. Build the backtest-vs-live reconciliation job described above (re-run the simulator over each strategy's actual live-traded window and diff against real fills) — it would catch #5 and #6 automatically going forward instead of relying on manual review.

---
*This review covers code and configuration as of the commits current at review time (backtestgosqlite `e415844`, trade_orchestrator `ca99151`, schwaber `241ad5e`). Nothing in the three repos was modified as part of this review.*
