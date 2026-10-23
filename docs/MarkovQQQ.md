# MarkovQQQ: a hidden Markov model of QQQ regimes

How to train, test and run one hidden Markov model (HMM) on QQQ (the Nasdaq 100 ETF) so that every trading day you have a defensible answer to two questions:

1. **What regime is QQQ in as of the last completed session?**
2. **What is the regime distribution for the next trading session?**

This is not the `markov_*` strategy family described in `CorrectlyUsingMarkov.md`. That family has no hidden state: it buckets the 20-day return at ±5% and counts transitions between the buckets. Here the regime is never observed. It is inferred from returns and volatility.

## Status

Everything in this document is built and tested as of 2026-10-23. Commands were run against the real QQQ history; the output shown in section 4 is from that run.

| Step | What | Where |
|---|---|---|
| 1 | Backfill QQQ daily bars (2010 onward) | `backtestgosqlite` `daily_bars` |
| 2 | HMM math: fit, forward filter | `backtestgosqlite/pkg/hmm` |
| 3 | SQL input stage (return, realized volatility) | `sql/stages/hmm_inputs` |
| 4 | Train with a cutoff, write model tables | `train hmm` |
| 5 | Compare state counts, out-of-sample report | `train hmm compare`, `train hmm report` |
| 6 | Daily update with frozen parameters | `train hmm extend` (`ExtendHMM`) |
| 7 | Daily job | `trade_orchestrator` `regime_predict` (CLI, `/cron/regime_predict`, 17:00 ET Mon-Fri) |
| 8 | Bundle export and `/ready` check | `scripts/export_strategies.sh`, check `hmm_model` |
| 9 | Strategy family `hmm` and two QQQ strategies | `backtestgosqlite/pkg/hmm_strategy`, `sql/strategies/hmm_strategy`, section 6 |

Three things are not automated on purpose: **training** (a deliberate command, never part of the daily job or of `train all`), **deploying** the refreshed bundle (run `scripts/export_strategies.sh`, then `go mod vendor`, then deploy), and **trading** the regime (the two `hmm` strategies are not in `STRATEGY_ALLOWLIST`; section 6).

## 1. The data: QQQ daily bars  (BUILT)

The hourly market database holds only 503 daily QQQ bars (2024-10 to 2026-10), too few to fit a three-state model. The HMM reads a separate, long-history store instead:

```bash
cd /Users/darianhickman/Documents/trade_well/backtestgosqlite
go build -o ./bin ./cmd/daily_bars

./bin/daily_bars -dry-run          # downloads and reports; writes nothing
./bin/daily_bars                   # replaces QQQ's history in data/daily_bars.db
```

- Defaults: `-symbols QQQ`, `-start 2010-01-01`, `-db data/daily_bars.db`.
- Source: Yahoo daily bars (dividend- and split-adjusted `adj_close`). Each row records its `source`.
- It writes only `data/daily_bars.db`, table `daily_bar(symbol, date, close, adj_close, volume, source)`. The 19 GB hourly database is never touched.
- It keeps bars up to the last completed session only. A bar for a session still in progress is dropped, because it would change by the close.
- **Every run replaces the whole history.** An adjusted series is rescaled each time a dividend is paid, so merging old and new rows would put two different price scales in one series.

Check it (as of 2026-10-22: 4,227 bars, 2010-01-04 to 2026-10-22):

```bash
sqlite3 data/daily_bars.db "
  select count(*), min(date), max(date) from daily_bar where symbol='QQQ';
  select count(*) from (select adj_close a, lag(adj_close) over (order by date) p
                        from daily_bar where symbol='QQQ')
   where p is not null and abs(ln(a/p)) > 0.15;"      -- expect 0 moves over 15%
```

Note: Yahoo is an unofficial endpoint. It can change or throttle without notice.

## 2. The math

### 2.1 What the model observes each day

For each session *t*, from `adj_close` *P*:

- **Return:** `r_t = ln(P_t / P_{t-1})`
- **Realized volatility:** `v_t` = sample standard deviation of the 20 most recent returns, `r_{t-19} … r_t`
- **Observation vector:** `x_t = (r_t, v_t)`, so D = 2.

The first 20 sessions have no `v_t` and are dropped.

### 2.2 The model

There are N = 3 hidden states `S_t ∈ {0, 1, 2}`. The model has three parts:

