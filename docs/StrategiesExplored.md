# Strategies Explored

One checklist of every strategy and tactic I have dived into, and every idea still waiting.

- `[x]` explored: built and run, with a verdict (or an honest "no verdict").
- `[ ]` open: an idea, a study to run, or a fix to a method.
- Numbers come from `LearningLog.md`, `backtestgosqlite/docs/*`, and the repos' code. Small samples are labelled as such.
- Sources: **repo** = a note found in a repo; **outside** = a note you pasted from outside the repos.
- The method rules (hold out the last 12 months, walk-forward, tiers) are in `LearningLog.md` §4. They are not repeated here.

---

## 1. Entry signals and strategy families

### Mean reversion after a drop or streak
- [x] **Streak / drop-N-days** (`streak-<signal>-<up|down><days>-<trade>`, one row per strategy in `streak_strategy`). The original 2022 idea (`sanddune`) and still the largest family. Several found in tech. Controlling drawdown with allocation limits CAGR fiercely.
- [x] **VOO + TECL combo (`voo-tecl-combo`), with `gld-decline` as secondary.** Found with Antigravity's help; the closest to competing with Omnifunds' "AI - Model 2: Genetic Scoring NAS50". Run as `./bin/backtest -primary voo-tecl-combo -secondary gld-decline -capital 100000 -html reports/voo-tecl_then_gld-decline_backtest.html`. It likely became `streak-voo-buy-tecl` below; confirm.
  - [ ] **Print its CAGR per calendar year** of backtesting, to see whether one great year carries it. *(outside)*
- [x] **`streak-voo-buy-tecl`** (live). The only member of the deployed stack that made money in the held-out year: 18.1% CAR, 5.3% DD. Live, it did a same-morning round trip the backtest would have held for up to 8 days (+$249), so exits are not yet identical.
- [x] **Whitings Creek** (buy a ≥10% drop below the trailing low). 8 entries, 8 hits, which is nothing. Archived 2026-09-14.
- [ ] **Whitings Creek with a market filter on the buy.** Hunch: the whole market is a signal that makes individual stocks more predictable. When trading Whitings Creek by hand, local bottoms were easier to call when the entire market had also moved the same direction. Study: re-run Whitings Creek buying only when the market (VOO) agrees, against the unfiltered version. Caveat: the original had only 8 entries, so the test needs a longer window or more symbols. Related to the VOO declining / not declining buckets in §3. *(outside)*
- [x] **Market-went-up, buy the leveraged ETF** (`voo-up3`, `sig_qqq_up1_buy_tqqq`, VOO to TECL/SPXU). No good one found. `voo-up3`: 113 trades, 54.9% win rate, CAGR −1.1%.
- [x] **Price Action Reclaim.** 13 trades in 5.7 years, +0.34% total. Dead.
- [ ] Check I am not over-invested in the same name by signal (two strategies buying the same symbol). *(outside)*
- [ ] Compare buy signals on their own before creating exits and the rest of the strategy. *(outside)*
- [ ] Watch the price-action video and work out why it did not translate into good code. *(outside)*

### Decision trees (CloudForest)
- [x] **Depth-3 tree on a ±5% next-day target** (`study-3-signals` 2025; the 2026 `tree_strategy` rows use 13 features and a 5-bucket target). Thin evidence: the 2025 version fired on 6 days.
- [x] **MARA and PDD trees** (live). Lost alone in the held-out year (MARA −16.5%, PDD −28.7%).
- [x] **Found the trees were untrained** (2026-10-02). Every earlier tree result was a generic fallback, not a tree result. Training is now its own command.
- [x] **Trees fit on all history have seen the held-out months.** 775 "beat the claim", not believable. Excluded from the clean stack.
- [ ] Use `train -through <date>`: **train on 2022 and 2023, evaluate on 2024 and 2025.** *(outside)*
- [ ] Try the balanced 5-bucket target (≤−5, −5..−1, ±1, 1..5, ≥5) and pick which CloudForest tree to use. *(repo: `PotentialUpgradestoTrees.md`)*
- [ ] Find out which parameters can be grid-searched on CloudForest. *(outside)*

