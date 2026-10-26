> **Updated 2026-10-13:** `backtestgo` is retired (`BacktestConsolidation.md`). Rows, models and market data now live in `backtestgosqlite/{refdata,data}`, and `scripts/export_strategies.sh` reads only `backtestgosqlite`. Read "backtestgo" below as `backtestgosqlite`; the three-piece split is now two pieces.

# Mixed backtest usage: what deploys strategies today

"Mixed" because deployment currently uses **three** backtest-side pieces at once, while [BacktestUnify.md](BacktestUnify.md) is still in progress:

| Piece | Role in deployment today |
|---|---|
| `backtestgo` | Where strategy rows are **researched and stored**. Its `refdata/strategies.db`, `data/markov_models.db` and `data/tree_models.db` are the source of truth for rows and trained models. |
| `backtestgosqlite` | The **code** `trade_orchestrator` imports (`replace github.com/darianmavgo/backtestgosqlite => ../backtestgosqlite`, `go.mod:53`). Its registry, row families, `refdb`, `livescan` and `strategy export` run in production. |
| `trade_orchestrator` | Holds the **allowlist** (what is greenlit) and the **embedded bundle** (`pkg/strategies/deploy/*.db`) that ships to App Engine. |

`backtestgo` does not import into the orchestrator (it is cgo/DuckDB). The orchestrator only reads its *data*, through `scripts/export_strategies.sh`.

Facts below were read from the code and databases on 2026-10-13.

## 1. Case study: `streak-shpu-down3`

Error the orchestrator raises:

```
strategy "streak-shpu-down3" is greenlit but not registered in backtestgosqlite
(a strategy that is a database row also needs its row in pkg/strategies/deploy/strategies.db:
regenerate it with `strategy export`)
```
(`trade_orchestrator/pkg/strategies/strategies.go:132`)

**Diagnosis**

| Check | Result |
|---|---|
| Row in `backtestgo/refdata/strategies.db` `streak_strategy` | Yes. SHPU watched and traded, `direction=drop`, `signal_days=3`, `hold_days=5`, `take_profit_pct=0.05`, `stop_loss_pct=0`, `allocation_pct=0.25`, regime `All Regimes`. |
| Row in `backtestgosqlite/refdata/strategies.db` | Yes (same row). |
| Row in `trade_orchestrator/pkg/strategies/deploy/strategies.db` | **No.** The bundle holds only: `streak-voo-buy-tecl`, `markov_model_amd`, `markov_model_lite`, `markov_model_mull`, `rotation-2x-sector-pairs-daily-limit95-tp3-sl5-hold2-pos20` (+ one `symbol_lists` row). |
| In the seeded allowlist in `deploy_gae/config.sh:86` | **No.** That seed is `streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull,tsll-daily-one-share,rotation-2x-...-pos20`. The live list is in the GCS config object (`gs://<bucket>/config/env`), which I did not read. The error means it is in the live list or in a `-strategy`/`STRATEGY_ALLOWLIST` override. |

Not a registry bug: `streak_strategy` is a `RowFamily`, so `strategy.Get` builds the row on demand from whichever `strategies.db` `ensureRegistered` placed. On App Engine that is always the bundled copy, which has no SHPU row, so `Get` returns `ok=false`.

**Fix** (this is the only change needed; I have not run it)

```sh
cd trade_orchestrator
scripts/export_strategies.sh "streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull,rotation-2x-sector-pairs-daily-limit95-tp3-sl5-hold2-pos20,streak-shpu-down3"
go mod vendor                                   # BT is vendored; stale copies build silently
go build ./... && go vet ./... && go test ./...
deploy_gae/config.sh set STRATEGY_ALLOWLIST="<same value, tsll-daily-one-share included>"
deploy_gae/deploy.sh
```

