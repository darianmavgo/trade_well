# ibkr_api

Go client for Interactive Brokers' Web API, generated from `api-reference.json` and shaped like `schwaber` (same client, options, errors, order/account/quote shapes, `account_state_*` tables).

## Quick start (Client Portal Gateway: the route for an individual account)

```bash
go build -o bin/ibkr ./cmd/ibkr
./bin/ibkr gateway setup       # files, Java, port, process: skips what is already done
./bin/ibkr gateway login       # opens Chrome at the gateway, waits until you have logged in
./bin/ibkr account_state       # live positions + orders -> ibkr.db (account_state_* tables)
```

Every command that talks to IBKR makes sure the gateway is running and logged in first (starting it and opening the login page if not); `-no-gateway` skips that. The login itself (username, password, 2FA) is yours, in the browser. The session idles out after about six minutes: `ibkr gateway keepalive` tickles it, and every API call does too. On macOS port 5000 belongs to AirPlay Receiver, so the gateway is put on 5001 (`conf.yaml` is edited once, original kept as `conf.yaml.orig`). `setup_client_portal.sh` and `open_login.sh` are thin wrappers over these commands.

## Commands (same shape as `schwaber`)

| `ibkr ...` | schwaber | does |
|---|---|---|
| `gateway setup / start / stop / status / open / login / keepalive` | | manage the Client Portal Gateway (`pkg/gateway`) |
| `auth` | `auth` | check the session |
| `accounts` | `accounts` | accounts, cash, value, position count |
| `account_state` (`listall`) | `listall` | positions and orders into `ibkr.db`; a working take-profit / stop become `profit_taker` / `stop_loss` |
| `quote SYMBOL` | `quote` | last / bid / ask |
| `buy` / `sell SYMBOL QTY [PRICE]` | `buy` / `sell` | market or limit; **preview (IBKR what-if) unless `-live`**, confirmed unless `-y` |
| `bracket SYMBOL QTY ENTRY [-profit -stop]` | `bracket` | entry + take-profit + stop as one IBKR bracket |
| `orders [-status S]`, `cancel ID` | `orders`, `cancel` | this session's orders; cancel |
| `killswitch [TICKER...]` | `killswitch` | cancel working orders and market-sell positions; preview unless `-live` |
| `reconcile` | | resolve pending order intents against the broker |

## Same as schwaber, on purpose

`ibkr.Order`, `Account`, `Quote`, `LinkedAccount` are schwaber's types (aliases), so `trader.BuildOrderSpec`, `BuildBracketOrderSpec`, `ParseOrderLegs`, the safety guard, `ResolvedAccount` and trade_orchestrator's job packages take IBKR values unchanged. `HashValue` is the account id. Statuses are mapped to WORKING / FILLED / CANCELED / REJECTED. `PlaceOrder` has schwaber's durable-intent protection (record before HTTP, dedupe, reconcile) and sends the client order id as IBKR's `cOID`, which IBKR echoes back as `order_ref`, so matching an unresolved intent is exact. Shared code is imported from schwaber (`orderintent`, `accountstore`, `tokensync`, `config.Bootstrap`, the logging HTTP client), never copied.

## Differences you should know

- **Order confirmations:** IBKR answers many orders with warning prompts (no market data, market-order confirmation, price caps). Without `IBKR_AUTO_CONFIRM_REPLIES=1` a live order stops with `ReplyRequiredError` and is *not* placed; with it, prompts are confirmed automatically. What-if previews show the warnings.
- **Orders list:** IBKR only lists the current session's orders, so `From`/`To` filter that list and cannot reach further back.
- **Positions payload:** IBKR gives no day P/L (`current_day_pl` stays 0); `exit_date` needs trade_orchestrator's `manage_exit`.
- **Brackets:** IBKR shows a bracket's legs as separate orders; the parent's id is what `PlaceOrder` returns.

## OAuth 1.0a (no gateway): `pkg/oauth1`

