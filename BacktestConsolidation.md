# Plan: keep backtestgosqlite, move backtestgo's useful parts into it, retire backtestgo

Supersedes the direction in `BacktestUnify.md` (there `backtestgo` replaced both repos). Now
`backtestgosqlite` (BT) is the survivor: it is the repo `trade_orchestrator` imports and it runs on
App Engine. `backtestgo` has already given BT its best part, the hourly simulator.

Facts below were read on 2026-10-13.

## Where things stand

| | backtestgosqlite (BT) | backtestgo |
|---|---|---|
| Role | Live execution side, research CLI, 48 packages | Hourly backtester rewrite, 17 packages, pure-Go SQLite (DuckDB was abandoned: no `go-duckdb` in `go.mod`, SQL is `.sql`) |
| Tests | Pass except 2 in `pkg/refdb` (already failing) | `go test ./...` passes |
| Simulator | Hourly (`pkg/hoursim` -> `backtestgo/pkg/sim`), legacy daily for unsupported settings and parked cash | `pkg/sim` |
| Data on disk | **none**: no `data/` folder at all | `data/market_history_hourly.db` 10 GB (9,566 symbols), `markov_models.db` 457 MB, `tree_models.db`, `sessions/` 1.3 GB, `data/reports` **47 GB**, `refdata/strategies.db`, `refdata/universe.db` |
| Strategy rows | rotation 9 | rotation **17** (markov 7,750 and streak 7,701 are the same) |
| README | current | stale: still describes DuckDB, `.duckql`, `bin/migrate` (the command is gone) |

Two things tie BT to `backtestgo` today:
1. **Code:** BT and `trade_orchestrator` both `replace backtestgo => ../backtestgo` for `pkg/sim`.
2. **Data and scripts:** `trade_orchestrator/scripts/export_strategies.sh` reads rows and models from `../backtestgo` (`data_root`), and BT's real-data tests (`realbars`) skip because BT has no `data/`.

Disk: 52 GB free of 460 GB. The 47 GB of old reports is the room to make.

## Phase 0: freeze

1. Commit BT (36 uncommitted files) and `trade_orchestrator` (36) on their own branches so every later step is revertible.
2. `git tag pre-consolidation` in `backtestgo` and in BT.
3. Gate: `go test ./pkg/... ./cmd/...` green in BT and `trade_orchestrator` (apart from the 2 known refdb tests).

## Phase 1: cut the code dependency

1. Copy `backtestgo/pkg/sim` (3 files, 1,339 lines with tests; it imports only `math` and `sort`) into BT as `pkg/sim`.
2. Point `pkg/hoursim` at `github.com/darianmavgo/backtestgosqlite/pkg/sim`.
3. Delete the `require`/`replace backtestgo` lines from BT and `trade_orchestrator` `go.mod`; `go mod tidy`; re-run `go mod vendor` in the orchestrator.
4. Gate: `rg "backtestgo\b" --glob go.mod` finds nothing; BT and orchestrator build and test green; `hoursim` parity test gives the same numbers as before (`docs/HourlyEngineParity.md`).

## Phase 2: move the data (the real blocker)

Copy, do not move, until Phase 5.

