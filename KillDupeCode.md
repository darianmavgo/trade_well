# KillDupeCode

Opportunities to delete code from `trade_orchestrator` (TO) by using packages that
already exist in `schwaber` (SW) and `backtestgosqlite` (BT), or by moving the
TO code *into* SW/BT once and importing it back.

Scope: read-only scan of `cmd/` + `pkg/` in all three repos. Nothing was changed.
Line counts are approximate (from the function boundaries). Items are ranked by
payoff/risk, not by size. Each item is tagged:

- **DELETE** – SW/BT already has it; TO just needs to call it.
- **PROMOTE** – TO owns it, but it is generic; move it to SW/BT and import it.
- **DEDUPE-IN-PLACE** – duplicate lives inside TO itself.

Baseline: TO is ~12.1k lines of Go (incl. tests), SW ~5.9k, BT ~30k.
TO already imports only `schwaber/pkg/{trader,schwab,config,oauthhttp,orderintent}`
and `backtestgosqlite/pkg/{strategy,runner,models}`. It does **not** yet use
`schwaber/pkg/tokensync`, `backtestgosqlite/pkg/{appenv,storage,refdb}`, or
`schwaber/pkg/trader.SafetyGuard`.

> **Caveat before any of this:** `go.mod` has `replace … => ../schwaber` and
> `../backtestgosqlite`, *and* TO has a `vendor/` dir containing copies of both.
> Check that `vendor/modules.txt` is in sync with the sibling repos before
> starting (`go mod vendor` after every SW/BT change), or drop vendoring while
> the three repos live in one workspace (a `go.work` file is the cleaner fit).

---

## 1. Byte-for-byte / near-identical duplicates (easiest wins)

### 1.1 `gcsAccessToken` — DEDUPE (PROMOTE)  ~15 lines
- TO: `pkg/dbsync/gcs.go:110`
- SW: `pkg/tokensync/tokensync.go:167`
- Same ADC lookup with scope `devstorage.read_write`; TO's version carries a
  dead `_ = oauth2.TokenSource(ts)` line to keep an import alive.
- **Action:** export one `GCSAccessToken(ctx)` from SW (`tokensync`, or a new
  tiny `pkg/gcsutil`) and use it from `dbsync`. Also lets TO drop its direct
  `golang.org/x/oauth2` import.

### 1.2 `.env` line parser — PROMOTE  ~25 lines ×3
- TO: `pkg/appserve/secrets.go:74` `loadDotEnv`
- SW: `pkg/config/config.go:105` `loadDotEnv`
- SW: `pkg/config/secretmanager.go:19` (same key=value loop, inline)
- BT: `pkg/appenv/appenv.go:28` `load()` (same idea, third copy)
- All: skip blanks/`#`, split on `=`, `Trim(…, "\"'")`, set only if env empty.
- **Action:** one exported `config.ParseDotEnv([]byte)` / `config.LoadDotEnv(path)`
  in SW (BT can import it or keep its own; TO must not have one).

### 1.3 Secret Manager fetch — DELETE  ~40 lines
- TO: `pkg/appserve/secrets.go:20-72` (`LoadSecretsIfCloud`, `writeSecret`)
- SW: `pkg/config/secretmanager.go` (`loadSecretManagerEnv`, unexported)
- Both open a `secretmanager.Client`, `AccessSecretVersion(.../versions/latest)`.
  TO writes to a file then re-reads it; SW parses straight into env.
- **Action:** export SW's loader (e.g. `config.LoadSecretManagerEnv(ctx, project, name)`)
  and have `appserve` call it. Deletes `writeSecret`, `loadDotEnv`, `envOr`,
  `firstNonEmpty`, and TO's direct dependency on
  `cloud.google.com/go/secretmanager` (plus a chunk of `vendor/`).
  Check first whether SW's `config.Load()` already triggers this when
  `SECRET_MANAGER_*` is set — if so, `LoadSecretsIfCloud` may reduce to a
  runtime-detect guard.

### 1.4 `orderNumericID` == `locationOrderID` — PROMOTE  ~8 lines
- TO: `pkg/submit_orders/submit_orders.go:44`
- SW: `pkg/schwab/place_idempotent.go:116` (unexported, identical body)
- **Action:** export `schwab.LocationOrderID`; delete the TO copy.

