# One year compared: Omnifunds claimed, IBKR actual, Schwab deployed, and a clean stack

Window: 2025-10-02 to 2026-10-01, the 12 months the backtester holds out. The IBKR account ran from 2025-10-06 to 2026-10-01, so the held-out year is the account's year. All tables are saved in `data/ibkr_2025oct_2026_oct.db` (`claimed_performance`, `ibkr_claim_vs_actual`, `timeframe_comparison`).

## Where each number comes from

| Row | Source | Out of sample? |
|---|---|---|
| Omnifunds claimed | `data/ibkr_claim_performance_nas100,TopTech&RUSS3000.txt`, loaded into `claimed_performance`. Newer than [omnifunds_benchmark.md](omnifunds_benchmark.md) (1 year CAR 44.9% against 42.0%). | It is the vendor's figure |
| IBKR actual | The real account. `transaction_calc account` rebuilds it from the 631 statement rows in `data/U22262325.TRANSACTIONS.1Y.csv` and the daily bars. | Not a backtest: these are real trades, so there is no in-sample or out-of-sample |
| Schwab stack | **A backtest, not the Schwab account.** The stack in `STRATEGY_ALLOWLIST`, `streak-voo-buy-tecl+mara_tree+pdd_tree` run through `backtest` on $100,000. `tsll-daily-one-share` is the ops check and is left out. | Yes, the held-out pass of run 18 |
| `streak-voo-buy-tecl` alone, markov strategies, buy and hold | Held-out passes of backtests in `data/reports/1/oos` (streak, markov) and `data/reports/3/oos` (hold) | Yes, held-out passes. Each runs alone on $100,000 at its own allocation |
| Clean stacks | `backtest` in `data/reports/19`, both passes | Held-out pass is the year above, and the in-sample pass is 2021-01-04 to 2025-10-02 |

The live Schwab record was in `schwaber.db` of the old `schwaber` repo (now Turso; that file is not in this tree) (`transactions`, 38 rows from 2026-09-03 to 2026-09-21). That is about three weeks, too short for a CAR, so the Schwab row is the backtest's expectation and not a measurement.

So the CAR and max drawdown figures are held-out (out-of-sample) backtest results for every strategy row, and a measured result for the IBKR row. The IBKR CAR is annualised on calendar days over the account's own history, and a drawdown is the largest fall of the time-weighted index from its running peak.

## What the markov strategies are

`markov_model_<symbol>` is a row of `markov_strategy`: a Markov regime model per symbol, trained on the daily bars (`train markov`), that buys the symbol when it predicts the "bull" state, holds up to 15 days, takes profit at +5%, and sizes each position at 25% of equity. It uses only transitions known by each date.

- `markov_model_amd`: Advanced Micro Devices.
- `markov_model_lite`: Lumentum Holdings Inc. common stock (ticker LITE).
- `markov_model_mull`: the GraniteShares 2x Long MU Daily ETF (ticker MULL), a 2x leveraged fund on Micron. Its return is twice Micron's moves, which is why its figures are the largest. `markov_model_muu` is the same idea on Direxion's MUU.

## One year, same window

| | CAR | Max drawdown | Calmar |
|---|---|---|---|
| Omnifunds claimed | 44.9% | 29.8% | 1.51 |
| **IBKR actual (time-weighted)** | **24.9%** | **41.1%** | **0.61** |
| Schwab stack (TECL + MARA + PDD), backtest | -24.2% | 33.2% | -0.73 |
| `streak-voo-buy-tecl` alone | 18.1% | 5.3% | 3.41 |
| buy and hold QQQ | 22.6% | 12.2% | 1.85 |
| buy and hold GOOGL | 37.7% | 21.1% | 1.79 |
| buy and hold XLK | 38.3% | 16.1% | 2.38 |
| buy and hold SMH | 82.8% | 24.6% | 3.37 |
| `markov_model_amd` | 46.4% | 8.0% | 5.79 |
| `markov_model_lite` | 71.9% | 8.6% | 8.38 |
| `markov_model_mull` | 90.5% | 16.0% | 5.66 |
| **Clean stack TECL + AMD + LITE** | 183.3% | 17.2% | 10.69 |
| **Clean stack TECL + AMD + LITE + MULL** | 348.3% | 19.6% | 17.78 |
| Clean stack AMD + LITE + MULL | 361.2% | 20.1% | 17.98 |

Shorter windows (CAR, annualised): YTD claimed 72.7%, actual 25.7%. 6 mo claimed 81.0%, actual 16.7%. 3 mo claimed -27.7%, actual -66.9% (-24.3% in total, against -7.8% claimed), with a 28.5% drawdown inside the 3 months. The clean stack TECL + AMD + LITE + MULL was 284.2% YTD, 337.1% over 6 mo and 125.2% over 3 mo, with a 16.1% drawdown in the last three months.

