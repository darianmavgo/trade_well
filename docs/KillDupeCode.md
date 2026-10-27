# KillDupeCode

Opportunities to delete duplicated code across `trade_orchestrator` (TO), `schwaber` (SW) and `backtestgosqlite` (BT), by calling a package that already exists or by moving the code once into the lower layer and importing it back.

**Re-scanned 2026-10-27** against the working trees; the repos were merged into one module the same day (paths below use the old repo names; see `docs/FlattenAssessment.md`). The first version (2026-10-13, commit 8b504d6) listed 16 items. Most are now done, so this file now records status and only the work that is left.

Size now: TO about 40.4k lines of Go (incl. tests), SW about 7.9k, BT about 58.4k. (The 2026-10-13 baseline of 12.1k / 5.9k / 30k was smaller or counted differently; do not compare totals.)

Tags: **DELETE** (a lower repo already has it), **PROMOTE** (generic code in TO, move it down), **DEDUPE-IN-PLACE** (both copies in one repo).

## 1. Status of the original 16 items

| # | Item | Status | Evidence |
|---|---|---|---|
| 1.1 | `gcsAccessToken` | **Done** | SW exports `tokensync.GCSAccessToken` (`tokensync.go:207`); TO's copy and `dbsync` are gone |
| 1.2 | `.env` line parser | **Partly done** | SW exports `config.ParseEnv` (`gcsenv.go:48`). BT `appenv.load()` still has its own parse loop (about 25 lines). TO's `loadDotEnv` now survives only in a test helper (`send_email_test.go:18`) |
| 1.3 | Secret Manager fetch | **Done** | No `secretmanager` import or `LoadSecretsIfCloud` anywhere |
| 1.4 | `orderNumericID` = `locationOrderID` | **Done 2026-10-27** | One exported `schwab.LocationOrderID` (`pkg/schwaber/schwab/place_idempotent.go`); the orchestrator copy is deleted and its test moved next to it |
| 1.5 | `flattenOrders` and helpers | **Done** | SW exports `schwab.FlattenOrders` (`order_tree.go:24`); TO's copies and `orderSymbol`, `orderIsSell` etc. are gone |
| 1.6 | `windowForSignal` | **Open** | Same function in `stage_orders/window.go` (44 lines) and `manage_exits/window.go` (69 lines, plus `windowForTrade`). Comments say it was duplicated on purpose to avoid a dependency; BT has no `strategy.WindowFor` |
| 2.1 | Token sync | **Done** | TO `auth/token_sync.go` is 43 lines and calls `tokensync.SyncBeforeUse` / `Push`; no sync script left in `deploy_gae/` |
| 2.2 | Interactive OAuth | **Done** | SW `oauthcli.Run`; TO `auth/command.go` imports it; `IsPermanentAuthError` lives in SW `schwab/client.go:294` |
| 2.3 | Account / order / transaction ingest | **Mostly done** | SW `pkg/accountstore` (625 + 177 + 42 lines) and `listall` uses it; TO `account_state` no longer has `parseAndSave*` and delegates `Summary` to it. TO still keeps 386 lines of sync, progress events and table builders: probably orchestration, not verified line by line |
| 2.4 | Linked-account resolution | **Partly done** | `filterLinkedAccounts` is gone. `account_allowlist.ResolveFromDB` still maps stored rows to `trader.ResolvedAccount` in TO; no `ResolveFromLinked` exists in SW. That DB-row mapper may be legitimately TO's |
| 2.5 | Litestream `dbsync` | **Done (removed)** | `pkg/dbsync` is gone; the deploy is Turso only (commit 83ab70a) |
| 2.6 | Stack and allowlist parsing | **Done 2026-10-27** (see below) | BT exports `strategy.IsStack` / `ParseStack` and TO uses them (16 call sites). Left: TO `strategies.Contains` is a one-line `slices.Contains` wrapper; `strategies.Parse` and `account_allowlist.Parse` are the same split, trim and dedupe function |
| 2.7 | Market-DB path resolution | **Done** | `resolveMarketDB` is gone; TO calls `appenv.MarketDB()`. `BACKTEST_ROOT` only appears in a comment (`appconfig/schema.go:36`) |
| 2.8 | Signal scan wrapper | **Done** | BT has `runner.RunLiveScan` (`livescan.go:21`); TO's `livescan.go` is gone and `SignalScanResult` exists only in BT. TO's `scan_market.RunSignalScan` (285 lines) is a different function: it calls `runner.RunLiveScan` and adds bar sync and persistence. It shares a name, not logic |
| 2.9 | Trading calendar | **Done** | BT `pkg/calendar` (`LastCompletedSession`); `CalculateTradingDays` and the 3-holiday list are gone |
| 2.10 | SQLite open and pragmas | **Mostly done** | SW `dbutil.Open` holds the pragmas (WAL, synchronous, temp_store); TO `appconfig.OpenDB` is a one-line call to it. Left: BT `storage.OpenSQLite` has its own pragma block, and `park_sweep/db.go:117` adds a `busy_timeout` of its own. Both use the pure-Go `"sqlite"` driver, so the driver question in the old file is answered; the one exception is `cmd/markov_test`, which imports the cgo `mattn/go-sqlite3` |