### 1.5 `flattenOrders` — PROMOTE  ~25 lines ×2 (inside TO)
- TO: `pkg/submit_orders/submit_orders.go:52`
- TO: `pkg/manage_exits/manage_exits.go:286`
- Both recurse into `Order.ChildOrderStrategies`. The submit_orders comment
  says it is deliberately duplicated to avoid a package dependency.
- **Action:** `func (o Order) Flatten() []Order` / `schwab.FlattenOrders` in SW
  next to the `Order` type. Related helpers `orderSymbol`, `orderIsSell`,
  `findWorkingSellLimitID`, `sellFillOnSymbol` (`manage_exits.go:302-350`) are
  also pure functions of `schwab.Order` and belong there too (~50 lines),
  and SW already has `trader.ParseOrderLegs` doing similar leg extraction.

### 1.6 `windowForSignal` — DEDUPE-IN-PLACE (then PROMOTE)  ~35 lines
- TO: `pkg/manage_exits/window.go:14` and `pkg/stage_orders/window.go:15` are
  the same function (diff shows only `windowForTrade` extra in manage_exits).
- **Action:** BT owns `strategy.Strategy`, `StrategyConfig`, and `models.Signal`;
  the "hold days / take-profit / stop-loss for this signal, falling back to
  DefaultConfig" logic is a strategy concept. Add `strategy.WindowFor(strat, sig)`
  in BT, delete both TO copies. `windowForTrade` then becomes a 5-line wrapper.

---

## 2. Reimplementations of things SW/BT already provide

### 2.1 Token sync — DELETE  ~95 lines + a shell script
- TO: `pkg/auth/token_sync.go` (`SyncToken`, `ResolveSyncSchwabTokenScript`) and
  `deploy_gae/sync_schwab_token.sh`; CLI `runTokenSync` in `cmd/orchestrator/main.go:1124`.
- SW: `pkg/tokensync` (`SyncBeforeUse`, `SyncAfterSave`) — already invoked
  inside `schwab.Client`. `appserve/secrets.go:47` even says so:
  *"schwab-token itself now lives in GCS, synced entirely inside schwaber."*
- So TO shells out to a script to do a job the library already does on every
  token load/save. **Action:** delete `SyncToken` + script search logic; make
  `token-sync pull|push` call `tokensync.SyncBeforeUse` / `SyncAfterSave`
  directly (or drop the subcommand if nobody uses it). Keep only the `auth`
  audit-log table writer if wanted.

### 2.2 Interactive OAuth — PROMOTE/DELETE  ~65 lines
- TO: `pkg/auth/oauth.go` `RunInteractiveAuth`
- SW: `cmd/schwaber/main.go:122` `runAuth` (same steps: print `AuthorizeURL`,
  read pasted URL, `ExtractCodeFromURL`, `ExchangeAuthCode`, `ListLinkedAccounts`)
  — but it is in `package main`, so TO cannot import it.
- **Action:** move the flow to `schwaber/pkg/oauthcli` (or into `pkg/oauthhttp`)
  taking an optional `func(event, detail string)` hook; SW's `runAuth` and TO
  both call it, TO passes `logEvent` as the hook. `IsPermanentAuthError`
  (`auth/auth.go:22`) is a superset of `schwab.IsReauthRequired`; fold the extra
  string matches into SW's function and delete TO's.

### 2.3 Account/order/transaction ingest — PROMOTE  ~500 lines (biggest single item)
- TO: `pkg/account_state/account_state.go` `parseAndSaveAccounts/Orders/
  Transactions/UserPreferences`, `saveRawResponse`, `boolToInt`, `Summary`
  (`summary.go`), plus `schema.go`.
- SW: `pkg/listall/listall.go` `initDatabase`, `Run` (was `cmd/schwaber/main.go` `runListAll`),
  `parseAndSave*`, `saveRawResponse`, `boolToInt`, `printSummaryReport`.
- Same function names, same endpoints, same JSON shapes; only table names
  differ (`accounts` vs `account_state_account`, etc.) and TO's version adds
  `execer` (tx/no-op for streaming) and progress events. SW's copy is stuck
  in `package main`.
- **Action:** create `schwaber/pkg/accountstore` with a configurable table
  prefix, taking TO's more capable `execer` design; SW's `listall` and TO's
  `account_state.Sync` both become thin callers. Removes ~450 lines from one
  side and stops two schemas drifting. Biggest risk: existing `schwaber.db` /
  `orchestrator.db` table names — keep prefix as a parameter and add no
  migration.

