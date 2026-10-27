-- The live_vs_backtest view for the CLI, worst verdict first.
SELECT strategy_id, is_win_rate, COALESCE(is_trades, 0), oos_win_rate, COALESCE(oos_trades, 0), all_win_rate, COALESCE(all_trades, 0),
	base_win_rate, COALESCE(base_kind, ''), live_trades, COALESCE(live_wins, 0), live_win_rate, live_ci_low, live_ci_high,
	COALESCE(first_close, ''), COALESCE(last_close, ''), live_minus_base, verdict
FROM live_vs_backtest
ORDER BY CASE verdict WHEN 'BELOW' THEN 0 WHEN 'IN_LINE' THEN 1 WHEN 'ABOVE' THEN 2 WHEN 'TOO_EARLY' THEN 3 ELSE 4 END,
	live_trades DESC, strategy_id;
