# Strategies tested: log of false successes

One entry per strategy that looked like it reached a goal and did not hold up. Each entry records what was claimed, the numbers, and why the claim is false or unproven. Add new entries at the top.

## Result databases in `data/reports/` (scanned 2026-10-22)

Read from copies of the files, not the originals. Numbers are `performance_summary` per run: $100,000 capital, 25% allocation, hourly engine, all runs created 2026-10-13 to 2026-10-15. The in-sample window ends 2025-10-10 and the held-out window is 2025-10-13 to 2026-10-09. Each in-sample window starts when that strategy's first trade can happen, so the windows are short (47 to 228 trading days).

| Report | Strategy | Window | CAGR | Max DD | Sharpe | Calmar | Trades | Win rate |
|---|---|---|---|---|---|---|---|---|
| 1, 3 | markov_model_amuu | in-sample 2025-02-12 to 2025-10-10 | 68% | 16.6% | 1.66 | 4.11 | 27 | 85% |
| 1, 3 | markov_model_amuu | held out | 30% | 17.8% | 1.02 | 1.70 | 40 | 85% |
| 2 | markov_model_mull | in-sample 2024-11-12 to 2025-10-10 | 28% | 18.5% | 1.21 | 1.50 | 33 | 85% |
| 2 | markov_model_mull | held out | 78% | 15.8% | 1.84 | 4.92 | 56 | 89% |
| 3 | markov_model_vrtl | in-sample 2025-03-25 to 2025-10-10 | 99% | 10.0% | 3.20 | 9.95 | 25 | 96% |
| 3 | markov_model_vrtl | held out | 28% | 24.3% | 1.03 | 1.16 | 39 | 90% |
| 3 | streak-shpu-down3 | in-sample 2025-08-06 to 2025-10-10 | 36% | 1.1% | 4.12 | 34.56 | 5 | 100% |
| 3 | streak-shpu-down3 | held out | 42% | 2.1% | 2.55 | 20.52 | 18 | 100% |

**Why none of these is a result:**

- **markov_model_vrtl (99% CAGR, 10% drawdown in-sample).** It is the closest to the goal on paper and it did not hold: held-out CAGR fell to 28% and drawdown rose to 24%. The in-sample window is 139 sessions with 25 trades, so 99% is a 6-month return annualized.
- **markov_model_amuu.** CAGR fell from 68% to 30% on the held-out year, drawdown stayed near 17%. Neither window has a drawdown under 10%.
- **markov_model_mull.** The in-sample year was the weak one (28%) and the held-out year the strong one (78%). A held-out result better than the in-sample one on 56 trades is not evidence of an edge; the reversal is not explained.
- **streak-shpu-down3 (100% win rate, Calmar 20 to 35).** Only 5 trades in-sample and 18 held out, all winners, with a 1% to 2% drawdown. With no losing trade the Calmar and profit factor are not meaningful, and a 47-session in-sample window annualized to 36% CAGR is a two-month return. Not enough trades to tell a rule from luck, and one stop-out would change every ratio.
- **Selection bias is unmeasured.** These ids are rows among tens of thousands in `refdata/strategies.db`. The databases do not record how they were chosen, so whether they were picked for scoring well is not known; if they were, the in-sample figures are inflated before any overfit.

**`regime_score.db`** (not a strategy). Pooled the trades of the four strategies above and scored them by the SPY regime (bull, sideways, bear) from the latest session before entry (table `spy_session`). 310 trades loaded, 67 duplicates dropped (the same strategy appears in more than one report), 243 scored.

| Window | Regime | Strategies | Trades | Win rate | Avg return per trade |
|---|---|---|---|---|---|
| in-sample | bull | 3 | 20 | 95% | 6.4% |
| in-sample | sideways | 4 | 69 | 87% | 3.2% |
| in-sample | bear | 1 | 1 | 100% | 6.3% |
| held out | bull | 4 | 35 | 94% | 5.2% |
| held out | sideways | 4 | 116 | 88% | 2.9% |
| held out | bear | 1 | 2 | 100% | 5.0% |

Finding: the trades win in every regime that has trades. Bear has 1 and 2 trades, so the data says nothing about how they behave in a falling market. The regime split does not show a filter that would have improved them.

**`streak-voo-up5-tqqq.db`** (not a result). No trades or runs, only the market bars (1,950 daily rows for two symbols, 2021-01-04 to 2026-10-09) and the views `v_streak_voo_up5_tqqq_slice`, `_signals` and `_signal_trace` generated from `streak-voo-up5-tqqq.sql`. It is an untested idea: buy TQQQ after VOO closes up 5 sessions in a row. There is nothing to log until it is backtested.

**Not logged:** `views.sql` (shared view definitions) and the `report.html` / `report_oos.html` files, which render the same runs as the databases above.

## rotation-2x-sector-pairs-daily-limit95-tp3-sl5-hold2-pos20 (2026-10-09, run folder `data/reports/58`)

**Claim:** about 80% CAGR with drawdown under 10%. Capital $100,000, cash yield 0, slippage 0.05% per side.

| Window | Dates | CAGR | Max drawdown | Sharpe | Calmar | Trades | Win rate |
|---|---|---|---|---|---|---|---|
| In-sample | 2021-10-01 to 2025-10-02 | 78% | 16% | 3.39 | 4.84 | 1,249 | 70.8% |
| Held out | 2025-10-03 to 2026-10-01 | 80% | 5% | 3.55 | 16.18 | 323 | 69.3% |

**Rule:** universe of the 50 2x leveraged ETFs of `etf-2x-sector-pairs-pruned` (25 sectors, each long and matched inverse). Every session it puts a buy limit at 95% of the previous close on every name (`pick = all`), 20% of portfolio per position, monthly cooldown per name. Exit on the first of +3% take profit, 5% stop, or the close 2 sessions after entry.

**Why it is a false success:**

- **Drawdown goal not met.** The four in-sample years reach 78% CAGR with a 16% worst drawdown. "Under 10%" holds only on the one held-out year, which is short.
- **The held-out year is not clean.** The symbol list was pruned using results (`prune_losers symbols` removed names that lost money in both windows), and the position size (`-position-pct 0.20`) was set after seeing them. Both choices used the held-out data.
- **Leverage did the work.** Doubling the position size took the 4-year CAGR from 33% to 78% and the in-sample drawdown from 12% to 16%. The base strategy (33% CAGR, 12% drawdown at 10% positions) never met the goal.
- **Fills are optimistic.** The backtest fills a name whenever the daily low reaches the limit. An order that does not fill reserves no cash, so the backtest holds a limit on all 48 names at once (about 960% of equity against 100% of cash). Live, a working buy order reserves its cash, so only about 5 limits (20% each) can rest.
- **Live and backtest book different trades.** The backtest picks among filled names alphabetically; live picks the nearest to filling. On a day when more than 5 names dip to their limit, live misses fills the backtest booked. A name placed by the 5-minute trigger after its low was reached only fills if price comes back to the limit. The cost in CAGR was never measured, so 80% is an upper bound for the live system.
- **Never run live.**

**To measure the fill gap:** count, in the saved `trades`, the sessions with more than 5 same-day entries, then rerun with a position cap of 5 and nearest-first ordering to see the CAGR left.

**Status:** not a result. Do not quote 80% CAGR / under 10% drawdown.