Also closed since the first version: `SafetyGuard` is now used. `submit_orders.go:381` calls `tc.Safety.ValidateOrder` before placing an order (the old file listed it as a gap).

Counts: **9 done** (1.1, 1.3, 1.5, 2.1, 2.2, 2.5, 2.7, 2.8, 2.9), **4 mostly or partly done** (1.2, 2.3, 2.4, 2.6, 2.10 with small leftovers), **2 open** (1.4, 1.6).

## 2. What is left to remove

**Update after the merge (2026-10-27):** items 1.4, 1.6 and 2.6 are done in the merged tree. 2.6: the orchestrator's `strategies.Parse` and `account_allowlist.Parse` now share `pkg/orchestrator/csvlist.Unique`, `strategies.Contains` is gone in favour of `slices.Contains`, and backtest's two identical `splitList` copies (`strateval`, `pipeline`) became `cliutils.SplitList`. Still open: 1.2 (`.env` parsing) and 2.10 (pragma lists), which need a layer decision, plus the reviews of 2.3 and 2.4. The table below is the list as it stood before the merge.


Small items; the large ones (ingest, `dbsync`, secrets, token sync) are gone. Rough sizes are from reading the bodies, not a tool.

| Item | Where | Action | Size |
|---|---|---|---|
| 1.6 `windowForSignal` x2 | TO `stage_orders/window.go`, `manage_exits/window.go` | Add `strategy.WindowFor(strat, sig)` in BT (the logic is a strategy concept); both TO copies call it; `windowForTrade` stays as a short wrapper | about 40 lines |
| 1.4 order id from Location | TO `submit_orders.go:47` | Export `schwab.LocationOrderID` and delete the TO copy | 6 lines |
| 1.2 `.env` parse | BT `appenv.load()` | Call SW `config.ParseEnv`, or keep BT's and say why (BT is Calculation and must not import SW, see below) | 25 lines |
| 2.6 CSV parse | TO `strategies.Parse`, `account_allowlist.Parse` | One shared "split, trim, dedupe, error if empty" helper, and delete `strategies.Contains` (use `slices.Contains`) | about 25 lines |
| 2.10 pragma lists | SW `dbutil.Open`, BT `storage.OpenSQLite`, `park_sweep` | Decide one owner of the pragma list (see the layer note) | about 20 lines |
| 2.4 `ResolveFromDB` | TO `account_allowlist/resolve_db.go` | Review only; likely stays in TO | n/a |
| 2.3 `account_state` | TO | Review the remaining 386 lines for overlap with `accountstore` | unknown |

Realistic total: **about 120 to 150 lines of non-test code**, plus the tests that move with them. The earlier estimate (900 lines, 1,700 with `dbsync`) is spent.

**Layer note (cross-repo rule: Foundation and Calculation never import each other).** Items 1.2 and 2.10 pair a schwaber helper (Foundation) with a backtestgosqlite one (Calculation), so neither repo can import the other today. The rule says shared helpers go to the lowest layer that needs them, and if two peer layers need one it goes in Foundation. That is a reason to settle these two in the flattening (one `pkg/` tree with a shared foundation package) and not by adding a cross-repo import.

## 3. Same name, not a duplicate (leave alone)

- `scan_market.RunSignalScan` (TO) vs `runner.RunSignalScan` (BT): TO's wraps `runner.RunLiveScan` and adds persistence. Earlier drafts, including `FlattenAssessment.md`, called this a real overlap; it is not.
- `killswitch` and `orders` packages in TO and SW: SW's are CLI commands, TO's are state and display.
- `recordRun`, `Assess`: different jobs.
- `stage_orders`, `submit_orders`, `manage_exits`, `check_health`, `dashboard`, `send_email`, `appserve`, `runtimeenv`, `job_runs`: orchestration logic against TO's own tables.

## 4. Leads from the first version that are not confirmed

- **Decision-tree study code in BT.** The first version said near-identical 1,100-line decision-tree files exist. Today `pkg/study/mu_decision_tree.go` (1,194 lines) and `mara_decision_tree.go` (1,145 lines) exist, but they share only 1 function name (12 and 11 functions), and `strategy/ARCHIVE` and `strategy/failed_training` no longer exist. So these are not name-level copies. They may still be structurally similar; only a clone detector (`dupl`, `jscpd`) can say.
- **`heldTail`** in TO `modelstate/bars.go:109` and BT `dailybars/dailybars.go:143`: the same row-scan loop over different tables. A shared scan helper might remove about 15 lines; low value.
- **`APP_FOLDER` handling** is read in TO (`appconfig.go`, `deploy_strategy.go`, a dashboard page), SW (`oauthhttp/handlers.go`) and BT (`appenv`, `market_history/main.go`). `appenv` is the intended owner (rule: paths come from one environment package per repo, and one spelling across repos); the others should be checked.

## 5. How this was checked, and what was not

- Checked: each item's named symbols were searched in all three repos (non-vendor Go files). The bodies of 1.4, 1.6, 2.6 (`Parse`), 2.10 and the `.env` loop in BT were read.
- Not done: no clone detector was run; nothing was compiled for this scan (the three repos did build and test clean earlier today); test files were not audited; `scripts/` and `deploy_gae/` were not searched for duplicate logic; function bodies of TO `account_state.go` were not compared with `accountstore`.
- Numbers of lines are estimates from file and function boundaries.
