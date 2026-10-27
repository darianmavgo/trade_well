SELECT strategy_id, verdict, greenlit, is_win_rate, COALESCE(is_trades, 0), oos_win_rate, COALESCE(oos_trades, 0),
	all_win_rate, COALESCE(all_trades, 0),
	live_win_rate, live_wins, live_n, live_fills, live_ci_low, live_ci_high, model_win_rate, model_n,
	COALESCE(base_kind, ''), live_minus_base, live_minus_model, live_avg_return, open_n, pending_n,
	COALESCE(first_close, ''), COALESCE(last_close, ''), COALESCE(evaluated_at, all_evaluated_at, ''), COALESCE(source, '')
FROM winrate_compare
WHERE greenlit OR live_n > 0 OR open_n > 0 OR pending_n > 0
ORDER BY CASE verdict WHEN 'BELOW' THEN 0 WHEN 'IN_LINE' THEN 1 WHEN 'ABOVE' THEN 2 WHEN 'TOO_EARLY' THEN 3 ELSE 4 END,
	live_n DESC, strategy_id
