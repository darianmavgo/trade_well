SELECT strategy_id, n, COALESCE(exit_date, ''), COALESCE(symbol, ''), win, COALESCE(pnl_pct, 0), wins, rate, ci_low, ci_high
		FROM winrate_progress ORDER BY strategy_id, n