- `π_i = P(S_1 = i)`: the initial distribution.
- `A_ij = P(S_{t+1} = j | S_t = i)`: the transition matrix. Each row sums to 1. The expected time in state *i* is `1 / (1 − A_ii)` sessions.
- Emissions: given `S_t = i`, `x_t ~ Normal(μ_i, Σ_i)` with a full 2×2 covariance, so return and volatility may be correlated within a regime.

Free parameters: `k = (N−1) + N(N−1) + N·D + N·D(D+1)/2 = 2 + 6 + 6 + 9 = 23`.

**State labels.** States are sorted by `μ_i[0]`, the mean return. State 0 is the lowest-mean-return regime ("bear"), state 2 the highest ("bull"), state 1 the middle. This makes labels stable across refits (otherwise EM may return the same regimes in a different order). It is a ranking by return only. Always read the fitted volatility of each state too: a "sideways" state with the highest volatility is a legitimate outcome.

### 2.3 Fitting: Baum-Welch (expectation-maximization)

`pkg/hmm` `Fit` runs EM on the training bars `x_1 … x_T`. Each iteration:

1. **Forward pass.** With emission likelihoods `b_t(i) = N(x_t; μ_i, Σ_i)`:
   - `α̃_1(i) ∝ π_i b_1(i)`
   - `α̃_t(j) ∝ b_t(j) · Σ_i α̃_{t-1}(i) A_ij`
   - Each row is normalised to sum to 1, with normaliser `c_t`. The log-likelihood is `LL = Σ_t ln c_t` (plus the density shift used to avoid underflow).
2. **Backward pass.** `β̃_T(i) = 1`, then `β̃_t(i) = Σ_j A_ij b_{t+1}(j) β̃_{t+1}(j) / c_{t+1}`.
3. **Posteriors.**
   - `γ_t(i) = α̃_t(i) β̃_t(i)`: probability of state *i* at *t* given **all** training bars.
   - `ξ_t(i,j) = α̃_t(i) A_ij b_{t+1}(j) β̃_{t+1}(j) / c_{t+1}`: probability of the transition *i→j* at *t*.
4. **Re-estimate.**
   - `π_i = γ_1(i)`
   - `A_ij = Σ_t ξ_t(i,j) / Σ_j Σ_t ξ_t(i,j)`
   - `μ_i = Σ_t γ_t(i) x_t / Σ_t γ_t(i)`
   - `Σ_i = Σ_t γ_t(i)(x_t − μ_i)(x_t − μ_i)ᵀ / Σ_t γ_t(i)`, plus a tiny diagonal floor (1e-9) so it stays invertible.
5. Stop when LL gains less than `Tol` (1e-6) or after 200 iterations.

EM only finds a local maximum, so `Fit` runs a deterministic quantile start (states initialised from return terciles) plus 4 seeded random restarts and keeps the highest LL. The same data and seed always give the same model.

Guards (the fit is refused, not silently weakened):
- fewer than `10 × k` bars (230 for N = 3, D = 2) in `pkg/hmm`, and fewer than `-min-bars` (default 1,000, about four years) in-sample sessions in `train hmm`;
- any non-finite observation;
- any state that owns under 2% of the bars (`ErrDegenerate`).

Model selection: `BIC = −2·LL + k·ln(T)`. Lower is better. Fit N = 2, 3, 4 on the same in-sample bars and compare. Keep N = 3 unless another N wins clearly and its states are interpretable and stable.

### 2.4 Sample splits

Reuse the holdout convention from the markov family and backtests: `storage.DefaultHoldoutMonths = 12`, and `storage.InSampleEnd` for the in-sample end date.

| Piece | Dates | What happens |
|---|---|---|
| In-sample (IS) | 2010-01-04 … `through` | EM learns `π, A, μ, Σ` from these bars only |
| Out-of-sample (OOS) | day after `through` … latest | parameters **frozen**; only filtering runs |
| Live | each new session | still frozen; one more filtering step |

`through` defaults to the newest bar minus 12 months. As of 2026-10-22 that is about 2025-10-22. The model must never see an OOS bar during fitting. If you retrain with a later `through`, the old OOS year becomes in-sample, and you no longer have an honest OOS result for it. Keep a record of which model produced which report.

### 2.5 Predicting: the forward filter, never the smoothed path

The prediction for day *t* is the **filtered** probability:

`p_t(i) = P(S_t = i | x_1 … x_t)`

