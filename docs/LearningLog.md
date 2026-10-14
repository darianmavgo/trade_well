# Learning Log: backtesting and order submission

Compiled 2026-10-05 from commit history, READMEs, docs, and result databases across these repos:

- **Current (2026):** `backtestgosqlite`, `schwaber`, `trade_orchestrator` (the three under `trade_well/`), plus `ibkr_personal`, `tradexrpbot`, `backtest_reports`.
- **Earlier (GitHub, cloned to read):** `sanddune` (2022), `backteststoxx`, `backtest-sell-limit`, `Grok4Trading`, `study-3-signals`, `crisis-winner`, `trading`, `tradingbolt` (all mid-2025).

Every number below is copied from a doc, a commit message, or a result DB in those repos. Where a number comes from a tiny sample, or I could not confirm it still holds, I say so. Section 7 lists what I did not verify.

---

## 1. What I know now (read this first)

### About the market

1. **The edge, if any, is short-term mean reversion in volatile things.** Every idea that survived (streak/drop-N-days, decision trees on ±5% days, Markov bull/bear regimes) is some version of "it fell or got stretched, buy the rebound, exit on a fixed clock." The very first 2022 notebook was already "buy after 3 consecutive down days." The 2026 engine now holds about 65,600 strategies and the largest family is still that rule, one per ticker.
2. **"Market went up, so buy the leveraged ETF" has not worked.** I wrote a pile of these (`voo-up3`, `sig_qqq_up1_buy_tqqq`, `sig_voo_up1_buy_tqqq`, VOO to TECL/SPXU). The commit on 2026-09-21 says it: *"I have looked at so many strategies built on a market gains as buy signal that it is suspicious that I haven't a good one."* `voo-up3` over 5 years: 113 trades, 54.9% win rate, **CAGR −1.1%**, max drawdown 40.8%. A winning win-rate with a negative return means the 5% target / 6% stop payoff is lopsided against me.
3. **High win rate is not an edge.** Whitings Creek (buy a ≥10% drop below the trailing low, +20% / −7% / 10 days, require ≥90% historical win rate) looked perfect on its sample: 8 entries, 8 hits at 3% and at 5%. Eight entries is nothing. The strategy was archived 2026-09-14 after the grid search returned the *same* best config for it and for unrelated strategies, which meant it was measuring the fallback and not the signal.
4. **Decision trees on a ±5% next-day target find something, but the evidence is thin.** 2025 `study-3-signals`: a depth-3 tree on BAR/SPY/BND daily returns predicted SPXL up ≥5% next day. At ≥90% confidence it fired on only 6 days in 2021–2025; 5 of the 6 next-day returns were ≥ +5%, one was −0.6%. All six had the identical probability (0.987), which is one leaf of a tiny tree, in-sample. The 2026 version is a 13-feature, depth-3 tree on a 5-bucket target (≤−5%, −5..−1, ±1, 1..5, ≥5), which needs ≥10 bars of the ≥5% class per symbol.
5. **Markov regime models had the best out-of-sample-looking results, with a big caveat.** `markov_model_<symbol>`: label each day bull/bear/sideways by trailing 20-day return (±5%), build the 3×3 transition matrix, buy when it predicts bull, hold ≤15 days, take profit +5%, 25% of equity per position. Held-out year (2025-10-02 to 2026-10-01): AMD 46.4% CAR / 8.0% DD, LITE 71.9% / 8.6%, MULL (2x Micron) 90.5% / 16.0%. But those three were picked *because* they did well in that year, out of about 22,000 held-out results. The honest number is in-sample (2021-01 to 2025-10): the four-strategy stack made **52.2% CAR with a 30.7% drawdown, Calmar 1.7**, and AMD and LITE alone were only 6.2% and 6.7% CAGR in that period. The held-out 339% is mostly "semiconductors had a great year."
6. **Buying the prior period's biggest winner did not work in my runs.** Idea from a conversation with Patrick (annual version "beat the SP500 most years"). My annual run ended at −93% (4 trades, 0 wins). The inverse and short variants show +24% and +20% CAGR but on **1 and 5 trades**, which is not a result. Rebuilt to weekly on 2026-09-30. I did not find a verdict.
7. **Price Action Reclaim made nothing:** 13 trades in 5.7 years, +0.34% total. **Covered calls:** implemented (`backtest covered-call`) after the 2022 note "could I rent out risk monthly?", but I found no recorded conclusion.
8. **Crypto momentum lost, and the options overlay result is not real.** `BTCMomentum` (EMA/RSI/MACD/ADX, 1h, 2025-09 to 2026-03): 10 trades, 30% win rate, 6 stop-outs, −2.63% while BTC fell 38%. The "+60% with put overlay" report is Black-Scholes at an assumed 75% IV with no spreads. Treat it as a diagram, not a result.
9. **Third-party email signals did not pay.** `backteststoxx` (a Gmail label of 294 emails, 2013–2025): 253 had a ticker, 175 survived cleanup. Of 193 backtested trades: 90 stopped out (46.6%), 64 never filled (33.2%), 36 no trade, **3 hit target (1.6%)**, returns −4.5% to −6.5%, negative Sharpe. A limit entry on someone else's price level mostly never fills, and when it does it tends to stop out.
10. **A fixed 5%/month target is a fantasy.** `Grok4Trading` set it, and its own README conceded SPY/TLT monthly rotation gave about 0.7–1%/month. The leveraged UPRO/TMF v1.2 "still has large months of losses."
11. **Stacking uncorrelated sleeves on one cash ledger is the one structural idea that kept paying.** Idle cash is parked in a symbol (`park-<symbol>`), sleeves are sized as a fraction of current equity, and complements are found with `backtest stack`. Complementarity is not guessable: `bb_capitulation` was "definitely not a VOO/TECL buddy." Greedy selection ranks by added equity, so finalists still need ranking by Calmar and drawdown.
12. **Buy and hold is a hard benchmark.** Same window as the 12-month holdout: QQQ 22.6% CAR / 12.2% DD, XLK 38.3% / 16.1%, SMH 82.8% / 24.6%, GOOGL 37.7% / 21.1%. The Omnifunds figures I am trying to beat (all-time 56% CAR, 29.8% max drawdown, Calmar 1.88; 1-year claimed 42–44.9%) are a vendor claim whose method I don't know.

