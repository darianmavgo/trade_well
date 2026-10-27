# Migrate Go to SQL: what is left

Signals for every strategy are calculated in SQL (`strategy.RunPipeline` runs a
`sql/strategies/<name>/` pipeline). This file lists the Go that is left and why.

## Left in Go

- The simulator and its metrics: `pkg/backtest/sim` and `pkg/backtest/hoursim` (the hourly
- The dividend-adjusted price copy in `pkg/backtest/runner` (`toTotalReturnBars`, and
  dividend recovery from `AdjClose`/`Close` in `pkg/backtest/options`).
- The covered-call overlay in `pkg/backtest/options` (the `*-covered-call` ids return no
  stock signals).
- `pkg/backtest/study` (MARA, MU, HMM and the other research studies).

These are execution or research, not signal generation.

**Decision needed: say if you want the simulator moved.** It is the largest item
and the only one that is on every backtest's path.

## Notes for whoever picks this up

- The simulator is stateful order handling (cash, positions, preemption in the
  shared account). Moving it to SQL means rewriting it as slice tables per day,
  and its results must be checked against the Go version on the real market
  database before the Go is deleted.
- The dividend-adjusted copy is a per-bar scale (`AdjClose / Close`). `first_bar`
  already does the same scaling in SQL for its signal (`__TOTAL_RETURN__`), so the
  simulator would need the same scaling on the bars it reads.
- `markov_hmm` still depends on `study hmm_regime`, which fits an HMM in Go.
  Fitting by iteration is not practical in SQL. Either keep it as a study or
  delete the strategy.
- Slower pipelines worth tuning if they matter: `price_action_reclaim` (about 3
  minutes over the whole market) and `annual_winner` (about 2 minutes each).