It uses only observations up to and including day *t*. Two things must never be used for a live regime:
- the smoothed posterior `γ` or the Viterbi path from the fit. These use bars *after* *t*, so a label that looks right in a chart was not knowable that day. `pkg/study/hmm_regime.go` (the old prototype) used Viterbi over all history, which is why its results should not be trusted for trading.
- parameters refit on data including the day being predicted.

Two outputs per day:

- **Regime as of day t:** `p_t` (3 probabilities), and `regime_t = argmax_i p_t(i)`.
- **Next session:** `q_{t+1}(j) = Σ_i p_t(i) A_ij`, i.e. `q_{t+1} = p_t · A`. This is a prediction: it's a forecast of what regime tomorrow's bar will be, built from today's filtered state and the transition matrix.

`pkg/hmm` `Filter` returns both (`Probs` and `Next`). A unit test checks no look-ahead: appending later bars leaves every earlier row unchanged.

### 2.6 Daily update (frozen parameters)

The live job does **not** refit. Each day it:
1. refreshes QQQ daily bars to the last completed session,
2. recomputes `r_t`, `v_t` for the new bar,
3. reruns the filter over the full series with the saved `π, A, μ, Σ`,
4. writes the new `p_t` and `q_{t+1}`.

This mirrors `ExtendMarkov` in the markov family. A day-by-day filter over the whole series equals the full-history filter by construction, so there is nothing to drift.

**Timing.** Daily bars are final only after the close (4:00 pm ET). Run the job after the close, before the evening scan (18:00 ET). Row *t* is "regime as of the close of day *t*". It is usable for decisions on session *t+1*, never for day *t* itself.

### 2.7 When to retrain

Parameters are frozen between retrains, so they age. Retrain on a schedule you choose and write down (for example, yearly, or when a check below fails), not automatically in the daily job. A retrain sets a new `through`; compare the new model against the old on identical bars before replacing it (see 4).

## 3. Commands

All paths below are relative to `/Users/darianhickman/Documents/trade_well`. Build once:

```bash
cd backtestgosqlite
go build -o ./bin ./cmd/daily_bars ./cmd/train
```

Files: bars in `data/daily_bars.db`; model in `data/hmm_models.db` (override with `-bars-db` / `-model-db`; `APP_FOLDER` moves the whole `data/` folder). Both are git-ignored.

### 3.1 Test the math  (read-only)

```bash
cd backtestgosqlite
go test ./pkg/hmm ./pkg/dailybars ./pkg/train
```

The tests use seeded synthetic series with known regimes and a temp SQLite. They check: sorted labels; recovered means; probabilities summing to 1; no look-ahead; the SQL inputs against an independent Go calculation; that bars after `-through` never change the fit; and that **extending a model gives exactly the rows a full train with the same cutoff gives**.

### 3.2 Refresh the data

```bash
./bin/daily_bars -dry-run     # download and report, write nothing
./bin/daily_bars              # replace QQQ's history in data/daily_bars.db
```

### 3.3 Compare state counts  (read-only)

```bash
./bin/train hmm compare                  # 2, 3 and 4 states on the same in-sample sessions
./bin/train hmm compare -states 3,4,5,6
```

### 3.4 Train  (writes data/hmm_models.db)

```bash
./bin/train hmm -dry-run                 # fit and print, save nothing
./bin/train hmm                          # QQQ, 3 states, newest bar minus 12 months as the cutoff
./bin/train hmm -through 2025-10-22      # explicit cutoff (the in-sample end)
./bin/train hmm -states 3 -seed 1        # same data and seed always give the same model
```

Flags: `-symbols` (default QQQ), `-states` (2 to 6), `-through`, `-from`, `-holdout-months` (12), `-min-bars` (1000), `-seed`, `-calc-dir` (keep the SQL slice tables of the build for inspection), `-dry-run`. Training **replaces** the symbol's saved model and all its predictions. Sessions after `-through` are filtered with the frozen parameters and flagged `in_sample = 0`.

### 3.5 Out-of-sample report  (read-only)

```bash
./bin/train hmm report
```

### 3.6 Daily update  (writes data/hmm_models.db)

```bash
./bin/daily_bars                         # 1. refresh the bars
./bin/train hmm extend -dry-run          # 2. preview: prints the regime and next-session odds
./bin/train hmm extend                   # 3. append predictions for the new sessions (no refit)
```

`extend` refuses a symbol with no saved model, and refuses if the bars now start on a different date than the model was fit from (a sign the history changed: retrain).

### 3.7 The deployed job  (trade_orchestrator)

