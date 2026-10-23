# MarkovQQQ: a hidden Markov model of QQQ regimes

How to train, test and run one hidden Markov model (HMM) on QQQ (the Nasdaq 100 ETF) so that every trading day you have a defensible answer to two questions:

1. **What regime is QQQ in as of the last completed session?**
2. **What is the regime distribution for the next trading session?**

This is not the `markov_*` strategy family described in `CorrectlyUsingMarkov.md`. That family has no hidden state: it buckets the 20-day return at ±5% and counts transitions between the buckets. Here the regime is never observed. It is inferred from returns and volatility.

## Status: read this first

The steps below are marked by whether the code exists today. Commands marked **BUILT** were run while writing this doc. Commands marked **PLANNED** do not exist yet; their shape is the design in `~/.claude/plans/enchanted-singing-wren.md` and will change if the implementation does. Do not paste a PLANNED command expecting it to work.

| Step | What | Status |
|---|---|---|
| 1 | Backfill QQQ daily bars (2010 onward) | **BUILT** `daily_bars` |
| 2 | HMM math: fit, forward filter | **BUILT** `backtestgosqlite/pkg/hmm`, unit-tested on synthetic data |
| 3 | SQL input stage (return, realized volatility) | PLANNED |
| 4 | `train hmm` (fit with a cutoff, write model tables) | PLANNED |
| 5 | Out-of-sample report | PLANNED |
| 6 | `ExtendHMM` (daily update with frozen parameters) | PLANNED |
| 7 | `regime_predict` daily job in `trade_orchestrator` | PLANNED |
| 8 | Bundle export and `/ready` check | PLANNED |

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
- fewer than `10 × k` bars (230 for N = 3, D = 2);
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

### 3.1 Build and unit-test the math  (BUILT)

```bash
cd /Users/darianhickman/Documents/trade_well/backtestgosqlite
go build ./... && go vet ./pkg/hmm && go test ./pkg/hmm ./pkg/dailybars
```

The `pkg/hmm` tests use seeded synthetic series with known regimes: they check sorted labels, recovered means, probabilities summing to 1, determinism for a seed, and the no-look-ahead property.

### 3.2 Train  (PLANNED, not built)

Design: a `train hmm` family beside `train markov`, reading `daily_bar` for QQQ, with `-through` defaulting to the newest bar minus 12 months. It would write `data/hmm_models.db` (`hmm_model_meta`, `hmm_params`, `hmm_transition`, `hmm_prediction`).

```bash
# PLANNED: the flags are the design, not a working interface
./bin/train hmm -symbols QQQ -states 3                      # live by default
./bin/train hmm -symbols QQQ -states 3 -through 2025-10-22  # explicit cutoff
```

### 3.3 Compare state counts  (PLANNED)

Fit N = 2, 3, 4 on the same in-sample bars and tabulate `LL`, `BIC` and each state's occupancy.

### 3.4 Daily prediction  (PLANNED)

```bash
# PLANNED in trade_orchestrator: pkg/regime_predict, same name as CLI, /cron/regime_predict and job_runs
orchestrator regime_predict --dry-run     # previews, leaves the database untouched
orchestrator regime_predict               # live by default
```

## 4. How to know it is working

Run these on the held-out year, using only the frozen model. None of them prove the model will make money; they check that it is describing the data honestly.

1. **Likelihood per bar.** OOS average log-likelihood per bar should be close to the in-sample value. A big drop means the regimes shifted or the fit overfit.
2. **Regimes differ.** Per regime, OOS mean return and mean volatility should be ordered like the fitted `μ`. If the bear and bull states look the same OOS, the model has no signal.
3. **Persistence.** Observed average run length per regime should be near `1 / (1 − A_ii)`. A regime that flips every day means the emissions overlap and the labels are noise.
4. **Stability across refits.** Refit with `through` a few months earlier or later; the sorted `μ` and `A` should move only a little.
5. **Compare against a trivial rule.** For any use as a filter or risk overlay, compare to something dumb (price above its 200-day average). If the HMM doesn't beat it OOS, it adds complexity for nothing.

## 5. Known limitations and open items

- **Overlapping volatility.** `v_t` is a 20-day window, so consecutive values share 19 returns and are strongly autocorrelated. The HMM assumes observations are independent given the state. This overstates confidence in a regime. The alternative is a model on returns alone, or on `ln v_t`; compare by BIC and OOS likelihood before trusting the probabilities as calibrated.
- **Probabilities are model-based, not calibrated.** `p_t` is the model's belief under its own assumptions (Gaussian emissions, a fixed transition matrix). Fat tails (the 2020-03 crash, the 2025-04 drop) are not Gaussian.
- **Regimes are not stationary.** A model trained to 2025-10 has never seen the future's regimes.
- **Labels are return ranks.** See 2.2.
- **Yahoo dependency.** The daily job needs a daily QQQ refresh. On App Engine the local `daily_bars.db` is not available, so the store (Turso or the instance) and refresh path for the deployed job are an open design item.
- **Rule exception.** The EM fit is Go, not SQL slice tables, because Baum-Welch is iterative. The inputs (return, volatility) are planned as SQL stages, and results go to named tables.
- **Not a trade signal by itself.** Nothing here places or sizes an order. Wiring a regime into a strategy is a separate, deliberate step with its own backtest.