### About my own trading

13. **My real IBKR account underperformed its claim and lost money in dollars.** 12 months to 2026-10-01: 24.9% time-weighted CAR with a **41.1% drawdown** (Calmar 0.61), versus 44.9% / 29.8% claimed. Net deposits were $124,936 and ending equity $105,195. Most deposits arrived in June, before the 3-month drawdown, so the time-weighted return flatters what the money earned. The worst day was 2026-06-05 (−13.6%) when the account sold its semiconductor holdings after a 13–17% one-day fall. Buy and hold QQQ beat it on drawdown by about a factor of three.
14. **The first live week lost money for specific, explainable reasons.** Greenlit strategies were down about **$702** across two funded accounts from 2026-09-21 to 2026-09-29. One trade, MARA on Sep 24–25 (bought 13.06, sold 12.62), was $656 of it. Entry fills were 49 to 62 bps worse than the model on the next-day-limit strategies. `tsll-daily-one-share` (a plumbing check, not a money-maker) matched its backtest almost exactly, which is the useful part.
15. **Live behavior diverged from the backtest's exit logic.** `sig-voo-buy-tecl` made +$249 on a same-morning round trip that the backtest would have held up to 8 days for a 5% target. That is a manual or operational flatten the model never saw, so the live record can't validate the backtest until exits are identical.

---

## 2. Bugs and traps that fooled me (each one invalidated some earlier "result")

