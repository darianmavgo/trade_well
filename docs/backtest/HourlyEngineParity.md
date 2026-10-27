# Daily simulator vs hourly simulator: what changed

Backtests run on `pkg/backtest/sim` through `pkg/backtest/hoursim`. The old daily-range engine (`pkg/simulator`) has been deleted; the numbers below were measured before it went, and the test that printed them now only checks the hourly run (`pkg/backtest/hoursim/realdata_test.go`). This is what the switch changed, measured on real hourly bars (`data/market_history_hourly.db`).

To see the hourly side today: `HOURSIM_REAL_STRATEGIES=streak-voo-buy-tecl,markov_model_mull go test ./pkg/hoursim -run RealData -v`.

## Results

| Strategy | Engine | Trades | Win % | Return % | Max DD % |
|---|---|---|---|---|---|
| streak-voo-buy-tecl | daily | 27 | 74.1 | 93.6 | 26.5 |
| streak-voo-buy-tecl | hourly | 27 | 74.1 | 60.5 | 26.5 |
| streak-shpu-down3 | daily | 25 | 96.0 | 61.3 | 4.2 |
| streak-shpu-down3 | hourly | 23 | 100.0 | 51.7 | 2.1 |
| markov_model_mull | daily | 88 | 88.6 | 130.5 | 18.5 |
| markov_model_mull | hourly | 88 | 88.6 | 131.0 | 18.5 |
| markov_model_amd | daily | 123 | 77.2 | 115.4 | 18.4 |
| markov_model_amd | hourly | 123 | 77.2 | 115.4 | 18.4 |
| markov_model_amuu | daily | 64 | 85.9 | 87.8 | 17.8 |
| markov_model_amuu | hourly | 64 | 85.9 | 87.8 | 17.8 |

## Why they differ

Every entry price matched. The differences are all exits and what they do to the account.

1. **Exit later in the entry session.** The daily engine cannot exit on the entry day, so a target
   touched later that day waits for the next day and fills at that day's open, often a gap
   (TECL 2025-04-09: daily exits 04-10 at 47.25, hourly exits 04-09 at 39.03). The hourly engine
   exits in the first hour after the entry hour.
2. **Compounding.** A smaller early gain means fewer shares on every later trade (about 13% fewer
   in the TECL run by mid-2025). The return gap is mostly this, on identical entries.
3. **Position cap and hour order.** SHPU 2025-09-29 and 2026-09-21: the previous position was
   still open at the 9:00 entry hour and exited at 13:00, so with a cap of 1 the new entry is
   refused. The daily engine runs exits before entries for the whole day, so it took both.

The Markov strategies (the ones on the live allowlist) barely move: they enter at the signal price and mostly leave on the hold window, so a day's range has little to mislead. The streak strategies, which exit on a take-profit hit soon after the entry, are where the daily engine overstated.

None of these is a bug in the hourly engine. Each is the daily engine seeing the whole day at once.

## Not modelled by the hourly engine

These are refused with their name (`hoursim.Supported`): margin, margin interest, ATR stops, Kelly sizing, and dividends on total-return prices. The only strategy that used them, `dividend-margin-buy-hold`, was deleted; no hold row sets `total_return`.

Trades from the hourly engine carry no MAE or MFE (`avg_mae`, `avg_mfe` read 0).
