# Data Needs: Is `account_state` Enough to Rebuild State?

> **Archived 2026-10-27.** Historical record, not maintained. Paths and statements refer to the old three-repo layout (`backtestgosqlite`, `trade_orchestrator`, `schwaber` as separate repositories, Litestream, `schwaber.db`); the code is now under `pkg/backtest`, `pkg/orchestrator` and `pkg/schwaber`.

**Question:** Does `account_state` retrieve enough information for every job to do its
job, even if the app DB (`$APP_FOLDER/$APP_DB`, i.e. `schwaber.db`) is missing
or wiped?

**Answer:** No. `account_state` mirrors genuine broker facts, but the job
pipeline's state is split across two categories, and `account_state` only ever
populates one of them.

## What `account_state` writes

`accounts`, `positions`, `orders`, `order_legs`, `transactions`,
`user_preferences`, `raw_responses` — all raw Schwab truth, re-fetched live
every run.

`account_state` **never writes to `active_trades` or `staged_orders`** (confirmed
directly in `pkg/account_state` — the only mention of those two
tables in that file is a comment).

## Per-job dependency

| Job | Needs | Recoverable from `account_state` alone? |
|---|---|---|
| `scan_market` | Which symbols are already held, to avoid re-buying | Yes — reads live `positions`/`active_trades`, degrades gracefully if the latter is empty |
| `submit_orders` | `staged_orders` (its whole input queue) | Lost, but low-stakes — a lost `PENDING` order is just a missed signal; the next scan re-evaluates fresh |
| `check_health` | Live positions/working orders (for protection checks) | Yes, mostly self-healing — even the `health_checks` check definitions reseed themselves on init |
| **`manage_exits`** | **`active_trades`**: strategy attribution, target price, stop price, entry time, `max_trading_days` | **No** |

## Why `manage_exits` is the real gap

`manage_exits` never looks at the broker's raw `positions` mirror to
discover holdings — it *only* iterates `GetOpenTrades()`, i.e. `active_trades`
rows. If the app DB is lost while a strategy-opened position is still live:

1. **The position becomes completely invisible to `manage_exits`.** It
   reports "No open strategy trades found" and does nothing for that symbol,
   indefinitely.
2. **Time-based exits stop working entirely.** "Sell if held ≥ N trading
   days" is logic *we* enforce — Schwab has no native order type for it, and
   nothing about it is recoverable from Schwab's API.
3. Profit/stop-loss protection has a partial safety net: if the original
   bracket order's TP/SL legs are still resting *at Schwab* as real working
   orders, they'll still fire independently of our DB — real risk protection
   isn't fully gone. But that's incidental, not by design, and it stops
   helping once those legs get canceled or expire.
4. **Strategy attribution is permanently unrecoverable.** Nothing Schwab
   returns says "mara_tree bought this MARA position" — that fact only ever
   existed in `active_trades.strategy_name`.
5. The only signal an operator gets is `check_health`'s existing
   `untracked_position` finding (a `WARNING`, not `ACTION_REQUIRED`) — easy
   to miss, and it doesn't reconstruct anything even if seen.

## Bottom line

`account_state` is sufficient for `scan_market`, `submit_orders`, and
`check_health` to keep functioning after a DB loss. It is **not** sufficient
for `manage_exits` — any position open at the time of loss loses its
time-based exit forever, and its identity as "a strategy trade" is gone for
good.

## Possible mitigation (not yet built)

A reconciliation step (on `account_state`, or a new dedicated job) that
best-effort adopts orphaned broker positions back into `active_trades`:

- `entry_price` / `quantity` — from `positions.average_price` /
  `positions.long_quantity` (always available).
- `entry_time` — from the earliest FILLED BUY order's `entered_time` for
  that symbol in the `orders`/`order_legs` mirror, if still within the
  lookback window.
- `strategy_name` — only inferable where a symbol maps unambiguously to one
  greenlit strategy today (e.g. MARA → `mara_tree`, PDD → `pdd_tree`). This
  is an operational coincidence of the current allowlist, not a guarantee —
  it breaks the moment two greenlit strategies could plausibly trade the
  same symbol.
- `target_profit_price` / `stop_loss_price` — recoverable only if the
  original bracket's TP/SL legs are still resting (unfilled) at the broker;
  otherwise unrecoverable.
- `max_trading_days` — only available if `strategy_name` was successfully
  inferred, by reading that strategy's current `DefaultConfig().HoldingWindow`.

Any row this reconciliation can't fully populate should be flagged for
manual review rather than guessed.
