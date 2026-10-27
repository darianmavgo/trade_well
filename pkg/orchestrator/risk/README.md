# risk

The pre-trade circuit breaker. There is no separate risk database: it is **two tables in the
orchestrator's main SQLite database** (`DB_PATH`, the same file as `stage_order`, `killswitch` and
`account_state_*`), created on first use by `risk.EnsureSchema`. It was added to answer one question
before every buy: *is this account allowed to open new positions right now, and how big?*

It limits **new entries only**. Exits, stops and take-profits keep running. That is what makes it
different from the killswitch (`pkg/killswitch`), which stops every cron job and touches no positions.

## How it runs

1. `account_state` syncs each account's equity and positions from Schwab into the mirror tables
   `account_state_account` and `account_state_position`.
2. The `risk` job (`orchestrator risk`, `/cron/risk`, 09:17 and 16:18 ET on weekdays) reads that mirror,
   records the session's equity, measures drawdown and the one-session change, applies the limits and
   saves one row per account in `risk_state`. It makes no broker call.
3. The other jobs read `risk_state` through `risk.Load`:

| Reader | What it does with the state |
|---|---|
| `stage_orders` | Skips every signal when halted; sizes against `equity x Scale`; trims cash to the exposure cap. Fails closed (stages nothing) if the state cannot be read, except in a dry run. |
| `submit_orders` | Last gate before the broker (`riskGate`): refuses a buy when halted (`RISK_HALTED`), over a per-order cap (`RISK_LIMIT`), or when the state cannot be read (`RISK_UNREADABLE`, the order stays PENDING and is retried next run). A dry run only previews. |
| `trigger_limits` | Seats no new limits while halted. |
| dashboard `/risk` | Shows every account's latest state. |

An account that was never assessed is treated as unrestricted (a fresh database must not refuse
trades). An account whose mirror is more than 4 days old is skipped with a warning, so a failing sync
cannot pass for a flat day. An email goes out when an account's entry size changes.

## Tables

### `risk_equity` — one row per account per session

| Column | Type | Meaning |
|---|---|---|
| `account_number` | TEXT | Schwab account number (primary key with `session_date`) |
| `session_date` | TEXT | ET session date, `YYYY-MM-DD` |
| `equity` | REAL | liquidation value recorded that session |
| `recorded_at` | DATETIME | when the row was written (UTC) |

The high-water mark is `MAX(equity)` over earlier sessions; the day change compares with the latest
earlier session. `orchestrator risk --reset-peak` deletes the earlier rows so the peak restarts at
today's equity (use it after a withdrawal, which otherwise looks like a drawdown).

### `risk_state` — the latest assessment, one row per account

| Column | Type | Meaning |
|---|---|---|
| `account_number` | TEXT PK | |
| `assessed_at` | DATETIME | when the job ran (UTC) |
| `session_date` | TEXT | ET session the equity belongs to |
| `equity` | REAL | latest recorded equity |
| `peak` | REAL | highest recorded equity |
| `drawdown` | REAL | fraction below `peak`, >= 0 |
| `day_change` | REAL | fraction vs the previous session (negative = loss) |
| `gross` | REAL | long market value as a fraction of equity |
| `scale` | REAL | 1 = full size entries, 0 = none |
| `halt_entries` | INTEGER | 1 when `scale` is 0 |
| `max_gross` | REAL | exposure cap in force (0 = none) |
| `reasons` | TEXT | newline-separated explanation of any restriction |
| `max_order_pct` | REAL | per-order cap, fraction of equity (0 = none) |
| `max_orders_per_day` | INTEGER | entry orders per account per ET session (0 = none) |
| `max_positions` | INTEGER | open long positions (0 = none) |

The last three columns were added later; an existing table gains them on the next `EnsureSchema` and
old rows read as 0 (no cap) until the `risk` job runs again. Always name columns when inserting by hand.

The per-order checks read two more existing tables: `stage_order` (entries already placed today) and
`account_state_position` (open long positions).

## Limits and their environment variables

Set like any config value (`deploy_gae/config.sh set NAME=value`). A bad value stops the jobs at
start-up. `0` turns a limit off.

| Variable | Default | Effect |
|---|---|---|
| `RISK_DRAWDOWN_LADDER` | `0.10:0.5,0.20:0` | `drawdown:scale` pairs: 10% below the peak entries are sized at half, 20% below they halt |
| `RISK_DAILY_LOSS_PCT` | `0.05` | equity down this much since the last session halts entries |
| `RISK_MAX_GROSS_PCT` | `1.0` | long market value may not exceed this fraction of equity |
| `RISK_MAX_ORDER_PCT` | `0.60` | one entry order may cost at most this fraction of equity |
| `RISK_MAX_ORDERS_PER_DAY` | `40` | entry orders placed per account per session |
| `RISK_MAX_POSITIONS` | `60` | open long positions per account |

The three order caps are deliberately loose: they stop a runaway or a typo, not a strategy.

## Automatic killswitch

After assessing, the `risk` job engages the killswitch (`changed_by = 'auto: risk'`) and emails when either
trigger holds. It sells nothing; the killswitch refuses every cron job, exits included, until you turn it off
(`orchestrator killswitch off`). To sell everything use `orchestrator exit_market --dry-run`, then without it.

| Variable | Default | Trigger |
|---|---|---|
| `RISK_AUTOKILL_DRAWDOWN` | `0.35` | any account this far below its high-water mark |
| `RISK_AUTOKILL_FAILURES` | `6` | `stage_orders` or `submit_orders` failed this many runs in a row (`job_run`). Other jobs are ignored. |

`orchestrator risk --dry-run` prints "AUTO-KILL would engage" without acting. The check runs only when the
`risk` job runs (09:17 and 16:18 ET), so a streak is noticed at the next run, not instantly.

## Commands

```
orchestrator risk                 # assess every account and save (emails if entry size changed)
orchestrator risk --dry-run       # print the assessment, write nothing
orchestrator risk --reset-peak    # restart the high-water mark at today's equity
```

Pure functions (`Assess`, `State.CheckEntry`, `ParseLadder`) take numbers and return a state, and
are tested with literal inputs; the job and the loaders are tested against a real temporary SQLite file.