| Date | What went wrong | What it taught |
|---|---|---|
| 2025-07-15 | Gemini kept changing the backtest start date (*"i had to fight gemini multiple times to not fuck with the start date"*) | Pin the window in one config file. Standardized to 2021-01-01 → 2025-07-05 across all strategies. |
| 2026-09-08 | *"I just learned my code isn't capturing all the calculations in sqlite"* | Anything computed in Go and not persisted can't be audited. Pushed all math into SQL tables. |
| 2026-09-13 | **CAGR dilution.** After the ETF backfill reached the 1990s, the simulator clock covered decades of idle time: `tree_bounce_combo` CAGR fell from 39% to 9% with identical trades. | The simulation clock must be scoped to the strategy's own symbols. A metric can silently change when unrelated data is added. |
| 2026-09-14 | Generic grid-search fallback gave three unrelated strategies the same best config (Score 0.0149, Calmar 0.71). One strategy had 210,000 permutations, 79% of a 265,740-config batch, and looked like a hang. | If many different strategies produce identical optima, the optimizer isn't touching their logic. Cap grid size. |
| 2026-10-02 | *"Just found out all the trees are not trained. they just used a generic coil bounce combo."* | Every tree result before that date was not a tree result. Training became its own command (`train`), separate from `backtest`. |
| 2026-10 | Tree models fit on all history have seen the held-out months. 775 trees "beat the claim," which is not believable. | Trees are excluded from the clean stack. Use `train -through <date>` to keep later months out. |
| 2026-09-03 | *"Discovered that Gemini 3.8 built dikr to lie to me"*, then *"Removed lying in code found by Gemini 3.1 Pro"* | An AI wrote an IBKR tool that fabricated data. This is where "no mocks, no fakes" (Rule 4) comes from. |
| 2026-09-29 | The ledger dated MARA/PDD fills on the fill day, not the signal day, which shifts a next-day strategy by one session. Sells that were separate orders had no exit fill recorded. | Store the signal date, the intended entry, and every order leg at the time of the decision. They can't be reconstructed later. |
| 2026-09-29 | `manage_exit` still said TSLL was OPEN after the broker had sold it, so later scans were dismissed as "already holding." | State that disagrees with the broker blocks real signals. The broker is the source of truth. |
| 2026-09-21 review | No slippage or commission in backtests; ETF discovery only lists tickers alive today (survivorship); a single 12-month split can't protect against picking winners by luck. | Re-run with realistic costs and drop what fails. The review's own advice: dry-run or 1 share for weeks first. |

---

## 3. Chronological log

### 2022: SQL-only, one idea
- **2022-06-01** `sanddune`: study "3 consecutive down days as a buy signal" in SQLite + notebooks. Questions asked: how often does a limit at the close fill the next day (`buylimitmet`), dollar gap between limit and high, day-4 high and low.
- **2022-06-09/10** Reuse the Whitings Creek SQL; first stats on 3% and 5% wins for three entry definitions. *"fucking left join failing"* (the first of many SQL-state bugs).
- **2022-06-14** First study finished: buy signals prevclose vs sanddune vs Whitings Creek.
- **2022-07-08..13** Standardize SQL, add idx scripts, convert `create as` to `insert into`. *"finished one tolerable round of stocks."* Seeds of the later "slice table" idea.
- Open thought in the README: study by week-of-year; sell out-of-the-money calls against held shares.

### Mid 2025: Python + backtrader, many repos, AI-assisted
- **2025-06-26** `backteststoxx`: Gmail → SQLite email extractor (Go goroutines made the pull "crazy fast"), parse buy/stop/target from email text. Python backtest.
- **2025-06-27** `backtest-sell-limit`: "no more yf or yahoo finance," Go fetches history from the Yahoo v8 endpoint; backtrader runs on it; S&P 500 list rebuilt manually; portfolio stored as a table; *"no sql in code."*
- **2025-06-29** `tradingbolt`: a Bolt-generated stock-screen UI. Dead end.
- **2025-07-01/02** Massive refactor, lost then recovered data ("got my data back"). *"I don't think God wants me to bother with stock trades."*
- **2025-07-05** *"I got a lot further on backtests this year than in 2020."*
- **2025-07-11/12** `Grok4Trading` v1 → v1.2: SPY/TLT then UPRO/TMF monthly rotation. *"show me something that works."* `study-3-signals`: decision trees for SPXL/SPXS. *"Grok is kind dumb on data management"*, *"my logic ended up being better"*, *"I enjoyed learning about decision trees."*
- **2025-07-14..16** `trading` / `crisis-winner`: consolidate everything onto one window and one schema (`allowed_tickers`, `strategy_history`, `config.json`). Crisis-winner idea: buy inverse ETF (TZA) when SPY drops sharply, exit on recovery; the 2025-07-16 commit message says "Crisis winners was quite interesting" but I found no results file. Lessons recorded in commits: *"never git modules on top of git modules"*, git LFS for the strategy DB.