## What it says

1. **The account did not deliver the claim.** One year: 24.9% CAR and a 41.1% drawdown against 44.9% and 29.8% claimed. Moving the deposits to the start of the day instead of the close gives 19.2% CAR and a 41.3% drawdown, so the shortfall does not depend on that choice. Ending cash matches the statement (-6,435.97).
2. **The account lost money in dollars while the return is positive.** Net deposits were $124,936 and ending equity $105,195. Most of the money arrived in June, before the 3 month drawdown, so a time-weighted return flatters what the deposits earned.
3. **The worst day was 2026-06-05 (-13.6%)**, when the account sold its semiconductor holdings (MU, AMD, ARM, MRVL) after a 13 to 17% one-day fall.
4. **Plain buy and hold beat the account on drawdown**, and except for QQQ also on CAR. QQQ returned 22.6% with a 12.2% drawdown, under a third of the account's.
5. **The deployed Schwab stack does not hold up in this window.** Its two tree members lost money alone (MARA -16.5%, PDD -28.7%) and only the TECL streak made money. The tree models were fitted on all history, which should flatter them, so the real picture is no better.

## The clean stack

Clean means no tree strategies: the three markov strategies and the TECL streak, run as one stack on one cash ledger with `backtest -strategy "a+b+c"` (run 19). Each sleeve is sized at its own allocation of the stack's current equity, so a sleeve's gains enlarge the others. Equity grew from $100,000 to about $444,000 with no margin: the largest amount invested never exceeded equity.

| Stack | Held-out CAR | Max drawdown | In-sample CAR (2021-01 to 2025-10) | In-sample drawdown |
|---|---|---|---|---|
| TECL + AMD + LITE + MULL | 339.6% | 19.6% | 52.2% | 30.7% |
| the same with `-alloc 0.25` on every sleeve | 405.5% | 19.3% | 32.9% | 25.0% |
| TECL + AMD + LITE | 178.0% | 17.2% | 47.6% | 23.7% |
| AMD + LITE + MULL | 360.2% | 20.1% | 15.7% | 25.5% |

(The first stack's 348.3% in the table above is measured to 2026-10-01 like the others, and 339.6% to 2026-10-02.)

Profit in the four-strategy stack by sleeve: MULL $143k, LITE $66k, TECL streak $65k, AMD $61k, from 155 trades.

**Read the held-out figures with care.** AMD, LITE and MULL were picked because they did well in this held-out year, out of about 22,000 held-out results, so the year flatters them. The honest test is the in-sample period, which was not used to pick them: the four-strategy stack earned 52.2% CAR with a 30.7% drawdown over 4.75 years (Calmar 1.7), close to the claimed long-run 56% and 29.8%. That is a good result but nothing like 339%. Two of the three markov strategies are weak alone in that period (AMD 6.2% and LITE 6.7% CAGR), so the stack's in-sample figure is probably helped by the sleeves compounding on each other, which I did not isolate. Dropping MULL, or the TECL streak, still gives triple-digit held-out returns, which says the year was strong for semiconductors and not that one strategy carried it.

## Other caveats

- **Trees are left out.** Their held-out results are contaminated, because a tree fitted on all history has seen the held-out months (775 trees "beat the claim", which is not believable).
- **In-sample selection fails.** Picking by in-sample results only, the best twelve strategies returned at most 13.6% held-out, none near the account's 24.9%. The earlier stacks chosen that way also failed (runs 7 and 8: -52% and +5%).
- **Liquidity and leverage.** MULL and MUU are 2x funds on one stock, so the stack leans on Micron. The four-strategy stack made 155 trades in the year, with positions up to about $125,000 each, against average daily dollar volume of $115M for MULL and $3.5B for LITE. The 19.6% drawdown is what this year produced, not a limit.
- **Reconstruction limits for the IBKR row.** There is no account value statement, so equity is cash plus positions at the close. Fills in the statement match the day's close exactly. KLAC (10 for 1) and CRWD (4 for 1) split during the year, and their shares are put on the bars' split basis. EA has no bars and is marked at its last fill. Option contracts (four fills, +$297) are in cash only.

## Reproduce

```bash
./bin/transaction_calc account -in data/U22262325.TRANSACTIONS.1Y.csv \
  -claim "data/ibkr_claim_performance_nas100,TopTech&RUSS3000.txt" -db data/ibkr_2025oct_2026_oct.db
./bin/backtest -run-id 19 -strategy "streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull"
```
