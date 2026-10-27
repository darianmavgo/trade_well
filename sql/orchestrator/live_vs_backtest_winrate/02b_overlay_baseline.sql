-- Stage 2b: the compiled-in baseline fills in every strategy that no refresh has stored. A stored
-- row always wins, so a refresh overrides the baseline per strategy.
INSERT INTO winrate_backtest
SELECT strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate,
	all_trades, all_win_rate, all_evaluated_at, is_start, is_end, oos_start, oos_end, evaluated_at, 'bundled with this deploy'
FROM winrate_baseline
WHERE strategy_id NOT IN (SELECT strategy_id FROM winrate_backtest);