### Markov regime models
- [x] **`markov_model_<symbol>`**: label bull / bear / sideways by trailing 20-day return (±5%), build the 3×3 transition matrix, buy when it predicts bull, hold ≤15 days, take profit +5%, 25% of equity. Source: `UsingMarkovModels.md`, plan in `MarkovModel.md`.
- [x] **Held-out year (2025-10-02 to 2026-10-01).** AMD 46.4% CAR / 8.0% DD, LITE 71.9% / 8.6%, MULL 90.5% / 16.0%. Caveat: those three were picked because they did well, out of about 22,000 results. In-sample the four-strategy stack made 52.2% CAR / 30.7% DD (Calmar 1.7).
- [x] **HMM regime study** on QQQ (`study hmm_regime`; the `markov_hmm` strategy that read it was removed). Run; no recorded verdict.
- [x] **GOOGL example.** "Completely sucked." *(outside)*
- [x] **Confidence-sizing the Markov position** (size by signal strength). Done; "didn't help much" on GOOGL. *(outside)*
- [ ] Run Markov on S&P 500 / VOO only, and use that to try the variations. *(outside)*
- [ ] Study Markov on GOOGL as an **exit** signal rather than an entry. *(outside)*
- [ ] Color the Markov regime graph. *(outside)*
- [ ] Walk-forward point-in-time matrices (Phase 6 of `MarkovModel.md`). Confirm it is zero look-ahead.

### Biggest winner (buy the prior period's leader)
- [x] **Annual** (from a conversation with Patrick: "beat the SP500 most years"). Ended −93% in my run (4 trades, 0 wins).
- [x] **Inverse and short variants.** +24% and +20% CAGR on 1 and 5 trades, not a result.
- [x] **Weekly rebuild** (2026-09-30). No verdict found.
- [ ] **Backtest: buy and hold for a week the top performer of the previous week.** Likely the same run as the weekly rebuild above; check its results before re-running. *(outside)*
- [ ] Is there enough delay in market response to just jump into an annual portfolio? *(outside)*

### Hold / rotation / overlay
- [x] **Buy and hold benchmarks.** QQQ 22.6% / 12.2% DD, XLK 38.3% / 16.1%, SMH 82.8% / 24.6%, GOOGL 37.7% / 21.1% (held-out year). A hard bar to clear.
- [x] **Grok4Trading monthly rotation** (SPY/TLT, then UPRO/TMF). About 0.7–1% a month. The 5%/month target is a fantasy.
- [x] **Crisis winner** (buy inverse ETF TZA when SPY drops, exit on recovery). Built 2025-07; no results file.
- [x] **Covered calls** (`backtest covered-call`; "could I rent out risk monthly?"). Implemented, no recorded conclusion.
- [x] **Crypto** (`tradexrpbot`, `BTCMomentum`). 30% win rate, −2.63% while BTC fell 38%. The put-overlay "+60%" is Black-Scholes at an assumed IV, not real.
- [x] **Third-party email signals** (`backteststoxx`). 3 of 193 trades hit target. A limit on someone else's price mostly never fills.
- [x] **Failed and retired:** `bb_capitulation`, `donchian_breakout`, `genetic_momentum`, `macd_crossover`, `rsi2_trend`, `trend_bb`, `voo_context_forest`, `voo_tree`.
- [ ] **Set out-of-the-money buy limits with 5% profit takers.** *(outside)*
- [ ] **Downsize the universe to ETFs and cap hold windows at 3 days.** *(outside)*
- [ ] Study which VOO streak performed best (refresh). *(outside)*

### Ops
- [x] **`tsll-daily-one-share`.** Exists to exercise the pipeline daily, not to make money. Matched its backtest almost exactly.

---

## 2. Portfolio construction and benchmarks