### 2.4 `filterLinkedAccounts` / account resolution — DELETE  ~30–60 lines
- TO: `account_state.go:223` `filterLinkedAccounts`, `account_allowlist/resolve_db.go`
  `ResolveFromDB` / `ResolveAllFromDB`, main.go `resolveAccounts*`.
- SW: `trader.ResolveRothAccount`, `TraderClient.GetAccounts(ctx, targets)`
  (`client.go:76`), `ResolvedAccount` type (`account.go:46`).
- TO's DB variants reconstruct a `*trader.ResolvedAccount` from stored rows;
  the live path duplicates `GetAccounts` filtering. **Action:** put a
  `trader.ResolveFromLinked(linked, targets)` in SW used by both the live
  and DB paths; keep only the DB-row → `ResolvedAccount` mapper in TO.

### 2.5 Litestream `dbsync` — PROMOTE (optional)  ~870 lines
- TO: `pkg/dbsync/*` (binary download, config gen, GCS freshness, restore,
  replicate, meta table). Nothing in SW/BT does this today, but SW's
  `orderintent.Store` (own SQLite file) and `schwaber.db` would benefit, and it
  is not orchestrator-specific.
- **Action (low priority):** promote to a shared `pkg/dbsync` in SW (SW already
  owns the GCS + ADC code from 1.1). Not a deletion for TO overall, but it
  removes the only reason TO needs its own GCS/HTTP plumbing.

### 2.6 Strategy allowlist / stack parsing — PROMOTE  ~80 lines
- TO: `pkg/strategies/stack.go` (`IsStack`, `ParseStack`, `MemberGreenlit`) and
  `strategies.go` (`Parse`, `Contains`, `ResolveRequest`, `ResolveGreenlit`).
- BT: `runner.ResolveStrategies` (`runner/resolve.go`) parses "all"/CSV; stack
  handling exists in `runner/shared_account.go` (`SharedAccountID`,
  `StackRequest`) and `runner/stack_eval.go`, but ids are joined with `+`
  ad hoc — no shared parser. BT and TO therefore each know the `a+b+c` format.
- **Action:** add `strategy.ParseStack/IsStack/ExpandStacks` to BT and have
  BT's runner and TO both use them. `strategies.Contains` is `slices.Contains`
  — delete it. `strategies.Parse` and `account_allowlist.Parse` are the same
  "split CSV, trim, dedupe, error if empty" function: share one
  (`cliutils`-style helper in BT, or stdlib + 4 lines).

### 2.7 Market-DB path resolution — DELETE  ~25 lines
- TO: `pkg/scan_market/scan.go:191` `resolveMarketDB` (+ `pathExists`),
  `dbsync.marketDBPath`, `appconfig.MarketDB/BacktestRoot` fields.
- BT: `pkg/appenv` — `appenv.MarketDB()`, `Data()`, `Folder()`, with
  `APP_FOLDER` semantic already shared with TO.