IBKR's self-service OAuth for individual accounts. `./setup_oauth1.sh` generates the two RSA keys and `dhparam.pem` in `oauth1_keys/`, adds the `IBKR_OAUTH1_*` settings to `.env`, and (with `--verify`) runs `ibkr auth`. You upload `public_signature.pem`, `public_encryption.pem` and `dhparam.pem` in IBKR's portal and copy back the consumer key, access token and access-token secret.

`pkg/oauth1` does the protocol: an RSA-SHA256 signed `live_session_token` request carrying a Diffie-Hellman challenge, the shared-secret arithmetic and IBKR's signature check, then HMAC-SHA256 signing of every request. `ibkr.Client` switches to it when `IBKR_OAUTH1_CONSUMER_KEY` is set (mode `oauth1`), stores the live session token in the token file (GCS-synced), and starts the brokerage session (`/iserver/auth/ssodh/init`). Mode precedence: OAuth 1.0a, then OAuth 2.0 (`IBKR_CLIENT_ID`), then the gateway.

Tested: the RFC 5849 base-string example, RSA signature verification, the DH exchange against the documented arithmetic, request signing. **Not tested against IBKR**; details taken from IBKR's published OAuth 1.0a description (realm `limited_poa`, `prepend` = hex of the decrypted secret, LST = HMAC-SHA1(K, secret), K as Java `BigInteger.toByteArray`) are unverified until a live call succeeds.

## Configuration

`.env` in this directory (never overrides an exported variable), then the shared GCS config object. `IBKR_PASSWORD` is never read.

| Variable | Meaning |
|---|---|
| `IBKR_ACCOUNT_ID` / `IBKR_ACCOUNT_IDS` | account(s) to read |
| `IBKR_TRADING_MODE` | `paper` or `live`; refuses to start if the account id contradicts it (paper ids start `DU`/`DF`) |
| `IBKR_GATEWAY_URL` | default `https://localhost:5000` |
| `IBKR_TOKEN_PATH`, `IBKR_DB_PATH`, `IBKR_LOG_FILE`, `IBKR_DEBUG` | as in schwaber (`ibkr_token.json`, `ibkr.db`, `ibkr_api.log`) |
| `IBKR_OAUTH1_CONSUMER_KEY`, `_ACCESS_TOKEN`, `_ACCESS_TOKEN_SECRET`, `_SIGNATURE_KEY_PATH`, `_ENCRYPTION_KEY_PATH`, `_DHPARAM_PATH`, `_REALM` | OAuth 1.0a mode |
| `IBKR_CLIENT_ID`, `IBKR_PRIVATE_KEY[_PATH]`, `IBKR_KEY_ID`, `IBKR_CLIENT_SECRET`, `IBKR_SCOPE`, `IBKR_CREDENTIAL`, `IBKR_IP` | OAuth mode (registered consumers only) |

## Layout

`pkg/ibkr` client (`client.go` auth + requests, `account.go` schwaber-shaped accounts/quotes, `orders.go` order translation, placement and intents, `oauth1.go`, `api_gen.go` one method per operation generated by `go run ./cmd/genapi`), `pkg/gateway` (Client Portal Gateway management), `pkg/oauth1`, `pkg/trader`, `pkg/orders`, `pkg/accounts`, `pkg/quote`, `pkg/killswitch`, `pkg/account_state`, `pkg/config`, `cmd/ibkr`, `cmd/genapi`.

## Status: what is and is not verified

- **Run live against your account** (read-only): `gateway status/setup`, `accounts`, `account_state` (positions and orders into `ibkr.db`), `orders`, `quote`, and IBKR's what-if for a market buy and a bracket (the wire format was accepted with `error: null`).
- **Not run live**: placing, cancelling and the killswitch's sells (no order was sent), OAuth 1.0a and 2.0 (no credentials). These are unit-tested where they are pure (bracket translation, status mapping, signing, gateway setup) but have never touched IBKR.
- Response shapes for positions, summary, secdef search, snapshots and live orders come from IBKR's documentation and what the gateway returned, not from the OpenAPI file (which leaves them untyped).
- No fake gateway exists (Rule 4), so nothing automated exercises the HTTP paths.