```bash
cd trade_orchestrator
go build -o ./bin ./cmd/orchestrator
./bin/orchestrator regime_predict --dry-run   # downloads to a scratch folder; writes nothing
./bin/orchestrator regime_predict             # live: extends the model, saves to regime_prediction
```

It is also `GET /cron/regime_predict[?dry_run=1]`, scheduled 17:00 ET Mon-Fri in `deploy_gae/cron.yaml` and in `job_runs.Schedule`. It refreshes QQQ's bars itself, so it needs no `daily_bars` step. It **fails** (and records nothing) if the newest QQQ bar is older than the last completed session, rather than report an old regime as current. The first output line is what `job_runs` keeps:

```
OK regime_predict symbol=QQQ as_of=2026-10-22 regime=2 (98%) next=2 (94%) new_sessions=1 trained_through=2025-10-22
```

The App Engine instance's model file is ephemeral (it restarts from the bundled copy), so the durable answer is the `regime_prediction` table in the orchestrator database: one row per `symbol, as_of_date` with `regime`, `regime_prob`, `probs`, `next_regime`, `next_regime_prob`, `next_probs` (JSON arrays by state).

### 3.8 Retrain and redeploy

```bash
cd backtestgosqlite
./bin/daily_bars && ./bin/train hmm compare && ./bin/train hmm && ./bin/train hmm report

cd ../trade_orchestrator
scripts/export_strategies.sh      # re-exports the bundle, including data/hmm_models.db -> pkg/strategies/deploy/hmm_models.db
go mod vendor                     # then build, test and deploy as usual
```

`/ready` shows a `hmm_model` check: ok when the model is current; warn when it trails the last session (normal until 17:00 ET) or its parameters are over two years old; bad when the bundle has no QQQ model.

## 4. How to know it is working

Run `train hmm report` and check these on the held-out year. None prove the model will make money; they check that it describes the data honestly.

1. **Likelihood per bar.** OOS mean log-likelihood per bar should be close to in-sample. A big drop means the regimes shifted or the fit overfit.
2. **Regimes differ.** Per regime, OOS mean return and volatility should be ordered like the fitted `μ`. If regimes look the same OOS, the model has no signal.
3. **Persistence.** Observed average run length per regime should be near `1 / (1 − A_ii)`. A regime that flips daily means the emissions overlap and the labels are noise.
4. **Stability across refits.** Refit with `-through` a few months earlier or later; sorted `μ` and `A` should move only a little.
5. **Compare against a trivial rule** (price above its 200-day average) before using it for anything.

### Actual results (2026-10-23, QQQ, 3 states, learned 2010-02-02 to 2025-10-22, 3,956 sessions)

```
state  mean ret  mean vol   P(stay)   expected
0        -0.029%     1.990%    0.982    54.7d
1        +0.102%     0.666%    0.972    35.8d
2        +0.103%     1.090%    0.963    27.2d

sample          state  days   mean ret   mean vol   avg run   LL/bar
in-sample       0      985       +0.002%     1.980%     44.8d    5.928
in-sample       1      1453      +0.074%     0.664%     26.9d    8.630
in-sample       2      1518      +0.111%     1.084%     20.5d    8.095
out-of-sample   0      70        +0.116%     1.639%     35.0d    6.397
out-of-sample   1      34        -0.005%     0.827%     17.0d    8.171
out-of-sample   2      147       +0.052%     1.101%     29.4d    8.139
mean log-likelihood per bar, in-sample: 7.7518      out-of-sample: 7.6573
```

As of the close of 2026-10-22 the model says state 2 (98%), and 94% state 2 for the next session.

What this says:
- **Check 1 passes**: OOS log-likelihood per bar (7.66) is close to in-sample (7.75).
- **The regimes are volatility regimes, not bull/bear.** State 0 is high volatility (about 2% a day) with roughly zero mean return, state 1 is calm (0.67%), state 2 is moderate (1.09%). States 1 and 2 have almost the same mean return (+0.102% and +0.103%). "State 0 = bear, 2 = bull" would be a misreading: read state 0 as "stressed", 1 as "calm", 2 as "normal".
- **Because the two upper states barely differ in mean return, sorting by return does not order them stably.** A refit could swap states 1 and 2. Ordering by mean volatility instead would be stable here. This is a design choice for you to make; nothing downstream depends on it yet (see 5).
- **Persistence (check 3)**: observed average runs (44.8, 26.9, 20.5 days in-sample) are shorter than the fitted expectations (54.7, 35.8, 27.2). Runs are broken by days where the filtered argmax briefly flips; reasonable, not alarming.
- **OOS sample is thin**: only 34 days in state 1 out of sample.
- **State count.** BIC improves at every step: 2 states −58,155; 3 −61,142; 4 −62,708; 5 −64,085; 6 −65,190. It never levels off. That is not evidence for more states: the overlapping volatility feature (section 5) inflates the likelihood, and a model that always prefers more states cannot choose one. N = 3 was chosen by the plan, not by BIC.