- [x] **Stacking uncorrelated sleeves on one cash ledger** (`backtest stack`, `park-<symbol>` for idle cash). The one structural idea that kept paying. Complementarity is not guessable; rank finalists by Calmar and drawdown, not added equity.
- [x] **Clean stack** (no trees): TECL + AMD + LITE + MULL, 339.6% held-out CAR but 52.2% in-sample. Mostly "semiconductors had a great year."
- [x] **Omnifunds benchmark** (vendor claim: 44.9% CAR / 29.8% DD). Reverse-engineered in `ImitatingOmnifunds.md`: rank daily, concentrate in 3–5 names, cap 50% per name, skip earnings.
- [x] **My IBKR year.** 24.9% CAR, 41.1% DD, lost money in dollars. Plain buy and hold had about a third of the drawdown.
- [ ] **Find a stack of strategies that works at 10% allocation.** *(repo: `Ideas for Backtest.txt` #13)*
- [ ] Run several sibling strategies without a designated primary; make `park` stackable like any strategy. *(repo: `UpgradeArchitecture.md`)*
- [ ] Sweep primary and position-size settings to map CAGR against drawdown (best stack so far: 79.9% CAGR in-sample, 19.7% DD, against a target under 6%). *(repo)*
- [ ] Check whether the drawdown is only there because 2022 was great. *(repo)*
- [ ] **Skip earnings reports** (backtest). Find a data feed for the earnings calendar and measure how often scheduled dates hold. *(outside + repo)*

---

## 3. Studies (questions, not strategies)

Studies are not strategies. Each answers a question that may or may not become one.

- [x] **5% gain frequency** (`gain_5pct_frequency`).
- [x] **VOO / GLD / UTEN Granger causality** (`march_april_voo_gld_uten`).
- [x] **QQQ vs TQQQ volume** (`qqq_tqqq_volume`).
- [x] **ETF universe discovery:** 5,449 US ETFs, 1,753 kept, one tree plus grid search each. Top result HIBL (42.4% CAGR) is regarded with suspicion; survivorship bias.
- [ ] **Re-run the 5% study against the entire universe.** *(outside)* Concrete form of the study:
  - [ ] Sort all available tickers by how often they go up 5% in one day. *(outside)*
  - [ ] Mark every leveraged ETF in that ranking, so they don't masquerade as stock-picking skill. *(outside)*
  - [ ] Cluster the movement of each ticker in the days before its 5% gain (uses the `market_clustering` stub). *(outside)*
  - [ ] Split by market bucket: (1) VOO declining, (2) VOO not declining. When VOO isn't declining, what is most likely to go up 5%? *(outside)*
- [ ] Metrics: is the close closer to the high or the low? *(outside)*
- [ ] **Score signal generation** by which finds a 5% day and avoids a 5% loss. *(outside)*
- [ ] **Run streak, Markov and CloudForest on GOOGL** and compare. *(outside; `googl_suite_integration_test.go` exists)*
- [ ] GOOGL: a signal that it is about to drop 5% in a day (`googl_drop_predictor`). *(repo: Ideas #8)*
- [ ] GOOGL news response speed; can I jump on good news? (`news_response`, needs Polygon news.) *(repo: Ideas #10)*
- [ ] **Market clustering** for context (`market_clustering.go` is a TODO stub). *(repo: Ideas #2)*
- [ ] **Weekly seasonality**: profile each trading week, look for cyclical stocks (`weekly_seasonality`). Same with Polygon minute bars. *(repo: Ideas #4, #5)*
- [ ] **Which S&P 500 stock leads VOO** on minute bars (`sp500_lead_lag.go` is a TODO stub). *(repo: Ideas #11)*
- [ ] **Early-exit signals:** once in a position, a simple calc that says to leave before the default hold window. *(repo: Ideas #12)*
- [ ] Small public companies that got acquired: run-up before the announcement. *(repo: Ideas #9)*
- [ ] Is there a pattern in sleepy markets versus panic? *(outside)*

---

## 4. Data and signal sources

- [ ] **Morningstar feed:** do 5-star stocks reduce drawdown risk? *(repo: Ideas #6)*
- [ ] **Score stocks on the 5 most different fundamentals** (PCA to find the orthogonal five). *(repo: Ideas #7)*
- [ ] **Backtest insider trading / the Trump picker** (needs an API of reported trades). *(outside; also `Fonxi.md`)*
- [ ] Is there an algorithm that picks which of 20 facts matters and offers a decision on that? *(outside, listed twice)*
- [x] Is there a wave-function version of correlation? *(outside; answered)*
- [ ] Is Elliott wave defined enough to automate? *(outside)*

---

## 5. Method and tooling to-dos

- [ ] **Try separate DBs per run per family.** Partly done (results are one DB per family per run folder). *(outside)*
- [ ] Automate pattern finding and strategy creation. The pipeline command wraps the stages; the search is still manual. *(repo: `UpgradeArchitecture.md`)*
- [ ] Rerun backtests with slippage and commissions (entry fills were 49–62 bps worse than the model live).
- [ ] Fix survivorship bias in ETF discovery.
- [ ] **Merge this list with the idea docs in the repos.** *(outside)* This file is the merge; once you agree, fold `Ideas for Backtest.txt` and `NEW_RESEARCH_PLAN.md` into it.

---

## 6. Finding better buy signals, and knowing when I'm done

- [ ] **Find more effective ways to search for high-CAGR strategies with lower max drawdown.** Where it stands: the manual search (§5 "automate pattern finding") produced the combo; stacking (§2) and Calmar ranking are the levers tried so far. *(outside)*
- [ ] **Done when:** I find a buy signal that is great while VOO is *not* declining. Most of what I have is VOO-down mean reversion (§1, streak). *(outside)*
- [ ] **Ask what beats "3-day decline in VOO" as a buy signal for TECL.** Candidate: the not-declining bucket study in §3. *(outside)*
- [ ] Score candidate signals against each other before attaching exits (see §1, "Compare buy signals").

---

## 7. The standing question

- [ ] Does any strategy make money live, net of slippage and fees? About three weeks of live data so far, and it lost $702. Everything above is a hypothesis until the weekly live-vs-backtest reconcile says otherwise.