1. `data/market_history_hourly.db` -> `backtestgosqlite/data/`. BT's tests (`realbars`) then stop skipping: that alone is a large gain in coverage.
2. `data/markov_models.db`, `data/tree_models.db` -> `backtestgosqlite/data/`. Check each model keeps `trained_through` (BT's `ExtendMarkov` and the bundle export rely on it).
3. `refdata/universe.db` -> `backtestgosqlite/refdata/`.
4. **Strategy rows:** `backtestgo` has 17 rotation rows, BT has 9. Merge with SQL (`INSERT ... WHERE id NOT IN`), then compare every table's row counts and a checksum of the id column.
5. Settings: compare `settings` tables and keep the newer value per key.
6. Repoint `trade_orchestrator/scripts/export_strategies.sh` to BT (`bt` and `data_root` become one path).
7. `data/sessions/*.duckdb` and `data/reports` (47 GB of DuckDB-era runs): archive the few runs worth keeping (`latest.db`, anything cited in docs), then delete the rest. Do not copy them.
8. Gate: `orchestrator` bundle export gives the same `strategies.db` rows as before; `scripts/export_strategies.sh` runs from BT only; `ls backtestgo/data` is no longer needed by any script (`rg "backtestgo/(data|refdata)"` finds nothing).

## Phase 3: port the useful features

In value order. "Port" means re-implement in BT's layout (thin `cmd`, logic in `pkg`, signals in SQL), not copy the package.

| # | From backtestgo | Into BT | Why it matters | Notes |
|---|---|---|---|---|
| 1 | `sql/stages/session_build/03_quality.sql`, `price_jump` (any close-to-close move beyond 1.8x between hourly bars) | `sessions` + leaderboard | Unadjusted splits make fake wins; a suspect strategy should not rank unless `-include-suspect` | Small. Do first |
| 2 | `sql/stages/params/*.sql`: one query per family saying **why a row cannot run** (stop at or above the fill, unknown symbol...) and `backtest list` | `stratlist` / `runner` | Rows fail silently today | Reuse the SQL; add the CLI listing |
| 3 | `pkg/check` (6 files, 2,190 lines): checkpoints that verify a finished run (exit hour, one entry per signal, entry gate, model cutoff, model on disk, signal arithmetic, tree shape...) | `pkg/check` beside `check_overfit` | Proves a run is honest after the fact; the exit-hour and one-entry checks guard the new engine | Needs the kept calc tables from #4 |
| 4 | `pkg/results` / `pipeline` "keep every calculated table" (accounting) | `storage` + `runner` | A trade can be audited to the signal row that made it. Fits the new rule: one fresh scratch DB per strategy, kept per run instead of deleted | Add `-keep-calc` default for ranked runs |
| 5 | Hourly `gridsearch` (`same_hour_trades` guard, carry-over verdict) | BT `gridsearch` | BT's still plays the daily engine | Switch BT's sweep to `hoursim` instead of porting theirs; take the two checks |
| 6 | Residual-cash book (a park) | `pkg/sim` | Last reason `pkg/simulator` and `park_sweep` still exist | New code in `sim`; the daily engine's `SharedAccountSimulator` park logic is the specification |
| 7 | `pkg/market` plan/gap report (missing hourly bars; `migrate gaps` is gone) | `market_history` | Shows holes in the 9,566-symbol history | Compare with BT's downloader first; port only what is missing |
| 8 | `pkg/train` markov/tree | none | BT's `train` already writes `trained_through` and extends live | Run `check` (#3) against both repos' models to confirm they agree, then drop |

Not worth porting: `pkg/workspace`, `pkg/store`, `pkg/testdb`, `pkg/data`, `pkg/refdata` (BT has `storage`, `refdb`, `appenv` for each), and the DuckDB-era README text.

Gate per row: ported code has a test on real bars (now possible after Phase 2), and the old package's tests are the specification.

## Phase 4: retire the daily simulator in BT

After #5 and #6: delete `pkg/simulator`, then the legacy branches in `runner/engine.go`, `walk_forward`, and `park_sweep`. Mark every run whose `runs.engine` is NULL or `daily` as suspect (a column in the runs table and a marker file), and exclude them from rankings.

## Phase 5: prepare backtestgo for removal

1. **Freeze:** replace the README with a banner ("superseded by backtestgosqlite on <date>; read-only") and tag `final`.
2. **Archive:** `git bundle create backtestgo.bundle --all`; copy the bundle plus the few kept reports to external storage. Confirm the bundle clones.
3. **Prove independence:**
   - `rg "backtestgo\b" -g '!backtestgo/**' -g '!**/vendor/**'` finds only docs;
   - every `go.mod` is free of it;
   - `scripts/export_strategies.sh`, `deploy_gae/deploy.sh`, `config.sh` and CI do not read it;
   - rename the folder to `backtestgo.removed` and run all tests in BT and the orchestrator.
4. **Soak:** two weeks with the folder renamed, including one App Engine deploy and a live scan, and a full BT research run.
5. **Delete** `backtestgo.removed` (and `backtestgoduckdb`, separately: nothing imports it).
6. **Docs:** update `BacktestUnify.md` (mark superseded), `MixedBacktestUsage.md` (the three-piece deploy becomes two), the READMEs and `CLAUDE.md` files, and the stale "about 65,000 rows" claims (actual about 18.3k).

## Risks

- **Strategy rows diverge** (rotation 17 vs 9): merge by SQL and diff, never overwrite.
- **Model files differ in training cutoff:** the live Markov scan depends on `trained_through`; verify before the bundle is exported again.
- **`sim` shorts apply long-side stop and target logic:** fix or forbid before any short strategy moves to the hourly engine.
- **The park** is the largest port and the reason the daily engine lives on.
- **App Engine stays cgo-free:** `sim` is dependency-free, so this holds; check again after Phase 1's `go mod tidy`.
- **Disk:** copy the 10 GB market DB only after deleting old reports, or free space first (52 GB free now).

## Status (2026-10-13, end of day)

Done and tested, on branch `consolidation` in `backtestgosqlite` and `trade_orchestrator` (tags `pre-consolidation`):

| Phase | Result |
|---|---|
| 0 Freeze | Tags `pre-consolidation` in all three repos; orchestrator work committed |
| 1 Code dependency | `pkg/sim` lives in `backtestgosqlite`; no `go.mod` names `backtestgo`; orchestrator re-vendored; parity numbers unchanged |
| 2 Data | Hourly market DB converted to BT's schema (84,136,555 rows, 9,566 symbols, integrity ok) in `backtestgosqlite/data/`, plus 10,232,228 derived session bars (daily rows); 5,657 Markov models and the tree models copied; `universe.db` copied; strategy rows merged (rotation 9 -> 17, symbol list 7 -> 8; counts match everywhere else); `backtestgo`'s settings table preserved in `backtestgosqlite/docs/BacktestGoSettings.md`; `scripts/export_strategies.sh` reads BT only and rebuilds the same bundle rows. The 47 GB of old reports was **not** touched |
| 3 Features | Ported: split detection (`sessions.SplitJumps`, `runs.suspect`, `scoreboard -include-suspect`); rows that can never fill fail with a reason (`runner.RowProblem`); fresh calc DB per run (`freshCalcFile`, so `-keep-calc` is safe); `pkg/check` + `cmd/check` (4 run checks, 3 model checks, passes on all 5,657 copied models); gridsearch on the hourly engine; the parked-cash book in `sim` (`sim.Park`), used by stacks and `park_sweep`. Not ported, with reasons: the `migrate gaps` report (BT's `strategy coverage` does it), `pkg/train` (BT's `train markov` reproduces the copied models exactly: AMD, MULL and VOO, 2,368 prediction rows, 0 differences; `check -markov` passes on all 5,657), workspace/store/testdb/data/refdata (BT has equivalents) |
| Gate | With the data in place, 45 BT packages pass (9 skips left, down from most of the real-data tests); the two long-standing `refdb` failures are fixed; `price_action_reclaim` and the stale-model test were re-based on the shorter history (2021-01-04 start) |
| 4 Daily simulator | **Done 2026-10-13:** `dividend-margin-buy-hold` deleted and `pkg/simulator` removed. Margin, ATR stops, Kelly sizing and dividends are now refused with their name (`hoursim.Supported`); `idle` stats moved to `analytics`. Runs with no recorded engine are marked suspect and redone by bulk commands. The dividend/total-return plumbing in `runner` is dead code (no row uses it) and can be deleted next |
| 5 Prepare backtestgo | README banner and tag `frozen`; `git bundle` verified (clones) in `~/Documents/trade_well_archive/`; `rg backtestgo` finds no code, script or `go.mod` outside the repo. **Not done on purpose:** rename, soak and delete |

## What is left for you to decide

1. **Delete `backtestgo/data/reports` (47 GB)** after choosing which runs to keep (`latest.db`, `market_gaps.db`, anything cited in docs). Also `data/sessions` (1.3 GB, DuckDB files) and the duplicate 10 GB market DB, once you trust the converted copy.
2. **Rename `backtestgo` to `backtestgo.removed`, soak two weeks** (one App Engine deploy, a live scan, one full research run), then delete it. `backtestgoduckdb` can go on the same schedule.
4. **Deploy** the orchestrator branch (direction fix, scratch-DB fix, hourly replay) and run the 18:00 scan; nothing here has been deployed.

## Order of work

Phase 0 -> 1 -> 2 are mechanical and unblock everything (about a day). Phase 3 items 1, 2 and 4 are small and make the next backtests more trustworthy; 3 depends on 4; 6 is the long pole. Phase 5 can only start when the table in Phase 3 is done and Phase 4 has removed the last reader.
