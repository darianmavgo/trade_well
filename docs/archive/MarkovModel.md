# Markov Regime Model Implementation Plan

> **Archived 2026-10-27.** Historical record, not maintained. Paths and statements refer to the old three-repo layout (`backtestgosqlite`, `trade_orchestrator`, `schwaber` as separate repositories, `schwaber.db`); the code is now under `pkg/backtest`, `pkg/orchestrator` and `pkg/schwaber`.

This document outlines the step-by-step plan for implementing a Markov Regime model as described in the hedge fund method (source: `UsingMarkovModels.md`). The model classifies market states, computes transition probabilities, and generates predictive trading signals without relying on traditional technical indicators.

We will build and validate this initially using **GOOGL** daily history before generalizing to the broader universe or backtesting.

## Phase 1: Regime Definition via SQL Views
**Objective:** Classify historical trading days directly in SQLite using window functions.
1. **Calculate Returns:** Create a SQL script/view that calculates the trailing 20-day return using the `LAG()` window function over `backtest_start` for GOOGL.
2. **Assign Labels:** Use a `CASE` statement to map returns to discrete regimes:
   - **Bull (1):** `Return_20d >= 0.05`
   - **Bear (-1):** `Return_20d <= -0.05`
   - **Sideways (0):** Everything else.

## Phase 2: Transition Matrix Generation in SQLite
**Objective:** Compute the 3x3 probability matrix using SQL aggregation and store it.
1. **State Pairs:** Use the `LEAD()` window function to pair today's state with tomorrow's state.
2. **Probability Calculation:** Group by `from_state` and `to_state`, count the transitions, and divide by the total occurrences of `from_state` to yield the transition probabilities.
3. **Storage:** Save this resulting 3x3 matrix into a reference table (e.g., `markov_transition_matrix` in the calc database) so that strategies can query the historical "stickiness" of the asset.

## Phase 3: Signal Generation Pipeline
**Objective:** Generate daily continuous trading signals and store them in a SQLite table for the backtester.
1. **1-Day Forecast:** Join the daily regime sequence against the `markov_transition_matrix` table on `today_state = from_state`. This instantly looks up the probabilities for tomorrow (`prob_bear`, `prob_sideways`, `prob_bull`).
2. **Compute Signal:** Calculate the continuous signal score directly in SQL:  
   `Signal = prob_bull - prob_bear`
3. **Persist Signals:** Insert these daily predictions into a table (e.g., `markov_signals(date, symbol, current_state, prob_bull, prob_bear, signal)`).

## Phase 4: Strategy Integration (`pkg/backtest/strategy`)
**Objective:** Consume the SQLite Markov signals natively in a trading strategy.
1. **SQL Strategy Pipeline:** Create a new pipeline (e.g., `sql/strategies/markov_googl/`) that queries the `markov_signals` table.
2. **Entry/Exit Rules:** Generate `LONG` or `SHORT` rows in the `signals` table based on the Markov `Signal` crossing a threshold (e.g., `Signal > 0.0`).
3. **Position Sizing:** (Optional) Use the continuous `Signal` magnitude to scale the position size (e.g., `allocation = Signal * max_allocation`).

## Phase 5: Generalization to Any Symbol
**Objective:** Expand the model to generate probabilities and signals for the entire stock universe, not just a single hardcoded asset.
1. **Universal SQL Pipeline:** Rename the pipeline from `markov_googl` to a generic `markov_model`. Update the SQL views to use `PARTITION BY symbol` in the `LAG()` and `LEAD()` window functions, allowing the database to compute regimes and transition matrices for every ticker in `market_history.db` simultaneously.
3. **Universal Signal Generation:** Output the predictions into a universal `markov_signals` table where signals for all tickers are stored, ready to be consumed by a multi-asset portfolio simulation.

## Phase 6: Walk-Forward Backtesting (Zero Lookahead)
**Objective:** Prevent lookahead bias by using rolling/expanding windows.
1. **Rolling Matrices:** Instead of a single static transition matrix derived from the entire history, compute the transition matrix *as of* each date $T$, using only data prior to $T$. 
2. **Historical Matrix Table:** Store these point-in-time matrices in a table (e.g., `markov_rolling_matrices(date, symbol, from_state, to_state, probability)`).
3. **Dynamic Signals:** Recompute the historical `markov_signals` table by joining each day's state with the point-in-time rolling matrix from $T-1$, ensuring the backtest remains strictly walk-forward.

## Phase 7: Hidden Markov Models (Future Enhancement)
**Objective:** Remove subjective labeling (-5% / +5%).
1. Since true unsupervised Hidden Markov Models (like Gaussian HMMs) require iterative expectation-maximization (EM) which is impractical in pure SQL, a Go package will compute the unsupervised regimes.
2. The Go routine will write the dynamically discovered daily regimes back into a SQLite table, allowing the rest of the SQL pipeline (Phases 2-4) to run unchanged on top of the unsupervised labels.
