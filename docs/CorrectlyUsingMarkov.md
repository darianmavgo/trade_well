# Correctly using the Markov strategies

How to get the same Markov results in the in-sample backtest, the out-of-sample (OOS) backtest and live trading. Written from the code in `backtestgosqlite` (`pkg/backtest/markov_strategy`, `pkg/backtest/train`, `sql/stages/markov_*`, `sql/strategies/markov_model`) and `trade_orchestrator` (`scripts/export_strategies.sh`, `pkg/orchestrator/strategies`, `pkg/orchestrator/scan_market`, `pkg/orchestrator/ready`). The code and SQL were read; none of the commands below were run while writing this, so run the checks in section 7 on your own data.

## 1. What the model is

For one symbol, using daily closes only:

1. **Return:** `ret_20d = (close - close 20 bars ago) / close 20 bars ago` (`markov_train/02_calc_ret.sql`).
2. **State:** bull (`1`) if `ret_20d >= +5%`, bear (`-1`) if `<= -5%`, else sideways (`0`). A bar with fewer than 20 bars behind it has no state.
3. **Transition:** the state of the *next* bar (`LEAD`).
4. **Counts:** for each date and each from-state, count how many earlier transitions went to bull, to bear, and in total. The window ends one row before the current date, so a date only sees transitions that were fully known before it.
5. **Prediction row per date:** `prob_bull`, `prob_bear` and `signal = prob_bull - prob_bear`, using the counts for today's state.

A symbol needs at least 60 counted transitions to get a model (`06_calc_prediction.sql`), and at least 21 bars to be considered at all.

The prediction table `markov_prediction` and the per-symbol `markov_model_meta` (`last_date`, `trained_through`, `bars`) live in `data/markov_models.db`. A strategy never trains. It reads the saved predictions.

## 2. What a strategy row does with the model

Rows are in `refdata/strategies.db`, table `markov_strategy` (columns: `signal_symbol`, `trade_symbol`, `direction`, `target_state`, `hold_days`, `take_profit_pct`, `stop_loss_pct`, `allocation_pct`, `slippage_pct`, `next_day_limit`).

Entry on a date `D` when **all** of these hold (`markov_model/03_generate_trades.sql`):

- the model's state for `signal_symbol` on `D` equals `target_state`
- and the signal passes the threshold: bull needs `signal > 0.10`, bear needs `signal < -0.10`, sideways needs `|signal| <= 0.10`
- and `trade_symbol` has a daily bar on `D`

Signal price is the `trade_symbol` close on `D`. Take-profit is `close * (1 + take_profit_pct)`, stop is `close * (1 - stop_loss_pct)` (a `stop_loss_pct` of 0 means no stop), and the hold limit is `hold_days`.

Example, `markov_model_amd`: signal AMD, trade AMD, long, bull, hold 15 days, take-profit 5%, no stop, `allocation_pct` 0.25, `next_day_limit` 0.

## 3. Why in-sample, OOS and live can disagree

Three things have to line up. Most mismatches are one of them.

### 3.1 The training cutoff (`trained_through`)

- `train markov` **without** `-through` learns from every bar. Its counts still only look backward per date, so there is no look-ahead, but the counts keep growing every day, even through the OOS months.
- `train markov -through <date>` stops *learning* at `<date>`. Later sessions are still scored, with the counts **frozen** at `<date>`. `markov_model_meta.trained_through` records it.
- **Live is always frozen.** On every scan, `markov_strategy.Prepare` calls `train.ExtendMarkov`, which scores each new session from its 20-bar return with the counts frozen at `trained_through`. It never refits an existing model.

So the only model that behaves the same in the OOS backtest and live is one trained with `-through` set to the in-sample end. A model trained with no `-through` gives OOS numbers from continuously updated counts that live will never reproduce.

The backtest holds out the last 12 months (`DefaultHoldoutMonths = 12`). The in-sample end is the newest daily bar minus 12 months (`storage.InSampleEnd`). The OOS pass starts the day after.

### 3.2 The entry fill

The backtest fill depends on `next_day_limit` on the row:

| `next_day_limit` | Backtest fills the entry | Can live match it? |
|---|---|---|
| `1` | next session, as a limit at the signal price (the signal-day close) | Yes. The scan runs after the close and rests that limit next session |
| `0` | at the signal bar's close, the same session as the signal | No. Live learns the signal after the close, so it can only buy the next session |

Rows with `next_day_limit = 0` (the exported `markov_model_amd`, `_amuu`, `_lite`, `_mull`, `_vrtl` all have 0) are optimistic against live by up to a session of price movement. Use rows with `next_day_limit = 1` when you want a backtest that live can reproduce, or treat the difference as a known gap. Set it with SQL on `markov_strategy`; that is data, not Go. `slippage_pct` should be non-zero for any row you intend to trade (see 3.4).

A limit that does not fill is a trade live never made. The backtest models that for `next_day_limit = 1`, so its trade count can be lower than the signal count.

### 3.3 Sizing