- **Action:** replace with `appenv.MarketDB()`. Removes `BACKTEST_ROOT`
  guesswork. (TO's `appconfig.applySchwabPaths` also re-implements
  `APP_FOLDER` handling that both SW and BT have separately; when unifying,
  SW's `config` should call `appenv.Folder()` or vice-versa.)

### 2.8 Signal scan wrapper — TRIM  ~50 lines
- TO: `pkg/scan_market/livescan.go` `RunLivescan` is a thin wrapper around
  `runner.RunSignalScan` and a hard-coded `defaultBarsTable = "backtest_start"`.
- BT: `runner.SignalScanOptions` could default `Table` and `Concurrency`
  itself (BT's own `pkg/livescan/main.go` repeats the same defaults).
- **Action:** BT adds `runner.RunLiveScan(strats, marketDB, outDir)` with those
  defaults + the `as_of == ""` check; TO deletes `livescan.go`. Also
  `SignalScanResult` in TO (`signal_scan.go:20`) shadows BT's type of the same
  name — rename or embed; don't keep both.

### 2.9 Trading calendar — DELETE  ~40 lines
- TO: `manage_exits/trade.go:174-210` `CalculateTradingDays` + hard-coded 3
  holidays (New Year, Jul 4, Christmas; no Good Friday, MLK, Thanksgiving,
  weekend-observed rules).
- BT: `runner/asof.go` `LastCompletedEquitySession`, `NextEquitySession`,
  which own the session logic for as-of resolution.
- **Action:** one calendar package in BT (`pkg/calendar`), used by both. This
  is also a **correctness** fix — TO's holiday list under-counts holidays, so
  `trading_days_held` will be too high around Good Friday/Thanksgiving, causing
  early time-stop exits.

### 2.10 SQLite open + pragmas — DELETE  ~35 lines
- TO: `pkg/appconfig/opendb.go` `OpenDB` (WAL, synchronous, busy_timeout,
  `SQLITE_FUSE_SAFE`).
- BT: `storage.OpenSQLite` (`storage/sqlite.go:31`) and `refdb.Open` each set
  their own pragmas; SW: `orderintent.Open`, `initDatabase`.
- **Action:** four places set nearly the same pragma list. Add one
  `dbutil.Open(path, opts)` (in SW, since it is the base dependency) and use it
  everywhere. Note BT uses `sqlx` + `mattn/go-sqlite3`?? vs TO `modernc.org/sqlite`
  — verify driver names before unifying; they register different names.

---

## 3. Bar-chart of what could move (TO lines)

| Item | TO lines removable | Destination |
|---|---:|---|
| 2.3 account ingest | ~450 | SW `pkg/accountstore` |
| 2.5 dbsync (optional) | ~870 | SW `pkg/dbsync` |
| 2.1 token sync | ~95 | SW `tokensync` (already there) |
| 1.3 secrets loader | ~40 | SW `config` |
| 2.2 interactive auth | ~65 | SW `pkg/oauthcli` |
| 2.6 stack/allowlist | ~80 | BT `strategy` |
| 1.5 flattenOrders + helpers | ~75 | SW `schwab` |
| 2.9 calendar | ~40 | BT `pkg/calendar` |
| 1.6 windowForSignal (×2) | ~35 | BT `strategy` |
| 2.8 livescan wrapper | ~50 | BT `runner` |
| 2.7 market DB path | ~25 | BT `appenv` |
| 2.10 open db | ~35 | SW `dbutil` |
| 1.1/1.2/1.4 small helpers | ~60 | SW |

Realistic total: **~900 lines of non-test code** without dbsync, **~1,700** with it,
plus matching test code.

## 4. Leave in TO (looks similar, isn't a duplicate)

- `stage_orders`, `submit_orders`, `manage_exits`, `check_health`: orchestration
  logic against TO's own tables. SW's `trader.SafetyGuard` is *not* used by TO —
  worth checking whether `submit_orders` should call `SafetyGuard.ValidateOrder`
  (a gap, not a duplicate).
- `dashboard`, `send_email`, `appserve/cron.go`, `runtimeenv`: GAE/deployment-specific.
- `job_runs`, `scan_market/buy_signals.go`: TO-only tables.

## 5. Duplication *between* SW and BT (not TO, but blocks the above)

- `.env` loading (3 copies, §1.2) and `APP_FOLDER` resolution (`appenv` vs `config` vs `appconfig`).
- `gcsAccessToken` only in SW+TO today; if BT adds GCS it will become 3.
- `backtestgosqlite/pkg/strategy/failed_training/` and `strategy/ARCHIVE/`,
  `cmd/*` dirs contain a lot of near-identical decision-tree files
  (`mu_decision_tree.go`, `mara_decision_tree.go`, `etf_decision_trees`,
  `strategy/decisiontree.go`, `mara_tree.go`): 1,100+ line study files each.
  Outside TO's scope but the biggest raw duplication in the tree; separate
  audit recommended.

## 6. Suggested order

1. Housekeeping: `go.work` or refresh `vendor/`; run all three test suites for a green baseline.
2. Tiny exports in SW (1.1, 1.3, 1.4, 1.5) → delete TO copies. Low risk, mechanical.
3. BT: `strategy.WindowFor`, `strategy.ParseStack`, `calendar` (1.6, 2.6, 2.9). Do the calendar first — it fixes a real bug.
4. SW `oauthcli` + token sync cleanup (2.1, 2.2).
5. `accountstore` extraction (2.3) — the largest, needs schema-compat test.
6. Optional: shared `dbsync`, shared DB opener.

## 7. Not verified

- I did not compile/run anything and did not diff every function body — items
  marked "identical" (1.1, 1.4, 1.6) were diffed; the rest are judged by
  signature, comments and skim of the code.
- I did not check `deploy_gae/`, `ARCHIVE/`, `scripts/*.go`, or TO `tmp/` for
  additional dead code; `ARCHIVE/` alone is likely deletable outright.
- I did not audit TO tests for what would need to move with each item.
- 2.10: a claim about which SQLite drivers each repo registers needs checking.
