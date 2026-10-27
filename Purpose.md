# Purpose: why trade_well exists

Written 2026-10-14 from the docs, READMEs, `Todo.TODO`, the commit history of every repo (about 380 commits since 2026-07-01; the earliest repo history in this folder starts 2026-08-20) and what the live system showed on 2026-10-14. Numbers are copied from `docs/LearningLog.md`, `docs/StrategiesExplored.md`, the repos' docs and the live databases. Where something is my inference, it says so. Section 8 lists what I did not verify.

## 1. The point, in one paragraph

trade_well is a personal system for **finding a short-term trading edge in volatile stocks and leveraged ETFs, proving it honestly on history, and then trading it automatically with real money** in three Schwab accounts, with enough safety and audit trail that a bug costs a few dollars, not the account. It exists because the earlier attempts (2022 notebooks, mid-2025 Python and AI-written bots, a 2026 IBKR bridge, a year of discretionary IBKR trading that lost money in dollars) either never reached live trading, or reached it without a way to tell whether the strategy or the plumbing was at fault. Everything in the three live repos is there to answer one question reliably: **does this strategy make money live, and if not, why not?**

It is not a promise of profit. The project's own notes say so more than once: *"No engineering list gets you to steady daily profit"* (`MakeMoneyNextDevCycle`), and the live record so far is about three weeks of small trades (first real round trips 2026-09-21).

## 2. What it is trying to beat

- **The benchmark is a vendor claim, not a fact.** `backtestgosqlite/docs/omnifunds_benchmark.md` (supplied 2026-10-02): Omnifunds-style auto-trading, claimed 42% over one year and 56% a year over all history, with a 29.8% max drawdown (Calmar 1.88 all-time, 3.35 best). A stack "beats it" when it has a higher CAR and a lower drawdown over the same period. `ImitatingOmnifunds.md` reverse-engineers the described method (rank daily, hold 3 to 5 names, cap 50% per name, skip earnings) and a new strategy family imitates it. The vendor's own figures are unaudited and partly simulated, which the notes point out.
- **Buy and hold, over the same 12 months.** QQQ 22.6% CAR / 12.2% drawdown, XLK 38.3% / 16.1%, SMH 82.8% / 24.6%. Anything that cannot beat these on risk-adjusted terms is not worth the complexity.
- **The owner's own history.** The IBKR account returned 24.9% CAR with a 41.1% drawdown over 12 months to 2026-10-01 and ended below net deposits ($105,195 vs $124,936 deposited). This is the baseline "discretionary me" the automation has to improve on.
- **A target set early:** a stack with roughly 79% CAGR and drawdown under 6%. Nothing has reached it; the best stack was 79.9% CAGR with 19.7% drawdown, in-sample.

## 3. How the work is split, and why

| Repo | Role | Why it is separate |
|---|---|---|
| `backtestgosqlite` (BT) | The "brain": market data, ~65,600 strategies stored as rows in SQLite, signals computed in SQL, the hourly simulator, training, validation, scoring | Research needs heavy compute and big data (GBs of bars). The live app needs a small, cgo-free binary. |
| `schwaber` (SW) | The "hands": Schwab API client, OAuth and token sync, idempotent order submission, account mirror | The broker is the source of truth. All broker contact is in one place with safety guards (cash checks, no shorting in the Roth, price collars). |
| `trade_orchestrator` (TO) | Scheduling and sequencing on App Engine: scan, stage, submit, manage exits, health checks, email, dashboard | It decides *when* and *in what order*, and contains no strategy math and no direct broker code. |
| `backtestgo`, `backtestgoduckdb` | An hourly-simulator rewrite and a DuckDB variant | Retired 2026-10-13 (`BacktestConsolidation.md`). The hourly simulator moved into BT. |
| `duckdb`, `ibkr_api`, `scripts` | Scratch and support | Nothing production depends on them. IBKR was dropped in early September after a tool fabricated data. |

The layering rules are in `CLAUDE.md`: dependencies point one way, a helper lives in the lowest layer that needs it, and nothing is copied.

## 4. Why the code looks the way it does

Each rule below came from something that went wrong.

- **Go controls execution, SQL does the calculation, one stage per file, every result in a table.** On 2026-09-08 the notes record "I just learned my code isn't capturing all the calculations in sqlite". Math done in Go and not stored cannot be audited. A strategy is mostly rows and `.sql` files, so ~65,600 of them are data, not code.
- **No mocks, fakes or stubs, ever (Rule 4).** On 2026-09-03 an AI-written IBKR tool was found to be fabricating data. Real Schwab replies also did not match anyone's guess of them (a TSLL 400 error is the cited example). The rule is to test against real SQLite and real functions, or write no test and say so.
- **One implementation for CLI, local web server and App Engine.** The CLI is the source of truth; cron only calls it. A second code path for cron would drift, and earlier drift is why live and backtest disagreed.
- **A single hourly simulator.** The old daily simulator saw a whole day at once, could not exit on the entry day and overstated streak strategies (`HourlyEngineParity.md`: streak-voo-buy-tecl 93.6% daily vs 60.5% hourly on identical entries). It was deleted on 2026-10-13. The Markov strategies on the live allowlist barely moved.
- **Hold out the last 12 months and look once; then walk-forward; then a strict out-of-sample gate; then liquidity; then tiers; then stack; then reconcile live to the model weekly** (`LearningLog.md` section 4). Every step exists because an earlier step fooled the author: CAGR diluted by decades of idle clock (2026-09-13), identical "best" configs from a fallback optimizer (09-14), and decision trees that had never been trained (found 10-02).
- **Idempotent, re-validated order submission and a kill switch.** Duplicate orders on 2026-09-17 led to durable order intent before every HTTP call, reconciliation by listing orders instead of re-placing, re-validation against cash at submit time, `/killswitch` and `/exit_market`.
- **Exit dates from the actual fill date**, not the intended one, and a broker-versus-ledger check. A ledger that said TSLL was open after the broker sold it dismissed real signals as "already holding".
- **Hosting on App Engine with Turso as the only database**, cgo-free, with all configuration in one GCS object. This was chosen over Cloud Run and a droplet because it is cheap, scales to zero, and lets the CLI and the cloud share one database.

