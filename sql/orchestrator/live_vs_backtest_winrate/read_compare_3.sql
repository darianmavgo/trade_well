SELECT strategy_id, pass, n, COALESCE(exit_date, ''), COALESCE(symbol, ''), win, COALESCE(return_pct, 0), rate
		FROM winrate_bt_progress ORDER BY strategy_id, pass, n
