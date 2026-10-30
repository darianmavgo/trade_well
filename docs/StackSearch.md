# StackSearch: finding a set of strategies with high win rate and low entry collision

Written 2026-10-30 from one working session. It records the goal, what was built, what was run, what was found, what is still wrong with the answer, and how to repeat it. Commands are real and were run unless a line says otherwise.

## 1. The goal

Find 10 strategies that

- win more than 75% of their trades, **both in-sample and in the held-out last 12 months**, and
- enter on different days from each other (low entry collision), so that a shared account is rarely asked to open several positions on one day.

"Stack" is the repo's word for a set of strategies run on one shared cash ledger. "Book" is a better name (the docs already say "stack books" and "residual book"), but renaming it is a coordinated change under rule 3 and has not been done. This document says *set* for the thing being chosen and *stack* for the existing commands.

## 2. Result in one paragraph

Ten strategies were found that pass 75% in both windows with an average pair overlap of 7.3% (worst 19.4%): 84.4% wins over 699 trades. It is a **provisional** answer. The held-out samples are 11 to 24 trades each, the held-out year was used to choose, and about 94 of roughly 18,000 candidates passed, so some are luck. Several are micro-caps. See section 8 before trading any of it.

| # | strategy | family | IS win % (n) | held-out win % (n) |
|---|---|---|---|---|
| 1 | streak-telo-down3 | streak | 86.5% (37) | 85.7% (14) |
| 2 | hmm-calm-psfd-g | hmm | 84.8% (33) | 90.9% (11) |
| 3 | hmm-calm-qtap-g | hmm | 84.3% (51) | 90.9% (11) |
| 4 | hmm-calm-snpg-g | hmm | 88.9% (54) | 83.3% (18) |
| 5 | streak-umac-down3 | streak | 92.1% (38) | 83.3% (24) |
| 6 | hmm-calm-mayt-g | hmm | 82.9% (35) | 91.7% (12) |
| 7 | hmm-calm-papr-g | hmm | 81.8% (55) | 92.3% (13) |
| 8 | hmm-stressed-svix-g | hmm | 81.0% (105) | 86.4% (22) |
| 9 | streak-nwtg-down3 | streak | 80.4% (51) | 87.5% (16) |
| 10 | streak-tkno-down3 | streak | 80.3% (76) | 87.0% (23) |

Set analysis of the ten (signals, trades taken on their own): 546 signal days, 129 of them (23.6%) with two or more strategies signalling, at most 5 on one day. Win rate 84.4% (590 of 699), average return 1.43%, worst single trade -82.46% (`streak-nwtg-down3`).

## 3. What was learned

### 3.1 The structural problem with the hmm family

Every `hmm_strategy` row reads the **same QQQ regime series**. A `calm_filter` row enters on QQQ's calm days whatever symbol it trades, so every such row has about the same ~955 signal days. Changing `trade_symbol` does not diversify entry days. Only the rule (`calm_filter`, `calm_down`, `normal_filter`, `stress_filter`) and `hold_days` separate them. Low collision cannot come from the symbol column.

The one-model design is deliberate (the owner does not trust per-symbol regimes), so collision had to come from other families.

### 3.2 Win rate is hard to get from a uniform exit

The 7,747 migrated `calm_filter` rows used one exit (hold 3 days, no take-profit, 8% stop). In run 54 (7,477 rows with results): 19 reached 70% with at least 30 trades and 120 reached 65%. None reached 75% in both windows. The three other regimes (3,000 more rows) added no qualifier. Only `hmm-stressed-svix` (an existing row, stress_filter, hold 6, TP 2%, stop 10%) passed.

### 3.3 What produced the answer

- **Gridsearch** on the hmm family lifted the count from 1 to 94 candidates across families, by tuning hold, take-profit and stop per row on the in-sample window only.
- **Streak** rows supply naturally spread-out entry days, because each trades its own symbol's decline streak, not a shared market regime. Four of the ten picks are streak rows, and they overlap the hmm picks very little.
- **Tree** (run 55) and **hold** and **price_action_reclaim** produced no candidate that passed both windows.

### 3.4 The hmm winners are mostly low-volatility ETFs

Most hmm candidates (the top 300 by in-sample win rate) are defined-outcome "buffer" ETFs with names like `pbmy`, `sepp`, `junp`. They win often because they rarely move, and they earn only 2 to 4% a year on the allocated capital. They are a good source of win rate and a poor source of return.