## 5. The last three months, in phases

1. **2026-08-20 to 09-07: the engine.** `backtestgosqlite` begins as a two-tier design (SQL screening, then a chronological Go portfolio simulator with a cash ledger). Whole-market studies, a Calmar optimizer, drawdown diagnostics. IBKR is tried and abandoned. `schwaber` and `trade_orchestrator` are split out and wired to cron and email.
2. **09-08 to 09-29: from backtest to real orders.** The first end-to-end test submits real orders to a Schwab Roth (09-08). Strategies move into SQL slice tables, then into database rows. App Engine, idempotent order intent, kill switch and market-exit tools arrive (09-17 to 09-24). The first live-versus-backtest reconciliation (09-29) shows the greenlit strategies down about $702 over a week, almost all one MARA trade.
3. **10-01 to 10-05: finding out what the models were.** Trees were never trained, Markov regimes were the best candidate, 100,000+ strategies forced one database per family, a holdout window became the default, and the Omnifunds benchmark was added. `LearningLog.md` was compiled on 10-05.
4. **10-06 to 10-10: breadth and pruning.** An Omnifunds-imitating family, pruning of losers, idle-day reports and a 2x sector-pairs rotation strategy that showed "80% with drawdown under 10%" (10-09) and was prepared for deployment. The next day's commit message reads "My find yesterday was all a lie". I found no written explanation. The same week the notes show the daily simulator overstating exits, so that is the likely connection, but it is my inference.
5. **10-11 to 10-14: consolidation and live hardening.** Retiring `backtestgo`, merging the hourly simulator and data into `backtestgosqlite`, swapping the market-data source to Alpaca daily bars, scan traces, ops and readiness pages. The rotation strategy sent about 48 buy orders per account on 10-11 and most failed with Schwab 429 rate limits or timeouts; it is no longer on the allowlist. On 10-13 the evening scan failed because the deployed code read Schwab daily history, which lags a night behind. That is fixed in code and awaiting a deploy-and-scan check, along with `clear_stale`, a per-strategy scan email, and a `/ready` check that no longer cries wolf about Markov models.

## 6. What has been learned so far

- **The edge, if there is one, is short-term mean reversion in volatile things** (drop-N-days streaks, Markov bull/bear regimes, trees on ±5% days). Momentum ideas ("market went up, buy the leveraged ETF") and several external-signal ideas have not worked.
- **A high win rate is not an edge** (Whitings Creek: 8 of 8 on 8 entries, archived).
- **Stacking uncorrelated sleeves on one cash ledger** is the one structural idea that kept paying.
- **Markov models looked best on the held-out year** (MULL 90.5% CAR / 16.0% drawdown) but were picked from ~22,000 results, and in-sample the four-strategy stack made 52.2% CAR with a 30.7% drawdown. "Semiconductors had a great year" explains much of the gap.
- **Live and model diverge in specific, fixable ways:** entries 49 to 62 bps worse than modelled on next-day-limit strategies, exits that did not match, and ledger dating bugs. The plumbing strategy `tsll-daily-one-share` matched its backtest almost exactly, which shows the pipeline can be right.

## 7. Where it stands on 2026-10-14

- **Allowlist (one stack):** `markov_model_mull`, `streak-shpu-down3`, `markov_model_amuu`, `markov_model_vrtl`.
- **Held:** AMUU in all three accounts, filled 10-14 (recorded entry $271.76, take-profit $285.35). The first live Markov position.
- **Scheduled path:** scan_market 18:00 ET, stage_orders 18:15, submit_orders next morning, manage_exits 09:35 and 15:50, ready at 09:00 and 17:30, check_health at 10:00 (times from the job_run history). The 18:00 ET scan on 10-14 is the first with the Alpaca fix deployed.
- **Open items from this stretch:** deploy and watch tonight's scan; the possible duplicate AMUU orders on 10-14 (OPEN rows with no signal or stage link, next to REJECTED staged orders for the same name); Schwab order throttling, since a burst of orders hit 429s; risk limits (position cap, daily-loss breaker) and an allowlist gate, still open per `MakeMoneyNextDevCycle`.

## 8. What I did not verify

- The performance numbers are as recorded in the docs; I did not rerun any backtest.
- The Omnifunds figures are unaudited vendor claims.
- Which of the P0 to P3 items in the older `Review.md` are still open (the source doc says the same).
- Whether the 10-09 rotation result was caused by the daily simulator. Only the commit message and the simulator-parity notes point that way.
- The contents of the production config object (reads were blocked), so I do not know whether the deployed instance has `ALPACA_KEY` and `ALPACA_SECRET`.

## 9. If you read only one thing

The system is built so that **each claim has to survive a more honest test than the last one**: a holdout year, walk-forward folds, a realistic hourly simulator, one-share live trades, and a weekly live-versus-model reconciliation. Most strategies have failed those tests, and recording that cheaply is the product. The live trading exists so that, eventually, the one remaining question (is the edge real with real fills?) gets an answer from data instead of from a good-looking backtest.