Notes:
- With no argument the script uses the allowlist seeded in `config.sh`, which lacks SHPU. Either pass the full list as above, or add `streak-shpu-down3` to the seed line first.
- `tsll-daily-one-share` is not a row; the script prints `not a row, left out of the bundle` and skips it. That is expected.
- `strategy export` **replaces** `strategies.db` (it removes the destination first), so the list must contain every row you still trade, not only the new one.
- It also rewrites `markov_models.db` and `tree_models.db`. A streak row needs no model, so SHPU adds nothing there.
- After a deploy, check with `orchestrator strategies` (greenlit marks) and `orchestrator strategy_allowlist show`. `strategy_allowlist set/add` itself runs `ResolveGreenlit` first (`pkg/strategy_allowlist/command.go:127`) and refuses an entry that does not resolve. A locally run command with the full `refdata/strategies.db` present can pass while App Engine fails, because `placeBundled` skips the write when a file exists locally. Test the bundle itself, not the dev machine's copy.
- SHPU also needs hourly bars in the market DB the scan reads (`MARKET_DB`, rebuilt from Turso `market_bars` by the scan). `pkg/scan_market` has the details; I did not verify that SHPU bars are in Turso `market_bars`.

## 2. How a strategy gets from research to a live order

```
backtestgo                              backtestgosqlite                 trade_orchestrator
-----------                             ----------------                 ------------------
backtest / gridsearch / train           (code only)                      STRATEGY_ALLOWLIST (GCS config/env)
  -> refdata/strategies.db rows                                              |
  -> data/markov_models.db                                                   v
  -> data/tree_models.db         scripts/export_strategies.sh  ->  pkg/strategies/deploy/{strategies,markov_models,tree_models}.db
                                 (runs `strategy export`)              |  go:embed (bundle.go)
                                                                       v
                                                         strategies.ResolveGreenlit -> strategy.Get -> scan / stage / submit
```

