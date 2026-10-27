-- Live against backtest, one row per strategy, built on trade_history_win_rate (live) and the
-- stored backtest rates in live_vs_backtest_winrate. The backtest side is judged against the
-- out-of-sample rate, else the in-sample rate, else the whole-window rate; the verdict is the
-- one the win-rate comparison gives (10 closed live decisions before judging):
--   NO_BACKTEST  nothing to compare against      TOO_EARLY  fewer than 10 closed live decisions
--   BELOW        the live interval sits entirely under the backtest rate
--   ABOVE        the live interval sits entirely over it
--   IN_LINE      the backtest rate is inside the live interval
DROP VIEW IF EXISTS live_vs_backtest;
CREATE VIEW live_vs_backtest AS
SELECT COALESCE(w.strategy_id, b.strategy_id) AS strategy_id,
	b.is_win_rate, b.is_trades, b.oos_win_rate, b.oos_trades, b.all_win_rate, b.all_trades,
	COALESCE(b.oos_win_rate, b.is_win_rate, b.all_win_rate) AS base_win_rate,
	CASE WHEN b.oos_win_rate IS NOT NULL THEN 'oos' WHEN b.is_win_rate IS NOT NULL THEN 'is'
		WHEN b.all_win_rate IS NOT NULL THEN 'all' END AS base_kind,
	COALESCE(w.trades, 0) AS live_trades, w.wins AS live_wins, w.win_rate AS live_win_rate,
	w.ci_low AS live_ci_low, w.ci_high AS live_ci_high, w.first_close, w.last_close,
	w.win_rate - COALESCE(b.oos_win_rate, b.is_win_rate, b.all_win_rate) AS live_minus_base,
	CASE
		WHEN COALESCE(b.oos_win_rate, b.is_win_rate, b.all_win_rate) IS NULL THEN 'NO_BACKTEST'
		WHEN COALESCE(w.trades, 0) < 10 THEN 'TOO_EARLY'
		WHEN w.ci_high < COALESCE(b.oos_win_rate, b.is_win_rate, b.all_win_rate) THEN 'BELOW'
		WHEN w.ci_low > COALESCE(b.oos_win_rate, b.is_win_rate, b.all_win_rate) THEN 'ABOVE'
		ELSE 'IN_LINE'
	END AS verdict
FROM trade_history_win_rate w
FULL OUTER JOIN live_vs_backtest_winrate b ON b.strategy_id = w.strategy_id;