### 2026-03: crypto detour
- **2026-03-10/11** `tradexrpbot`: XRP/BTC, Freqtrade + Hummingbot + Go drawdown watcher. Target ≥17.55% over 6 months, ≤13% drawdown. Result above (§1.8). Last commit: "Finding some assets that trade well."

### 2026-08: the Go + SQLite engine
- **08-20/21** `backtestgosqlite` initial commit: two-tier design (SQL screening, then a chronological Go portfolio simulator with a cash ledger, position limits, dual-barrier stop/target/time exits). Original thesis: mean-reversion bounces on leveraged ETFs and high-beta stocks.
- **08-26** *"This is the best backtesting learning session I've had."* Whole-market VOO studies, calmar optimizer, drawdown diagnostics. Same day: *"why are all the new studies generating go but not sql?"* and refactor Go commands into SQL.
- **08-28** `ibkr_personal` started: IB Gateway in Docker on a droplet, SSH tunnel, 2FA by phone, order queue, `dikr` CLI. *"Automating TWS sucks on purpose."*

### 2026-09: from backtest to real orders
- **09-01** `schwaber`: Schwab Trader API toolkit for a Roth IRA, started with a mock server and paper simulator, then removed both the same day ("Remove mock server, fallback quotes, and unrequested utilities").
- **09-01..03** IBKR bridge to backtestgosqlite, order queue works; **09-03** the "dikr lied" discovery; *"I hate coding for IB even with AI helping."* **09-05** "Removed lying from the repo." IBKR moved to the Archive.
- **09-03/05** `trade_orchestrator` extracted: cron schedule for entry, exit and health scanners; droplet deploy. Direct Schwab Trader API replaces an intermediary.
- **09-07/08** Big cleanup; `livescan` command exists; strategies refactored into SQL slice tables; email alerts.
- **09-08** **End-to-end test submitted real orders to the Schwab Roth.**
- **09-11** Scorecard for strategies, fixes to the SQL VOO/TECL/SPXU combo.
- **09-12/13** Minute bars for studying news. Two strategies sharing one account/cash ledger; solo vs tiered comparison. ETF universe: 5,449 US ETFs found, 1,753 with ≥6 years of history kept; one tree + grid search per ETF in 20 seconds (555 usable, 994 had too few ±5% days, top result HIBL at 42.4% CAGR, later regarded with suspicion per §2).
- **09-14** Grid-search batch mode on one shared worker pool (44x faster on the slow case); skip-duplicate-work made the default; Whitings Creek archived.
- **09-15** Repo-wide rename; Cloud Run chosen; *"it took a weird amount of work to make livescan actually live."*
- **09-16** *"Lots of failed strategies and one super strong stack to implement."* Failed strategies moved to `pkg/strategy/failed_training/`: `bb_capitulation`, `donchian_breakout`, `genetic_momentum`, `macd_crossover`, `rsi2_trend`, `trend_bb`, `voo_context_forest`, `voo_tree`. Switch from CGO sqlite to pure-Go `modernc.org/sqlite`.
- **09-17/18** Ended duplicate order submissions in the DB; submit-order bugs; healthcheck made thorough; Litestream instead of FUSE.
- **09-19** Started on App Engine ("schwab completely sucks now").
- **09-20** `strateval` in-sample/out-of-sample ledger with tiers A–D.
- **09-21** Order-intent idempotency (`client_order_id`); Schwab token sync via Secret Manager; fully moved to GAE with Litestream; cloud run deprecated. First real round trips (TECL, MARA).
- **09-22/23** *"Massive refactor"*; killed aliases twice *"cuz Claude needs hand holding."* Real order-submission fixes. Docs per package. `OldShit.html` cleanup of 48 rows.
- **09-24** `killswitch`, `exit_market`, `manage_exits`; strategy allowlist; deploy local-live.
- **09-25** Test specifically for buy-side slippage.
- **09-27/28** Research plan of 11 ideas; HMM regime study (QQQ); BiggestWinner; Jules-authored PRs merged (`--theirs` merges, then a script to find unmerged branches).
- **09-29** First live-vs-backtest reconciliation (§1.14). Added `reconcile_trades`, `audit_slippage`.

