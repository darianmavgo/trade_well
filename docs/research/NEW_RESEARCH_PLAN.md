# New Research Plan: Backtests & Studies

This document details the architectural approach for integrating 11 new research ideas into the `backtestgosqlite` platform.

The platform distinguishes between **Strategies** (`pkg/backtest/strategy/`, standard backtesting with entries/exits/equity curves) and **Studies** (`pkg/backtest/study/`, ad-hoc statistical analysis, correlation, reporting). For data ingestion, it uses `pkg/backtest/datasource/` and `cmd/market_history`.

## 1. Buy Biggest Winner (Annual Hold)
**Concept:** Buy and hold for a year the biggest winner of the previous year (beats SP500 most years).
**Type:** `pkg/backtest/strategy`
**Implementation Plan:**
- Create `pkg/backtest/strategy/biggest_winner.go`.
- The strategy will scan all symbols in the provided universe at the end of every calendar year.
- It calculates the `(Close_Dec31 - Close_Jan01) / Close_Jan01` return for every symbol in the prior year.
- It generates a `models.Signal` with `OrderType: "market"` to buy the top performer on the first trading day of the new year, and a sell signal on the last trading day.

## 2. Build Market Context with Clustering
**Concept:** Build market context with clustering (grouping stocks/days by behavior).
**Type:** `pkg/backtest/study` (Ad-hoc study)
**Implementation Plan:**
- Create `pkg/backtest/study/market_clustering.go`.
- This requires extracting features (e.g., daily return, volatility, RSI) across the universe.
- Given Go lacks native SciPy/scikit-learn, we can either:
  1. Port a simple K-Means clustering algorithm to Go.
  2. Have the study write a feature matrix to an isolated SQLite DB, and provide a companion Python script (`scripts/cluster.py`) to run the scikit-learn clustering and write the cluster IDs back into the DB.
- The study outputs `data/reports/market_clustering.db` containing `cluster_assignments` and `cluster_centroids`.

## 3. Downscale Biggest Winner to Each Week
**Concept:** Evaluate the biggest winner of the previous week and hold for a week.
**Type:** `pkg/backtest/strategy`
**Implementation Plan:**
- Expand the `biggest_winner.go` strategy (from Item 1) to accept a `RebalancePeriod` in its config (e.g., `"annual"`, `"weekly"`).
- If `"weekly"`, it calculates Friday-to-Friday returns, entering the top stock on Monday open and exiting Friday close.
- This will allow grid-searching the rebalance period.

## 4. Build a Profile of Each Trading Week (Cyclical Stocks)
**Concept:** Profile each trading week year-over-year to detect seasonal/cyclical stocks.
**Type:** `pkg/backtest/study`
**Implementation Plan:**
- Create `pkg/backtest/study/weekly_seasonality.go`.
- It iterates over the historical data, grouping data by `ISO Week Number` (1-52).
- For each stock, it calculates the historical probability of a positive return for that specific week number, and the average return.
- Output: `data/reports/weekly_seasonality.db` with a view showing the most historically reliable stocks for each week of the year.

## 5. Minute-Bar Cycle Detection (Polygon)
**Concept:** Same cycle detection but using minute bars from Polygon.
**Type:** `pkg/backtest/study`
**Implementation Plan:**
- This relies on downloading minute bars via `cmd/market_history -source polygon -timeframe 1m`.
- The `weekly_seasonality.go` study can be parameterized to accept a timeframe and cycle definition (e.g., "minute of the day" 0-389 for a trading session).

## 6. Morningstar Feed & 5-Star Stocks Drawdown Reduction
**Concept:** Buy Morningstar feed, see if 5-star stocks reduce drawdown risk.
**Type:** Data Ingestion + `pkg/backtest/strategy`
**Implementation Plan:**
- **Data:** Requires a new implementation in `pkg/backtest/datasource/morningstar.go` that writes ratings into SQLite.
- The ratings need to be stored chronologically (as a stock's rating changes over time) in a new table in `market_history.db` or `settings.db`.
- **Strategy:** Create `pkg/backtest/strategy/morningstar_defense.go`, which acts as a regime filter: only take long signals if the stock is currently 5-star. Compare its equity curve's Max Drawdown against a baseline.

## 7. Score Stocks on 5 Most Different Fundamentals
**Concept:** Use 5 distinct fundamental metrics to score stocks.
**Type:** Data Ingestion + `pkg/backtest/study`
**Implementation Plan:**
- **Data:** Similar to Morningstar, fundamentals (P/E, Debt/Equity, FCF, etc.) must be sourced (e.g., Polygon Fundamentals API) and saved locally.
- **Study:** A new study `fundamental_scoring.go` that runs a PCA (Principal Component Analysis) or correlation matrix to prove which 5 metrics are the "most different" (orthogonal), then outputs a combined rank score for every ticker.

## 8. GOOGL: Search for 5% Drop Prediction Signal
**Concept:** Invest in GOOGL, search for a signal that predicts it's about to drop 5% in one day.
**Type:** `pkg/backtest/study` -> `pkg/backtest/strategy`
**Implementation Plan:**
- **Study:** First, run the existing `gain_5pct_frequency` study (modified for a -5% loss) to see how often it actually happens.
- **Study:** Create `pkg/backtest/study/googl_drop_predictor.go` that looks at the day *before* every historical -5% drop in GOOGL and aggregates technical indicators (RSI, Bollinger Band width, Volume surge).
- **Strategy:** Once a pattern is identified, implement it as a strategy (e.g., `googl-short-drop`) to backtest.

## 9. Study Small Publicly Traded Companies Acquired
**Concept:** Study behavior of small public companies right before acquisition.
**Type:** Data Ingestion + `pkg/backtest/study`
**Implementation Plan:**
- **Data:** Requires a historical dataset of M&A events and delisted tickers.
- **Study:** `acquisition_runup.go` study that anchors on the "Announcement Date" (T=0) and analyzes volume and price action from T-30 to T-1.

## 10. GOOGL News Response Speed
**Concept:** Study how fast GOOGL responds to news.
**Type:** Data Ingestion + `pkg/backtest/study`
**Implementation Plan:**
- **Data:** Ingest historical news timestamps (Polygon News API) into a new table `news_events`.
- **Study:** `news_response.go`. Correlate minute-by-minute bars (`1m` timeframe) following the news timestamp to measure how many minutes it takes for the volume/price spike to decay.

## 11. SP500 Stock Predicting VOO (Minute Bars)
**Concept:** Which SP500 stock predicts VOO using minute bars?
**Type:** `pkg/backtest/study`
**Implementation Plan:**
- Create `pkg/backtest/study/sp500_lead_lag.go`.
- This expands on the existing `march_april_voo_gld_uten` Granger Causality study.
- It loads `1m` bars for VOO and the entire S&P 500 universe.
- For each stock, it runs a Granger Causality test (or simple cross-correlation) against VOO shifted by 1, 5, and 10 minutes.
- Output: Leaderboard of which tickers have the highest predictive power over VOO's next minute.