- `allocation_pct` on the row is 0.20 for the five bundled rows (`markov_model_amd`, `_amuu`, `_lite`, `_mull`, `_vrtl`), the same as `EXPORT_ALLOC`. Other rows still say 0.25.
- The SQL gives each signal its own allocation (5% to 30% by `|signal|`). In `backtest stack` with `-alloc`, and live, that is ignored: every position is the flat fraction (`FlatAllocation`).
- `strategy export -alloc` and `EXPORT_ALLOC` (default **0.20**) size the exported bundle rows and record `alloc_pct` in `bundle_meta`.
- Rule: run `backtest stack -alloc X` and export with `EXPORT_ALLOC=X`, same `X`.

### 3.4 What `slippage_pct` is

An adverse price move on each fill, as a fraction of the fill price. A long entry pays `price * (1 + slippage_pct)`; every exit (take-profit, stop, time-up) sells at `price * (1 - slippage_pct)`. It is charged once per side, so a round trip costs about twice the value; `0.001` is 0.1% per side. It stands in for the bid/ask spread and market impact. It is not a commission (`CommissionPerShare`) and not a financing cost (`cash_yield`). Code: `slip()` in `backtestgosqlite/pkg/backtest/sim/run.go`.

## 4. Recipe: results that match in-sample, OOS and live

All commands run from `backtestgosqlite/` so `.env` and default paths resolve. Flags go before positional args.

### Step 1: current market data

```bash
./bin/market_history        # see the README for the downloader flags
```

Daily bars are derived from the hourly bars, so the hourly database is the one the models read. The 20-bar return needs the bars, and so does `Prepare` later.

### Step 2: pick the cutoff and train through it

The in-sample end is whatever the backtest will print as the cutoff. Get it from the same function the backtest uses by running `train check` (it reports the cutoff and the number of leaking models):

```bash
./bin/train check
```

Then train the models for the symbols you will trade, with that date:

```bash
./bin/train markov -through 2025-10-09 -symbols AMD,AMUU,LITE,MULL,VRTL
```

`-from <date>` also exists and limits the first session the counts may learn from. Leave it off unless you mean to.

Note: `backtestgosqlite/README.md` says only `train tree` has `-through`. That is out of date. `train markov` has `-through` and `-from` (`pkg/backtest/train/markov.go`).

If the symbol list is empty, `train markov` trains every `signal_symbol` in `markov_strategy`. That is thousands of symbols (about 13,000 rows). Always pass `-symbols`.

### Step 3: backtest

```bash
./bin/backtest -strategy markov_model_amd,markov_model_mull
./bin/backtest stack -primary ... -alloc 0.20      # for a stack; see the README
```

The run does an IN-SAMPLE pass up to the cutoff, then one OOS pass on the last 12 months into `oos/` of the run folder. Tune only on the in-sample numbers. Look at OOS once. Do not tune on it; if you change a parameter after seeing OOS, OOS is in-sample now.

`train check` and `check -markov -through <date>` verify that the models hold no data past the cutoff (section 7).

### Step 4: export to the deploy bundle with the same allocation

```bash
cd ../trade_orchestrator
EXPORT_ALLOC=0.20 scripts/export_strategies.sh "<your live allowlist>"
```

This rebuilds `pkg/strategies/deploy/{strategies,markov_models,tree_models}.db` from the models you just trained. The script fails if an allowlisted Markov strategy has no trained model. Pass the allowlist explicitly; with no argument it falls back to the one seeded in `deploy_gae/config.sh`, which may differ from the live `STRATEGY_ALLOWLIST` in the config object.

### Step 5: deploy

```bash
# then rebuild (there is no vendor/ step any more)
deploy_gae_turso/deploy.sh      # or deploy_gae/deploy.sh
```

The bundle is compiled into the binary and rewritten onto every App Engine instance at start (`placeBundled`), so an old deploy keeps old models until you redeploy.

## 5. What live does each night

The job is `scan_market`: the `/cron/scan_market` entry in `deploy_gae/cron.yaml`, 18:00 ET Sun-Thu. `Prepare` runs inside it, from `runner.RunLiveScan`; there is no separate Markov cron. `run_e2e_live` runs the same scan.

1. `scan_market` hydrates recent bars (from Turso `market_bars` when Turso is the backend), then `runner.RunLiveScan` downloads fresh daily bars for the required symbols.
2. `Prepare` runs for each Markov strategy. It:
   - reads the last bar date of the signal symbol and caps it at the last completed session (a bar for the open session never enters the model);
   - if the model's `last_date` is already that date, does nothing;
   - if the model is behind, runs `ExtendMarkov`, which scores the missing sessions with counts frozen at `trained_through`;
   - if there is no model at all, fails with the command that makes one (`train markov -symbols X`, then export the bundle). It never fits a model from the bars the instance holds, because that is not the model you backtested.
3. The Markov SQL pipeline applies the rule in section 2 and the scan stages the resulting orders.
4. `signal_trace` records, for the latest five sessions, why the strategy did or did not enter, with `trained_through` and `scored_live` in its `facts`. `scored_live = true` means the session was scored by the frozen extension, not by training.

