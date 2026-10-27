SELECT strategy_id, pass, run, path, rel, trades, COALESCE(win_rate, 0),
		COALESCE(window_start, ''), COALESCE(window_end, ''), note FROM bs_pick ORDER BY strategy_id, pass