### 2026-10: finding out what the models actually were
- **10-01** Markov strategy: *"no big winner yet"* (later contradicted by the held-out year). *"studies are NOT strategies."* Strategies moved into SQLite as rows of a template (`streak`, `hold`, `tree`, `markov`).
- **10-02** Trees found untrained (§2). Holdout window made default in `backtest`. Omnifunds benchmark added. IBKR year analysed: *"ibkr for one year absolutely sucked."* Over 100,000 strategies, so DB-per-family.
- **10-03** Docs consolidated.
- **10-04** `pipeline` command wraps every stage (data → train → backtest → gridsearch → validate → strateval → scoreboard → livescan), with a GOOGL example. `trade_orchestrator` now reads DB-defined strategies. *"Arch violations still exist. There is no go / sql strategy dichotomy."*

---

## 4. Validation method that came out of all this

The order matters, and each step exists because an earlier step fooled me:

1. Hold out the **last 12 months** by default and look at them once, at the end. Editing a strategy after seeing the holdout makes that holdout in-sample.
2. **Walk-forward**: 24-month train, 6-month test folds, one strategy at a time. Verdicts: `HOLDS`, `DECAYS`, `CURVE_FIT`, `INSUFFICIENT`. Drop anything not `HOLDS`.
3. **Strict OOS gate** (SQL): ≥30 OOS trades, OOS Sharpe ≥1.5, ≥75% positive folds, worst-fold drawdown ≤6%.
4. **Liquidity screen**: dollar volume, price, split-jump check. (MULL's average daily dollar volume was $115M; positions up to $125k.)
5. **Tiers A–D** (`strateval`): A needs ≥12 OOS trades, ≥55% win rate, ≤15% OOS drawdown. Advisory only; promoting to `STRATEGY_ALLOWLIST` is manual.
6. **Stack** survivors on one ledger; rank by Calmar and drawdown, not added equity.
7. **Reconcile live to model** weekly (`reconcile_trades`, `audit_slippage`). This is the only test of whether any edge is real.

Standing result as of 2026-10-02: no stack reached 79% CAGR with drawdown under 6%. Liquid stacks held a Calmar of about 4–8 in-sample and about 4.5 on the holdout. Selecting purely on in-sample results, the best twelve strategies returned at most 13.6% held out, and earlier stacks picked that way returned −52% and +5%.

---

## 5. Order submission and live-trading lessons

**Broker facts (Schwab)**
- Access tokens last 30 minutes and refresh automatically; **refresh tokens last 7 days and rotate on every use**. Syncing the token file through a shared store between a laptop and the cloud races, which is why the token has its own object and its own sync package (`tokensync`).
- Cash accounts settle T+1. Buying with unsettled funds and then selling before settlement is a Good Faith Violation.
- Schwab doesn't always echo `clientOrderId`, so reconciling a possibly-placed order means matching against the broker's order list.
- Real Schwab answers don't match anyone's guess of them; the project notes cite a TSLL 400 error as the example. That is the stated reason for Rule 4.

**Order design that survived**
- Entry plus take-profit plus stop as a **1st-triggers-OCO bracket**, with a −20% "crisis stop" if the strategy has none.
- **Re-validate at submit time** against cash and positions as of *now*, not as of when the order was staged. Staging and submission are separate jobs (`stage_orders`, `submit_orders`).
- **Idempotency**: durable order intent written *before* the HTTP call; `PENDING → SUBMITTING` claim; on an unknown outcome, reconcile by listing orders instead of re-placing. Born from the duplicate submissions of 2026-09-17.
- Exit date is computed from the **actual fill date**, not the intended one (hold window counted in NYSE trading days via a holiday calendar). `manage_exits` runs at 09:35 and 15:50 ET.
- **Kill switch** (`killswitch`, `/exit_market`): cancel every working order first (bracket legs reserve the shares and a working buy would fill after the sell), verify none remain, then market-sell whole-share long positions. Shorts, options, mutual funds, fractional remainders are listed and skipped, never guessed.
- `check_health` audits every position: tracked? strategy known? entry and fill recorded? exit date equals fill date plus hold? take-profit and stop at the broker match the strategy? Overall status rolls up to HEALTHY / WARNING / ACTION_REQUIRED.
- **One-share ops strategy** (`tsll-daily-one-share`): a strategy that exists to exercise the pipeline daily, not to make money.

**Security and risk gaps recorded 2026-09-21..23** (some fixed in code, not all verified deployed): a hardcoded `X-Cron-Auth` bypass on the public cron URL (removed in code); OAuth tokens logged in plaintext; no max-positions limit or daily-loss breaker; no retry/backoff on 429/5xx; partial fills unhandled; no Litestream restore drill; failure emails only on `account_state`.

---

## 6. Tech stack and architecture

**Evolution**
| Era | Stack | Why it changed |
|---|---|---|
| 2022 | SQLite + notebooks, hand-written SQL | Fine for one idea; didn't compose. |
| 2025 | Python + backtrader + yfinance; Go for concurrent download and Gmail | Slow rolling windows on thousands of symbols; GIL and memory; many repos with nested git modules. |
| 2026 early | Pure Go | Fast, but nil-handling boilerplate for optional metrics. |
| 2026-09 | **Go orchestrates, SQLite calculates** | SQL window functions and `INSERT … SELECT` do the math; Go owns I/O, types, wiring. |

**Rules now in `docs/CLAUDE.md`** (written because Claude, Grok and Antigravity kept duplicating code across repos):
1. In `backtestgosqlite`, Go controls execution; every calculation is SQL with **no nesting**, each stage written to a **slice table** you can `SELECT`.
2. One implementation, three contexts (CLI, local web server, GAE): logic in `pkg/<capability>`, `cmd/` is a thin wrapper, differences are parameters, never a forked package.
3. One job, one name, everywhere: package = CLI command = `/cron/<job>` = `cron.yaml` entry = log prefix.
4. **No mocks, fakes, stubs or fake servers, ever.** Real SQLite and real inputs only; if a path can't be tested for real, say so and cover it with dry-run or a non-mutating broker call (e.g. Schwab `previewOrder`).

Dependency direction is one way: `trade_orchestrator` imports `schwaber` and `backtestgosqlite`; they don't import it or each other.

**Data and ops**
- Market bars in one SQLite table (`backtest_start`), daily and minute together; strategy definitions as rows in `refdata/strategies.db` (≈65,600 across `streak`, `hold`, `tree`, `markov`); results one DB per family per numbered run folder.
- `modernc.org/sqlite` (pure Go, no CGO). Yahoo with Stooq fallback; Polygon for universe, minute bars, options.
- Deploy: DO droplet → Cloud Run → **App Engine** with **Litestream** replicating the DB to GCS (FUSE mount was dropped). Secret Manager for config; token in its own GCS object. Cloudflare Worker forwards the Schwab OAuth callback.
- Tooling lessons: AI tools re-add aliases and copy functions, so the rules above are enforced by review; `antigravity` once edited every file during a rename; keep exactly one git repo (nested repos broke in 2025); LFS for big DBs; cap grid sizes.

---

## 7. Open items and what I did not verify

**Biggest open questions**
- Does any strategy make money live, net of slippage and fees? (Only ~3 weeks of live data, and it lost $702.)
- Do the clean-stack leaders (AMD, LITE, MULL + TECL streak) survive when judged only on years they weren't picked in?
- Make the live exit logic identical to the backtest, then re-run the reconciliation.
- The next-dev-cycle plan: deploy fixes, keep dry-run or 1 share for several weeks, add risk limits and alerts and an allowlist gate, re-run backtests with costs, scale only what survives, using money I can afford to lose.

**Caveats on this document**
- I did not run any code. Numbers come from docs, commit messages and the local result DBs (some of which are early runs; `biggest-winner` results in particular may predate the weekly rebuild).
- The older 2025 repos have thin write-ups; for `crisis-winner`, the SPXL clustering work in `backtest-sell-limit`, and the covered-call work, I found strategy descriptions but no recorded conclusions, so none are claimed here.
- IBKR statement reconstruction has stated limits (no account-value statement; two splits adjusted; one symbol marked at last fill; options only in cash).
- `backtest_reports` (GitHub) holds the generated DBs and HTML reports behind several of these numbers.
- Omitted on purpose: account numbers, server addresses, key material.
