# Omnifunds reverse engineering

Scratch analysis behind `docs/ImitatingOmnifunds.md`. Run these with `sqlite3` against a **scratch** database, never against `data/market_history.db` or `data/ibkr_2025oct_2026_oct.db` (attach those read-only, or read from a copy). Nothing here is embedded or run by a Go command. The strategy that came out of it is `sql/strategies/rotation_strategy`.

| File | Needs attached | Writes (scratch) |
|---|---|---|
| `01_decision_log.sql` | `i` = ibkr_2025oct_2026_oct.db | `tx pos eq held heldprev newbuy sold stretch daily` |
| `02_behavior_stats.sql` | tables of 01, `m` = market_history.db | none, prints |
| `03_features_descriptive.sql` | tables of 01, `m` = market | `bars liq uni px rk` |
| `04_pit_universe.sql` | `m` = market | `b0 f lq` |
| `05_clone_curves.sql` | tables of 04, `r` = scratch of 01 | `u sc rk p_top* bench reg g_* q` |
| `06_candidates.sql` | tables of 04 | `rotation_candidates` |
| `stats.sql` | a table `T(date, r)` | none, prints CAR / max drawdown / Calmar |

`03` ranks names by *current* liquidity, so it is descriptive only (how the fund's picks compare to other names). Any return number must come from `04` onward, which ranks by liquidity known at each close.

Results and caveats are in the memory note `omnifunds-reverse-engineering` and in `docs/ImitatingOmnifunds.md`.