### 3.5 Warrants

The universe types warrants inconsistently: 173 rows are `asset_type = 'WARRANT'`, but another 325 are typed `CS` with "Warrant" in the name. `streak-tlsiw-down3` (TriSalus Life Sciences warrant, entry prices $0.05 to over $1) had the best in-sample CAGR (34.6%) and was the least trustworthy number in the set. All 498 were removed (section 6).

`TELO`, `UMAC` and `NWTG` are not warrants. They are micro-caps (entry prices roughly $1.3 to $6, and NWTG swung from $1.45 to over $100, which looks like a reverse split but is unconfirmed). Liquidity has not been checked for any pick.

### 3.6 Speed and disk

- A full family backtest of ~7,750 strategies, in-sample plus held-out, took roughly two hours the first time (about 70 "Completed" lines a minute, in-sample and held-out counted separately).
- Resuming a run with `-run-id N` skips strategies that already have results and ran about 200 strategies a minute for the new rows. 3,000 new rows took roughly 15 minutes.
- The resume has a start-up scan of existing results that takes a minute or two on a 2 GB family database.
- Run 54 is 3 to 4 GB. The volume had 59 GiB free at 87% full.

## 4. What was built

All new work reads results and writes into the run folder. Nothing writes `data/market_history_hourly.db`.

### 4.1 `backtest stack` set analysis

`backtest stack` (also accepted as `backtest stack-eval`) now ends with a set analysis for the primary and its overlays.

