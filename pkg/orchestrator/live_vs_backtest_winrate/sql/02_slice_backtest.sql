-- Stage 2: the backtest side as stored by the last refresh.
DROP TABLE IF EXISTS winrate_backtest;
CREATE TABLE winrate_backtest AS
SELECT strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate,
	all_trades, all_win_rate, all_evaluated_at, is_start, is_end, oos_start, oos_end, evaluated_at,
	'refreshed ' || synced_at AS source
FROM live_vs_backtest_winrate;
