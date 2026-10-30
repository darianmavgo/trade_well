# Purpose and goals: the discussion

Saved 2026-10-14. This records the conversation about why trade_well exists, the $10k-a-month goal, whether to try another asset class, and how to find a stable strategy without fooling ourselves. It follows `Purpose.md` (the summary of the last three months of work). The numbers are the ones used in the conversation; none were re-run.

---

## 1. Why we are doing all of this

Asked: "Generate Purpose.md. Scan through all the repos in trade_well: code, sql, docs we've been working on for the last 3 months and summarize why we are doing all of this."

Result: `Purpose.md`, which says the project exists to **find a short-term trading edge in volatile stocks and leveraged ETFs, prove it honestly on history, and trade it automatically with real money** in three Schwab accounts, with enough safeguards that a bug costs a few dollars. The question it answers is whether a strategy makes money live, and if not, why not.

The reply to it: *"That's all really well said and the assumptions are correct. Even with 4 leading edge AI's to help it's been a lot of work."*

---

## 2. The goal: $10k a month in gains

Asked: *"Given my goal to get to $10k a month in gains should I stay focused on stocks or is there another asset class I need to try out strategies on?"*

**The arithmetic is the binding constraint, not the asset class.**

- The three Schwab accounts hold about $80,600 in equity (ledger lines from the scan output: about $28.9k, $2.4k and $49.3k). The IBKR account is separate.
- $10k a month is $120k a year, roughly a 150% annual return on $80k.
- Capital needed for $10k a month at different returns:

| Annual return | Capital needed |
|---|---|
| 15% | $800k |
| 25% | $480k |
| 40% | $300k |
| 56% (Omnifunds all-time claim, unaudited) | about $215k |
| 150% | $80k |

- The best honest number so far is the in-sample stack at 52% CAR with a 30.7% drawdown, likely worse live.
- If gains also have to cover taxes, the target is higher (short-term gains are ordinary income in taxable accounts).

**Recommendation: do not switch or add an asset class yet.**

- The one earlier excursion, crypto momentum, lost money (30% win rate, −2.6% while BTC fell 38%). The options-overlay "+60%" was a Black-Scholes assumption.
- More universes mean more chances to overfit; about 22,000 held-out results were already searched.
- The open question is whether the edge survives live fills. About three weeks of live data exist, and live entries came in 49 to 62 bps worse than the model on next-day-limit strategies.

**If something is added, in order of fit with the engine:**

1. Micro index futures (MES, MNQ): same hourly mean-reversion work, capital efficient, near 24-hour, 60/40 tax treatment in taxable accounts. Leverage could turn a 30% drawdown into a blown account. Check what the Schwab accounts allow; the Roth almost certainly cannot.
2. Defined-risk options on names already traded (put spreads, covered calls). Covered calls were implemented with no recorded conclusion; testing needs real bid/ask data, which does not exist here yet.
3. Crypto, as a second try: the earlier test was momentum, short-horizon mean reversion is untested, and costs and weekend gaps matter.

**Suggested order of work**

- Decide whether the goal is $10k a month from trading, or $10k a month eventually. If the second, adding savings to a strategy proven live with a bounded drawdown matters more than a new asset class.
- Write down a small number of hypotheses before testing, keep the 12-month holdout, and require live one-share trading before real size.
- Run the current four strategies and the weekly live-versus-model reconciliation for 8 to 12 weeks, then add an asset class if the edge holds.

This is analysis of the project's own results, not licensed financial advice.

---

## 3. Finding a stable strategy without overfitting

Asked: *"You keep accusing me most days of overfitting by looking through gajillions of options. So what is the right way to find the strategy that is stable CAGR and minimal drawdown?"*

**The principle.** Searching many options is fine. The mistake is picking the best of thousands and judging it on the same data it was picked from, and reporting the best of N as if it were one test.

**How large the luck effect is (approximate).** With N independent trials and no real edge, the best one sits about √(2 ln N) standard errors above zero. For N = 22,000 that is about 4.5. On a one-year test the standard error of an annualized Sharpe ratio is about 1, so luck alone can produce a Sharpe near 4.5, about the size of the MULL, AMD and LITE held-out numbers. The trials are correlated, so the effective N is smaller, but the point holds.

**What to change, in order of payoff**

1. **Keep one test set that never influences a choice.** The 12-month holdout stopped being clean once MULL, AMD and LITE were chosen because they did well in it. Use three layers: search (older years), selection (the next stretch), and a final period viewed once per decision.
2. **Count the trials and correct for them.** Log every backtest. Compute a deflated Sharpe ratio, or run a null test: shuffle or block-bootstrap the data, run the same search, and check that the real winner clearly beats the 95th percentile of the noise's best-of-N.
3. **Hold out tickers, not only time.** Choose the rule on half the symbols and test on the other half. A rule that works across many similar leveraged ETFs is more believable than one that works on a single name.
4. **Pick a plateau, not a peak.** Take the config whose neighbours (hold days, take-profit, stop) also perform. If the best result needs one exact setting, discard it.
5. **Score on the worst case, not the average.** Use worst-fold drawdown, median fold CAGR, at least 100 trades, and measured costs (live entries were 49 to 62 bps worse than modelled).
6. **Build stability by combining.** Several modest, weakly correlated strategies with fixed simple weights. The current greedy stacking ranks by added equity, which brings selection back in.
7. **Treat live trading as the final test.** Compare weekly live results with the range the backtest predicts, and haircut backtest CAGR by about half when planning capital.

**A realistic target.** A long-run Calmar above 2 is already exceptional. A stable 20 to 30% CAGR with under 10% drawdown is rare, so anything much better is not a finding until it survives the steps above.

**What already exists.** Walk-forward, a strict out-of-sample gate and tiers A to D are in place (`LearningLog.md` section 4). What is missing is a truly untouched test set, a trial ledger with a null test, and plateau-based selection.

**Offered, not yet done.** Build the trial ledger and null-test command into the pipeline, then report the current best strategies against it.

---

## 4. Related files

- `Purpose.md`: the three-month summary this conversation builds on.
- `docs/LearningLog.md`: what was learned, with the validation method.
- `docs/StrategiesExplored.md`: the checklist of strategies tried and ideas still open.
- `docs/MakeMoneyNextDevCycle.html`: the guardrails still open before real size.