- SQL: `sql/stages/set_analysis/` (8 stages: entry days, per-strategy days, pairs, shared days, overlap, day load, set summary, trade stats).
- Go: `pkg/backtest/runner/set_analysis.go` (`AnalyzeSet`, `PrintSetReport`), wired into `stack_eval.go`.
- It reports collision days (days with two or more strategies signalling), pair overlap (shared days over the smaller strategy's days) and Jaccard, and the shared-account win rate, average and worst return.
- Not implemented: return correlation, because per-strategy equity series are not kept.

Example: the stack `hmm-calm-qqq` with `hmm-calm-tqqq`, `hmm-calm-soxl` and `hmm-normal-tecl` showed `soxl` and `tqqq` at 99.7% entry-day overlap with the primary, which is the collision problem in 3.1 made visible.

### 4.2 `backtest pick`

Chooses the set. It reads finished result databases, so it starts no run folder and loads no bars.

```
./bin/backtest pick -family hmm@54,hold@44,streak@44,price_action_reclaim@49,tree@55 -exclude streak-tlsiw-down3
```

- `-family a,b@N` takes several families. `family@N` reads that family from run N (the families may come from different runs as long as the in-sample and held-out windows match; they did here). Without `@N`, `-run-id` or the latest run is used.
- Flags: `-pick 10`, `-min-win-rate 0.75`, `-min-trades 30` (in-sample), `-min-oos-trades 8` (held-out), `-max-overlap 0.30`, `-top-candidates 200` (the overlap join is quadratic), `-exclude id,id`.
- It writes `pick_<family>.db` (or `pick_multi.db`) in the first family's run folder: every slice table, so any stage can be queried.
- Selection is greedy: gate on both windows, rank by the lower of the two win rates, then add the best remaining candidate whose largest overlap with the ones already chosen is at most `-max-overlap`.
- SQL stages, run in this order by `runner.PickSet`: `pick_init`, `pick_gate` (once per family), `pick_top`, `pick_source_days` (once per family), `pick_overlap`, then `pick_next` once per round.
- Go: `pkg/backtest/runner/pick_set.go`, `pkg/backtest/backtest/pick.go`. Tests: `pick_set_test.go` (real temp SQLite, two families, overlap skipping).
- `Run` dispatches `pick` before `prepareRun`. An earlier version ran it after, when `OutDir` had already become the run folder, and failed with "no run 54 in .../54 (latest is 0)".

### 4.3 The markov to hmm migration

The owner does not trust the markov family (discrete bull/bear/sideways states, one model per symbol, unclear live updating).

- `sql/seed/hmm_from_markov.sql` copies all markov rows into `markov_strategy_retired` and inserts one `hmm_strategy` row per distinct trade symbol (`hmm-calm-<symbol>`, `hmm_symbol = QQQ`, rule `calm_filter`, hold 3, TP 0, stop 8%, allocation, cash yield and slippage carried over, `source_strategy` = the markov id).
- 7,749 markov rows had 7,747 distinct trade symbols (SPXL and UPRO were duplicated). The row with the smallest id wins.
- Three pre-existing ids (`hmm-calm-qqq`, `-soxl`, `-tqqq`) kept their older exits because the insert is `INSERT OR IGNORE`.
- Test: `pkg/backtest/refdb/hmm_from_markov_test.go`.
- `markov_strategy` itself was left in place. Removing the markov package, `train markov`, `data/markov_models.db`, the 500 MB bundle embed and the orchestrator markov code is a separate cleanup that has not been done (it is a rule-3 rename).

### 4.4 More hmm rows

- `sql/seed/hmm_other_regimes.sql`: `normal_filter`, `stress_filter` and `calm_down` rows for the 1,000 symbols with the most calm-filter trades in a given result database (3,000 rows). Same exits as the calm rows.
- `sql/seed/hmm_tuned.sql`: one `-g` row per swept strategy, using the best in-sample gridsearch result with at least 30 trades and a 0.75 win rate. 215 rows were created (213 calm_filter, 2 stress_filter). `gridsearch promote` only writes streak rows, which is why this seed exists.

### 4.5 `strateval` removed

`pkg/backtest/strateval` and its pipeline step were deleted as not useful. The `stack-candidates` skill and the docs were updated. Historical mentions remain in `docs/LearningLog.md`, `FlattenAssessment.md`, `KillDupeCode.md`, `ArchitectureViolation.md`, `MakeMoneyNextDevCycle.html` and `docs/archive`.

## 5. How to repeat it

```
# 1. Seed the hmm rows (back up refdata/strategies.db first)
sqlite3 refdata/strategies.db < sql/seed/hmm_from_markov.sql

# 2. Backtest the family: in-sample, then the held-out year, into a new run folder
./bin/backtest -family hmm -auto-download=false

# 3. Add the other regimes for the symbols with the most history, backtest only the new rows
sqlite3 refdata/strategies.db "ATTACH 'data/reports/<N>/hmm.db' AS calm;" ".read sql/seed/hmm_other_regimes.sql"
./bin/backtest -family hmm -run-id <N> -auto-download=false

# 4. Tune the best in-sample rows (in-sample only: the sweep stops before the held-out months)
./bin/gridsearch -strategy <id,id,...> -no-html -run-id <N>
sqlite3 refdata/strategies.db "ATTACH 'data/reports/<N>/gridsearch.db' AS grid;" ".read sql/seed/hmm_tuned.sql"
./bin/backtest -family hmm -run-id <N> -auto-download=false

# 5. Backtest the other families, then pick across all of them
./bin/backtest -family tree -auto-download=false
./bin/backtest pick -family hmm@<N>,streak@<M>,hold@<M>,tree@<K>
```

The candidate list for step 4 was the top 300 hmm strategies by in-sample win rate with at least 30 trades, read from the in-sample result database only:

```
sqlite3 data/reports/<N>/hmm.db "select strategy_id from performance_summary where total_trades>=30 and strategy_id not like '%-g' order by win_rate desc, total_trades desc limit 300"
```

Runs used in this session: hmm in run 54 (`data/reports/54/hmm.db` and `oos/hmm.db`), hold and streak in run 44, price_action_reclaim in run 49, tree in run 55. All share the in-sample window 2021-01-04 to 2025-10-10 and the held-out window 2025-10-13 to 2026-10-23.

## 6. Data changes made to real files

`refdata/strategies.db` and `refdata/universe.db` were written on 2026-10-30. Each step has a backup in `data/reports/`:

| step | backup |
|---|---|
| markov to hmm migration | `strategies_before_hmm_migration.db` |
| other regimes (+3,000 rows) | `strategies_before_hmm_regimes.db` |
| tuned rows (+215 rows) | `strategies_before_hmm_tuned.db` |
| warrant removal | `strategies_before_warrant_removal.db`, `universe_before_warrant_removal.db` |

Warrant removal (498 symbols: `asset_type = 'WARRANT'`, or any non-UNIT row whose name contains "warrant"): `universe` -498, `avg_volume` -752, `avgvol_window_bars` -2,741, `streak_strategy` -13, `markov_strategy` -14, `tree_strategy` -7, `hold_strategy` -1, `hmm_strategy` -20. `markov_strategy_retired` was left as an archive and still holds its 14. Units and rights were kept.

Current row counts: `hmm_strategy` 10,947, `streak_strategy` 7,688, `markov_strategy` 7,735, `tree_strategy` 1,624, `hold_strategy` 1,251.

The saved result databases (runs 44, 49, 54, 55) still contain the removed warrants' backtests. That is why `pick` has `-exclude`.

## 7. Gotchas found

- `backtest`'s `Run` replaces `conf.OutDir` (the reports root) with the run folder. Any subcommand that reads a run by number must be dispatched before `prepareRun`, as `pick` is.
- `gridsearch promote` writes streak rows only. For other families, write a seed (see `hmm_tuned.sql`).
- The gridsearch grid for an hmm row is the generic exit grid (432 permutations: hold 2 to 15, take-profit 0 to 10%, stop 0 to 8%). It never varies the rule or the regime. Every hmm row also logs a warning that the QQQ model runs to 2026-10-22 while the market data runs to 2026-10-23.
- The market database would not open from the `sqlite3` CLI with `-readonly` or `mode=ro` in this session (error 14). The result databases do. Liquidity and price checks therefore used the trade logs (`entry_price`) in the result databases, not the bars.
- `pgrep -f` in a waiting loop matches its own shell, so a loop such as `until ! pgrep -f "backtest -family hmm"` never ends. Match a narrower string such as `bin/backtest -family hmm`.
- No `;` inside a SQL comment in a stage file: `storage.RunStage` splits statements on `;`.
- `sql/seed` files can attach other databases (`calm`, `grid`) with the `sqlite3` CLI's `ATTACH` before `.read`; the seed files assume those names.

## 8. Why the answer is provisional

1. **The hmm model overlaps the held-out year.** `data/hmm_models.db` was trained through 2025-10-22, but the held-out window starts 2025-10-13. About seven trading days of held-out data are inside the training period for every hmm row. It is small but it is contamination, and the plan flagged it.
2. **The held-out year chose the set.** Candidates had to pass in both windows and were ranked by the lower of the two, so the held-out numbers are optimistic. A final untouched period does not exist yet.
3. **Multiple testing.** Roughly 18,000 candidates were tried and 94 passed. At 11 to 24 held-out trades a strategy, a few passes are expected by chance.
4. **Tuning adds more.** The `-g` rows were selected as the best of 432 exit combinations in-sample. The held-out year then selected again.
5. **Win rate is not return.** Win rate favours small take-profits with wide stops. `streak-nwtg-down3` has an 80% win rate and an 82% single-trade loss. The ETF picks win often and earn about 2 to 4% a year.
6. **Liquidity is unchecked.** `TELO`, `UMAC`, `NWTG` and `TKNO` are small caps. The shared-account result assumes fills at the simulator's prices.
7. **Some hmm picks have thin samples.** `hmm-calm-psfd-g` has 33 in-sample trades and `hmm-calm-mayt-g` 35, just over the 30-trade gate.

## 9. What to do next

In rough order of value:

- Check bar history, price and traded volume for each pick, and add a minimum price and volume gate to `pick`, then rerun.
- Re-train or extend the HMM so its training end is before the held-out start, or move the held-out start after 2025-10-22, and rerun the hmm rows.
- Reserve a final period that no step above touches (for example the months after 2026-10-23 as bars arrive) and report the set's result there only.
- Run `backtest stack` with the ten as a real shared account to get fills, drawdown and capital use. The set analysis here uses signals and standalone trades.
- Add return correlation to the set analysis (needs per-strategy daily returns kept in the result databases).
- Decide the "stack" to "book" rename (rule 3: one change across code, docs, CLI and skill).
- Clean up the markov family (package, `train markov`, `data/markov_models.db`, the 500 MB bundle, orchestrator code, `STRATEGY_ALLOWLIST` entries) once nothing live uses it.
- Extend `-exclude` into a real gate: skip strategies whose symbols are no longer in `universe.db`.

## 10. Rules kept

No mocks or fakes (tests use real temp SQLite); calculation in SQL slice tables with one stage per file; outputs only in `data/reports/`; real data written only after approval and with a backup; no live commands on any account; nothing committed. `make check` passed after the pick work, before the final `-exclude` change. After that change only `go vet` and the tests for `runner`, `backtest` and `layers` were rerun.
