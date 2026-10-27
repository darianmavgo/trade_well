# Schwaber: Charles Schwab Trading & Data Engine (Go)

> **Moved 2026-10-27.** This was the `schwaber` repository README. The code is now `pkg/schwaber/` in the single `trade_well` module; paths in this document are relative to the repo root (`cmd/`, `sql/`, `refdata/`, `data/`, `deploy_*` stay at the root). Build with `make build`; binaries land in `bin/`.

A robust, safety-first Go trading toolkit and data engine for the **Charles Schwab Trader API**, making direct calls to Schwab's official endpoints with zero external SDK dependencies, specifically tailored for executing trades in **ROTH IRA** accounts and persisting complete account analytics to Turso (libSQL).

---

## Features

- ⚡ **Go Native**: Blazing fast, statically typed, single-binary CLI.
- 🌐 **Direct Official Schwab API**: No third-party wrapper dependencies; direct HTTPS REST calls to official Charles Schwab endpoints (`trader/v1`, `marketdata/v1`, `oauth`).
- 💾 **Turso Data Ingestion (`listall`)**: Reads complete data across all linked accounts and persists accounts, positions, 60-day order histories, transaction logs, and user preferences into the Turso database.
- 🗄️ **Auditable Raw Data**: Captures exact HTTP payloads and response codes in `raw_responses` table for full trade auditing.
- 🔐 **Automated Token Management**: Native OAuth2 lifecycle management with token persistence and automated refresh via HTTP Basic Auth against `api.schwabapi.com/v1/oauth/token`.
- 🛡️ **Roth IRA Safety Guardrails**:
  - Pre-trade cash & buying power verification (prevents overdrafts).
  - Short-selling prevention for Roth IRA accounts.
  - Limit price collar warnings (>15% deviation).
- 📊 **Real-Time Market Data**: Fetches real-time equity and ETF quotes directly from Schwab Market Data endpoints.

---

## Quick Start

### 1. Build the Binary

```bash
make build                  # every cmd/* binary into bin/, including bin/schwaber
go build -o bin/ ./cmd/schwaber   # just this one
```

A bare `go build ./cmd/schwaber` writes `./schwaber` into the current directory; use one of the two commands above.

### 2. Configure Credentials

`schwaber` does not read a `.env` file. At start-up `config.Bootstrap` fills every unset variable from the config object `gs://<bucket>/config/env` (the bucket is `GCS_BUCKET`, else derived from the GCP project), so the object is required and you need `gcloud auth application-default login`. A variable already exported in your shell wins over the object. Set your developer credentials in the object:

```bash
deploy_gae/config.sh set SCHWAB_APP_KEY=your_app_key_here SCHWAB_APP_SECRET=your_app_secret_here \
  SCHWAB_CALLBACK_URL=https://127.0.0.1 SCHWAB_TOKEN_PATH=token.json
deploy_gae/config.sh set TURSO_DATABASE_URL=libsql://<db>.turso.io TURSO_AUTH_TOKEN=<token>
```

`deploy_gae/config.sh keys` lists the variable names stored (never values).

---

## CLI Commands

### 1. Ingest All Account Data (`listall`)

Fetches all linked account numbers, hashes, balances, open positions, 60-day orders, transaction history, and streamer preferences directly into Turso:

```bash
./bin/schwaber listall
```

#### Schema Overview (Turso, SQLite dialect)
- `accounts`: Account numbers, hashes, cash balances, buying power, liquidation values, day trader status.
- `positions`: Long/short quantities, average execution price, market value, current-day P/L ($ and %), settled shares.
- `orders`: Order ID, session, duration, strategy types (Single, OCO, Trigger), prices, quantities, statuses.
- `order_legs`: Individual order legs, instructions (BUY/SELL), symbols, and asset types.
- `transactions`: Activity IDs, trade and settlement dates, net amounts, and transaction descriptions.
- `user_preferences`: Streamer WebSocket configurations, correlation IDs, channels, and function IDs.
- `raw_responses`: Raw JSON payloads and HTTP status codes for complete auditing.

Query your local database anytime:
```bash
turso db shell <db> "SELECT symbol, long_quantity, average_price, market_value, current_day_pl FROM positions;"
```

### 2. Authenticate (One-time Setup)

```bash
./bin/schwaber auth
```
*Connects with Schwab Developer API and saves authenticated tokens to `token.json`.*

### 3. Inspect Accounts & Holdings

```bash
# View account overview
./bin/schwaber accounts

# View open positions, average price, market value, and day P/L
./bin/schwaber accounts --positions
```

### 4. Fetch Market Quotes

```bash
./bin/schwaber quote VOO
./bin/schwaber quote AAPL
```

---

## Testing

Run the Go test suite:

```bash
make test
# or
go test -v ./...
```

---

## Packages (`pkg/`)

Everything reusable lives in `pkg/`. `cmd/schwaber` and `trade_orchestrator` are both thin callers of these packages, so one implementation serves the CLI, a local server and App Engine. `schwaber` imports nothing from the other repos.