Facts to rely on:

- A live scan needs only about 25 closes per symbol (`MinHistoryBars() = 25`) because the saved model already holds history. The extension needs the last 21 closes.
- Extended scores are not saved across an App Engine cold start (the bundle is rewritten). They are recomputed on the next scan from the market bars, so the market bars must reach back past the model's `last_date`. `/ready` shows `markov_models` as warn (catch-up pending) or bad (model too far behind what a download can cover, about 330 days).
- Because of that, **refreshing the bundle regularly is the way to stop the model trailing**. Retrain with the same `-through` (so the in-sample definition does not move) and re-export. The extension then starts from a newer `last_date`.

## 6. Why live results still differ even when all of the above matches

These are real, and a match to the cent is not the goal:

- **Fills.** A resting limit may not fill, or may fill at a better price than the signal close. The backtest assumes the modelled outcome from daily/hourly bars.
- **Data source.** Backtests read the hourly database (Alpaca or Polygon). Live on App Engine uses Alpaca daily bars (`MARKET_DATA_SOURCE=alpaca_daily`). A different vendor or an unadjusted split changes `ret_20d` and can flip a state. Splits are flagged in `runs.suspect`; check them.
- **Provisional bars.** A scan before the close sees the open session's bar. `Prepare` caps at the last completed session; do not scan early.
- **Sizing.** Live sizes from cash/equity at order time. The backtest uses its own equity curve.
- **Ordering and caps.** Position caps, the kill switch, cash checks and `park` residual cash exist live and in a stack, and can skip a signal.
- **Holding period.** Hold days count sessions in the simulator; live exits by `manage_exits`. A holiday or a missed job shifts it.
- **Slippage and fees.** Only what `slippage_pct` models.

## 7. Verify a model before you trust it (read-only)

```bash
# Cutoff, and symbols trained past it
./bin/train check

# Model rows: last_date, trained_through, bars
sqlite3 data/markov_models.db \
  "select symbol, first_date, last_date, trained_through, bars from markov_model_meta order by symbol"

# The bundle that will ship
sqlite3 ../trade_orchestrator/pkg/orchestrator/strategies/deploy/markov_models.db \
  "select symbol, last_date, trained_through from markov_model_meta"
sqlite3 ../trade_orchestrator/pkg/orchestrator/strategies/deploy/strategies.db \
  "select * from bundle_meta"

# Backtest result integrity incl. model cutoff and signal = prob_bull - prob_bear
./bin/check -results data/reports/<run_id>/markov.db -run-id <n> -markov -through 2025-10-09
```

Healthy means:

- `trained_through` equals the in-sample end you backtested with, for every symbol, in both `data/` and the bundle.
- `last_date` is at or near the last completed session after a scan (it trails in the bundle until a scan extends it).
- `bundle_meta.alloc_pct` equals the `-alloc` of the backtest.
- `train check` reports no leaks (it currently checks trees; Markov is walk-forward by construction, but `check -markov` also confirms every model has a cutoff not past `-through`).

Recomputing the signal for one date by hand is also a good check: take the saved row for the date from `markov_prediction` and confirm `signal = prob_bull - prob_bear` and the state matches your own 20-bar return.

## 8. Checklist

- [ ] Market data is current and covers every signal and trade symbol.
- [ ] `train markov -through <in-sample end> -symbols <only these>` for the symbols in your allowlist.
- [ ] `backtest` run, in-sample tuned, OOS read once.
- [ ] Rows you intend to trade have `next_day_limit = 1` and a non-zero `slippage_pct`, or you accept the entry-fill gap in 3.2.
- [ ] `backtest stack -alloc X` and `EXPORT_ALLOC=X` agree.
- [ ] `scripts/export_strategies.sh "<live allowlist>"` run, rebuilt, redeployed.
- [ ] `trained_through` in the bundle equals the cutoff; `/ready` `markov_models` is ok or a short-lived warn.
- [ ] After the first live scan, `signal_trace` shows `scored_live = true` for sessions after the cutoff and the `reason` matches what the backtest would have done on those dates.

## Where things live

| What | Where |
|---|---|
| Training stages | `backtestgosqlite/sql/stages/markov_train/` |
| Live extension stages | `backtestgosqlite/sql/stages/markov_extend/` |
| Entry rule and trace | `backtestgosqlite/sql/strategies/markov_model/` |
| Strategy, `Prepare` | `backtestgosqlite/pkg/backtest/markov_strategy/strategy.go` |
| `train markov`, `ExtendMarkov` | `backtestgosqlite/pkg/backtest/train/markov.go`, `markov_extend.go` |
| Holdout window | `backtestgosqlite/pkg/backtest/backtest/holdout.go`, `pkg/backtest/storage/sqlite.go` (`InSampleEnd`) |
| Bundle export | `trade_orchestrator/scripts/export_strategies.sh` |
| Readiness checks | `trade_orchestrator/pkg/orchestrator/ready/ready.go` (`modelsCheck`) |
| Background | `backtestgosqlite/docs/MarkovModel.md` (the original plan) |