## 5. Known limitations and open items

- **Overlapping volatility.** `v_t` is a 20-day window, so consecutive values share 19 returns and are strongly autocorrelated. The HMM assumes observations are independent given the state. This overstates confidence in a regime. The alternative is a model on returns alone, or on `ln v_t`; compare by BIC and OOS likelihood before trusting the probabilities as calibrated.
- **Probabilities are model-based, not calibrated.** `p_t` is the model's belief under its own assumptions (Gaussian emissions, a fixed transition matrix). Fat tails (the 2020-03 crash, the 2025-04 drop) are not Gaussian.
- **Regimes are not stationary.** A model trained to 2025-10 has never seen the future's regimes.
- **Labels are return ranks, and the data says that is a weak ordering.** See 2.2 and the results in section 4: the fitted regimes differ by volatility, and the two upper states have the same mean return. The `hmm` strategies avoid the problem by finding the stressed state by volatility, so a swap on refit does not change their signals (tested). Anything else that reads the state numbers is still exposed. Ordering the stored states by volatility is still an open choice.
- **BIC does not pick a state count here.** See section 4.
- **Yahoo dependency.** The daily job downloads QQQ's whole history from an unofficial endpoint each run. If it fails the job fails (it does not publish a stale regime), and `check_health` reports the missed run. The instance keeps `daily_bars.db` and the extended model in ephemeral storage; only the `regime_prediction` row is durable.
- **Two strategies use the regime, and neither is live.** See section 6. They are not in `STRATEGY_ALLOWLIST`, and their backtests are a smoke test, not evidence of an edge.
- **Rule exception.** The EM fit is Go, not SQL slice tables, because Baum-Welch is iterative. The inputs (return, volatility) and the out-of-sample report are SQL stages (`hmm_inputs`, `hmm_report`), and results go to named tables.
- **Not a trade signal by itself.** `regime_predict` places and sizes nothing. Only an `hmm` strategy in the allowlist can trade the regime, and that is a deliberate live-trading change.

## 6. Trading the regime: the `hmm` strategy family  (BUILT)

A new row-backed strategy family, `hmm`, in `backtestgosqlite` (`pkg/hmm_strategy`, SQL in `sql/strategies/hmm_strategy`, table `hmm_strategy` in `refdata/strategies.db`). It only reads the model that `train hmm` saved; it never trains or refits.

### 6.1 The rows

`hmm_symbol` is a field of the row and defaults to `QQQ` (the one model that exists); `trade_symbol` defaults to the same. Other columns: `rule`, `stress_prob`, `hold_days`, `take_profit_pct`, `stop_loss_pct`, `allocation_pct`, `cash_yield`, `slippage_pct`.

| id | rule | hold | stop | What it does |
|---|---|---|---|---|
| `hmm-calm-qqq` | `calm_filter` | 3 | 4% | Signal on every session whose filtered stressed-regime probability is under `stress_prob` (0.5). Position cap 1, so it re-checks every 3 sessions. |
| `hmm-calmdown-qqq` | `calm_down` | 10 | 5% | Signal only on the session the stressed probability falls back under 0.5 after a session at or over it. |

The exits are starting points, not tuned (gridsearch can vary hold, take-profit and stop from `strategy_family_param`).

### 6.2 Rules that matter

1. **The stressed regime is found by volatility, never by state number.** Stage `02_stress_state.sql` picks the state with the highest `mu_vol` in `hmm_params`. State numbers come from sorting by mean return, which is unstable between refits here. A test relabels the stored states and the signals do not change.
2. **Causal only.** The probability is the forward-filtered one for the session's close (`hmm_state_prob`), never the smoothed or Viterbi path.
3. **Entry is the next session's open** (`NextDayOpenEntry`), because the regime is known only at the signal session's close. Orders are market orders.
4. **A backtest only reads the model.** With no model it returns no signals, says to run `train hmm`, and creates nothing. A test confirms the model file is byte-identical after a run.
5. **Live scan.** `Prepare` downloads the symbol's daily bars and runs `ExtendHMM` with the parameters frozen at `trained_through` (the same way the out-of-sample backtest scores later sessions). Each instance does this itself, so the strategy does not depend on the instance's ephemeral model file or on the 17:00 `regime_predict` job.