| Package | Purpose |
|---|---|
| `schwab` | The official Charles Schwab API client (`trader/v1`, OAuth). Loads and refreshes the token under a file lock, retries once on 401, lists and reads accounts, orders and quotes, and places orders. `PlaceOrder` is idempotent (see `orderintent` below). Also holds the Schwab types (`Order`, `Quote`, `Account`, `FlexTime`), `ConfirmPlacedOrder` (polls the real order status after a 201), `IsReauthRequired` and `IsPermanentAuthError`. |
| `trader` | Trading logic on top of `schwab`. `TraderClient` resolves configured accounts (`GetAccounts`, `FilterLinkedAccounts`), parses balances and positions, builds order specs (single order and 1st-triggers-OCO bracket, with the -20% crisis stop when a strategy has none), and runs the pre-trade `SafetyGuard` (buying power on buys, a buy with no reported buying power is refused, held quantity on sells, no short selling, and a price collar: a limit more than `PRICE_COLLAR_WARN_PCT` (default `0.5`) from the market warns, more than `PRICE_COLLAR_REJECT_PCT` (default `1.0`) is refused; `0` turns a step off). Also HTTP request/response logging. |
| `orderintent` | Durable "about to place" records. A live order writes an `order_intents` row before the HTTP call, keyed by a deterministic client order id, and reconciles unresolved rows against the broker, so a crash or timeout cannot double-place an order. |
| `accountstore` | Mirrors Schwab accounts, positions, orders, transactions and user preferences into the shared database. The table names are a parameter: `schwaber listall` uses the defaults (`accounts`, `positions`, ...) and `trade_orchestrator` uses its own `account_state_*` names. Also builds the display tables and the ingestion summary. |
| `tokensync` | The one place the Schwab token is synced to and from GCS (`gs://$GCS_BUCKET/schwab-token/<env>.json`). Pulls only when the remote token is strictly newer, pushes after every save, and exposes `Push` and `GCSAccessToken`. |
| `oauthcli` | The interactive OAuth authorization-code flow for a terminal: prints the authorize URL, reads the pasted redirect, exchanges it for tokens, and lists the linked accounts. Used by `schwaber auth` and `orchestrator auth`. |
| `oauthhttp` | The same OAuth flow as HTTP routes (`/auth/schwab`, `/auth/schwab/callback`) for a server such as App Engine. The start route needs the dashboard Basic Auth. |
| `config` | Loads configuration from the environment, which `Bootstrap` fills at process start from `gs://$GCS_BUCKET/config/env` (no `.env`, no Secret Manager) (credentials, callback URL, token and DB paths, debug), optionally from Secret Manager (`LoadSecretManagerEnv`). Real environment variables win over `.env`. |
| `dbutil` | Opens the shared Turso database (required; no local file outside `go test`) and holds the SQLite-dialect helpers (`AddColumn`, `EnsureOnce`). |

## Commands (`cmd/`)

There is one binary, `cmd/schwaber`, a thin dispatcher over `pkg/`. `--debug` (or `-v`) prints full HTTP payloads, `--dry-run` validates an order against the safety guard without placing it. Commands are live by default; `--live` is accepted and `--live=false` also previews.

| Command | What it does |
|---|---|
| `schwaber auth` | One-time OAuth setup through `oauthcli`; saves `token.json`. Accepts a pasted redirect URL or code as an argument. |
| `schwaber listall` | Reads every linked account (balances, positions, 60 days of orders, transactions, preferences) into Turso through `accountstore`, and prints a summary. |
| `schwaber accounts [--positions]` | Shows accounts, balances, and (with `--positions`) holdings and day P/L. |
| `schwaber quote <SYMBOL>` | Fetches a real-time quote. |
| `schwaber killswitch [TICKERS] [--dry-run] [-y] [-account N]` | Market-sells open long positions across linked accounts (all by default, or only the tickers given). It cancels open orders first, to avoid OCO-child rejections. Run it by hand; nothing schedules it. |

The manual order commands (`buy`, `sell`, `bracket`, `orders`, `cancel`) were removed on 2026-10-27. They picked one account through `SCHWAB_ROTH_ACCOUNT_NUMBER` or IRA auto-detection; orders are now placed only by the orchestrator jobs, which target `SCHWAB_ACCOUNT_NUMBERS`.

`cmd/schwaber` is a thin wrapper: it parses flags and calls the matching package: `pkg/schwaber/listall`, `pkg/schwaber/accounts`, `pkg/schwaber/quote` and `pkg/schwaber/killswitch`. Those packages take an `io.Writer` (and an `io.Reader` where they prompt) and never exit the process; `pkg/schwaber/clicolor` holds the shared colors and banner.

## Order idempotency (`pkg/schwaber/orderintent`)

Live `PlaceOrder` writes a durable `order_intents` row (`pending`) **before** the HTTP call, keyed by `clientOrderId` (generated if omitted). On success the intent is `acked` with the broker order id; clear rejects become `failed`; ambiguous network/5xx errors stay `pending`/`unknown` for reconcile.

- Database: Turso, same as listall.
- Schwab’s public Trader OpenAPI does **not** document `clientOrderId` or list/filter by it. We still send the field; reconcile matches recent orders by symbol / instruction / quantity / entered time (`ReconcilePendingIntents`).
- `trade_orchestrator` pairs this with `staged_orders.client_order_id` (UNIQUE) and `PENDING → SUBMITTING` claim so overlapping submit jobs cannot double-place.


## Turso (shared database)

`TURSO_DATABASE_URL` and `TURSO_AUTH_TOKEN` are required: `dbutil.Open` and `orderintent.OpenDefault` use that remote database (pure Go, over HTTPS) and fail when they are unset. There is no local SQLite fallback. Ignored under `go test`, where tests open an in-memory or `t.TempDir()` SQLite file. `orderintent.Open(path)` still opens a local file when Turso is unset; only `ibkr_api` relies on that.