1. **Research (`backtestgo`).** `bin/backtest`, `bin/gridsearch`, `bin/train`. Rows live in `refdata/strategies.db`; `+` joins ids into a shared-account stack (`-id "markov_model_mull+streak-shpu-down3"`, `backtestgo/README.md:66`).
2. **Export (`scripts/export_strategies.sh`).** Reads the allowlist, keeps only entries whose every member is a row in one of the five tables, then runs `go run ./cmd/strategy export` in `backtestgosqlite` with `APP_FOLDER=../backtestgo`. That reads backtestgo's `strategies.db` and models, and writes the bundle into `pkg/strategies/deploy/`.
3. **Embed (`pkg/strategies/bundle.go`).** Three `//go:embed` blobs. `ensureRegistered` writes them to `appenv.RefDB()`, `MarkovDB()`, `TreeDB()` (always on App Engine, because `/tmp` is empty or old; elsewhere only when no file exists). Then `stratreg.RegisterFamiliesAt(path)`.
4. **Greenlight (`pkg/strategies/strategies.go`).** `Parse`, `ResolveRequest` (an explicit `-strategy` must already be in the allowlist), `ResolveGreenlit` (expands `a+b+c` stacks via `strategy.ParseStack`, calls `strategy.Get`, logs one row per strategy to the orchestrator's `strategies` table).
5. **Run.** `ResolveGreenlit` is called by `scan_market`/`stage_orders` (`stage_orders/command.go:52`), `manage_exits`, `trigger_limits`, `check_health`, the dashboard stage-orders preview and `appserve/cron.go:198,535`. A failure here stops those jobs, which is why `strategy_allowlist` validates before saving.

## 3. Features in use

**backtestgosqlite packages the orchestrator reaches**
- `pkg/strategy` (`Strategy` interface, `Get`, `List`, `IsStack`, `ParseStack`, `Preparer`).
- `pkg/stratreg` (`RegisterFamiliesAt`). `pkg/appenv` (`RefDB`, `MarkovDB`, `TreeDB`; `APP_FOLDER`).
- Row families: `streak_strategy`, `hold_strategy`, `tree_strategy`, `markov_strategy`, `rotation_strategy`. Each is one `Family`, rows built on demand.
- `pkg/refdb` (`Export`, `ExportModels`). `pkg/strategy_cmd` (`strategy export|derive|coverage`).
- Live signal path: `livescan` and `runner.RunLiveScan` with SQL pipelines under `sql/strategies/<family>/`, run through `storage.ExecuteSQLFile`. Pure-Go SQLite, no cgo.
- `Preparer.Prepare` after the bar refresh (markov trains its signal symbol through the latest bar).

**Not used in deployment:** simulator, gridsearch, walk-forward, stackopt, backtest, pipeline (these are the Phase 2 removal targets in BacktestUnify.md), and everything DuckDB.

## 4. Tables

**Source (`backtestgo/refdata/strategies.db`, ~18.3k rows)**: `streak_strategy` 7,701; `markov_strategy` 7,750; `tree_strategy` 1,631; `hold_strategy` 1,233; `rotation_strategy` 17; plus `symbol_lists`, `stack`, `settings`, `strategy_family_param`, `strategy_last_backtest`.
(`backtestgosqlite/refdata/strategies.db` has the same counts except `rotation_strategy`, which has 9. Export from backtestgo, as the script does.)

**Bundle (`pkg/strategies/deploy/strategies.db`)**: the five row tables (schemas as above, rows only for greenlit ids) and `symbol_lists` (only lists a bundled row names, via `refdb_export` SQL). It has no `settings`, `stack`, or `strategy_family_param`.

**Models**: `markov_models.db` (`markov_model_meta`, `markov_prediction`) and `tree_models.db` (`tree_model_meta`, `tree_node`), restricted to the signal symbols of exported rows. The full `markov_models.db` is about 477 MB, so it cannot be embedded whole.

**Orchestrator tables**: `strategies` (resolve log: `strategy_id`, `config_json`, `resolved_at`), `scan_market_signal`, `scan_market_buy_signal` (per-signal `stack_id`, `strategy_id`, `buy_limit`, `take_profit`, `stop_loss`, `allocation_pct`, `hold_days`, `status`), plus staged-orders and job-run tables (`pkg/schemas`, `pkg/job_runs`).

**Market data**: `market_history.db` (`MARKET_DB`; locally `backtestgo/data/market_history_hourly.db`, table `backtest_start`, key `(symbol, Date)`), rebuilt from Turso `market_bars` on App Engine.

## 5. Code, by file

| File | Why it matters |
|---|---|
| `trade_orchestrator/pkg/strategies/strategies.go` | Allowlist parsing, `ResolveGreenlit`, the error quoted above |
| `trade_orchestrator/pkg/strategies/bundle.go` | Embeds and places the three bundle DBs |
| `trade_orchestrator/pkg/strategies/stack.go`, `command.go` | `MemberGreenlit`, `orchestrator strategies` listing |
| `trade_orchestrator/pkg/strategy_allowlist/command.go` | `show/set/add/remove` on `config/env` in GCS, validates first, keeps `config/env.prev` |
| `trade_orchestrator/scripts/export_strategies.sh` | Rebuilds the bundle from backtestgo data |
| `trade_orchestrator/deploy_gae/config.sh:86` | Seeded allowlist |
| `backtestgosqlite/pkg/strategy_cmd/main.go` (`export`) | CLI behind the script |
| `backtestgosqlite/pkg/refdb/export.go`, `export_models.go` | Row and model copy; errors on any id that is in no row table |
| `backtestgosqlite/pkg/stratreg/stratreg.go` | `RegisterFamiliesAt` |

## 6. Gotchas and gaps

- **Three places must agree**: the live allowlist (GCS), the bundle contents, and `config.sh`'s seed. `strategy_allowlist` only protects the first from the second when the bundle is current.
- **Silent drift**: `bundle.go` and `CLAUDE.md` still say the full DB is "about 65,000 rows". The actual count is about 18.3k (BacktestUnify.md Phase 3 already lists this).
- **Rows exist in two repos** (`backtestgo` and `backtestgosqlite` `refdata`) and they differ (rotation 17 vs 9). The script uses backtestgo; running `strategy export` by hand defaults to `backtestgosqlite` unless `APP_FOLDER` points at backtestgo.
- **Not checked here**: the live GCS allowlist, whether SHPU bars are in the deployed market DB, and whether `backtestgo`'s `+` stacks and the orchestrator's `ParseStack` agree on `streak-shpu-down3` as a stack member. A single-id check is the quickest test: `orchestrator strategies` after the fix.
- **Unify plan impact**: when `backtestgo` replaces both repos, `export` and `refdb` must move with the slimmed execution side (Phase 2), and the script's `bt` and `data_root` split goes away.