Pipeline stages (one per file, slice tables): `01_schema`, `02_stress_state`, `03_stress_prob`, `04_stress_lag`, `05_generate_trades`, `06_trace` (the `signal_trace` rows a live scan shows beside ENTER or NO_SIGNAL).

### 6.3 Commands

```bash
cd backtestgosqlite
sqlite3 refdata/strategies.db < sql/seed/hmm_strategy.sql      # creates the table if needed and adds the two rows (done 2026-10-23)

# Backtest. -auto-download=false keeps it from downloading hourly bars into the market database.
./bin/backtest -strategy hmm-calm-qqq,hmm-calmdown-qqq -auto-download=false -start 2024-10-09
./bin/backtest -family hmm -auto-download=false -start 2024-10-09        # every hmm row

# Bundle the rows (the table travels with the others; -alloc 0.20 matches the bundle default)
cd ../trade_orchestrator && scripts/export_strategies.sh && go mod vendor
```

QQQ's hourly bars in the market database start 2024-10-09, so `-start` earlier than that adds nothing. The run does an in-sample pass (to the model's `trained_through`, 2025-10-22) and one held-out pass (2025-10-23 onward).

### 6.4 Results (2026-10-23, QQQ, 20% of equity per position)

| Strategy | In-sample return | Held-out return | Trades (in-sample / held-out) | Max drawdown (in / held-out) |
|---|---|---|---|---|
| `hmm-calm-qqq` | +5.20% (Sharpe 1.73) | +6.30% (Sharpe 2.18) | 55 / 44 | 2.34% / 1.79% |
| `hmm-calmdown-qqq` | +4.89% (Sharpe 7.89) | +5.47% (Sharpe 5.91) | 1 / 2 | 0.26% / 0.32% |

For scale, QQQ's own price rose about 22.8% over the in-sample window (493.17 to 605.38) and about 22.1% over the held-out one (610.60 to 745.55).

### 6.5 Findings

- **No evidence of an edge.** Returns include the 4.5% cash yield on the idle 80%, and no buy-and-hold QQQ row was run through the same simulator, so there is no like-for-like baseline. Do not read +5% against QQQ's +22%. A buy-and-hold row is the first thing to add.
- **`hmm-calmdown-qqq` is not testable on this window.** It made 1 trade in-sample and 2 held-out, and the reliability check flagged `WARN OVERFIT`. Its Sharpe is a few trades, not a result.
- **`hmm-calm-qqq` is mostly in the market** (103 idle days in-sample, 111 held-out of roughly 250 sessions), with small drawdowns. That is what a stressed-regime filter should do, but two years with few stress episodes cannot show whether it avoided any.
- **Thin history is the limit.** About two years of hourly QQQ, and the held-out year has 34 days in the calm state. Backfilling decades of hourly QQQ was ruled out, so a longer test needs the daily-bar simulation question (how to avoid same-day fills without ten times the data) answered first.
- **No regime exits live.** The backtest harness supports a signal that closes a position (`Entry` below zero), but `manage_exits` knows only take-profit, stop and hold days, and the live scan writes only LONG, BUY or ENTER signals. So a live position cannot be closed because the regime turned stressed. That is why `hmm-calm-qqq` uses a short hold and re-checks.

### 6.6 What is and is not done

- **Done and tested** (real SQLite, no mocks): signals equal an independent read of `hmm_state_prob` for both rules; `calm_down` fires only on crossings; state relabelling changes nothing; a missing model gives no signals and creates no file; the model is not modified; the row validator rejects bad symbols, rules, thresholds and sizes; `hmm_symbol` and `trade_symbol` default to QQQ; the family resolves a row by id; `signal_trace` agrees with the signals. Both repos pass `go build`, `go vet` and `go test`.
- **Not run:** the live `Prepare` path (the Yahoo download inside a scan), the real `scripts/export_strategies.sh`, a deployed scan. A scratch `strategy export` of the two rows worked.
- **Not done on purpose:** the strategies are not in `STRATEGY_ALLOWLIST`. To trade them, set it with `deploy_gae/config.sh set STRATEGY_ALLOWLIST=...` and deploy together, since one id that does not resolve stops every job.
