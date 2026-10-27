# Omnifunds reverse engineering

Scratch analysis behind `docs/ImitatingOmnifunds.md`. Run these with `sqlite3` against a **scratch** database, never against `data/market_history_hourly.db` or `data/ibkr_2025oct_2026_oct.db` (attach those read-only, or read from a copy). Nothing here is run by a Go command.

The part that became the strategy has moved into the rotation family. The point in time liquid universe, the candidate symbols and the equal weight top-K curves (the old `04_pit_universe.sql`, `05_clone_curves.sql` and `06_candidates.sql`) are now the numbered stage `sql/stages/rotation_universe`, run by `strategy rotation_universe`, which stores the candidates as the symbol list `rotation-candidates` that the rows of `sql/seed/rotation_strategy.sql` name. What stays here is the part that needs the fund's own IBKR statements, which are not in the market database:

| File | Needs attached | Writes (scratch) |
|---|---|---|
| `01_decision_log.sql` | `i` = ibkr_2025oct_2026_oct.db | `tx pos eq held heldprev newbuy sold stretch daily` |
| `02_behavior_stats.sql` | tables of 01, `m` = market | none, prints |
| `03_features_descriptive.sql` | tables of 01, `m` = market | `bars liq uni px rk` |
| `stats.sql` | a table `T(date, r)` | none, prints CAR / max drawdown / Calmar |

`03` ranks names by *current* liquidity, so it is descriptive only (how the fund's picks compare to other names). Any return number must come from the `rotation_universe` stage, which ranks by liquidity known at each close. To compare a curve with the fund itself, load the stage's scratch database with `strategy rotation_universe -keep-calc` and join `p_top10`, `p_top20` or `g_top10` to `i.ibkr_return_daily` from the fund's window (2025-10-07 on).

Results and caveats are in the memory note `omnifunds-reverse-engineering` and in `docs/ImitatingOmnifunds.md`.
