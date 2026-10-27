SELECT strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate, COALESCE(all_trades, 0), all_win_rate,
		COALESCE(is_start, ''), COALESCE(is_end, ''), COALESCE(oos_start, ''), COALESCE(oos_end, ''), COALESCE(evaluated_at, ''), COALESCE(all_evaluated_at, '')
		FROM live_vs_backtest_winrate
		WHERE (is_win_rate IS NOT NULL OR oos_win_rate IS NOT NULL OR all_win_rate IS NOT NULL)
		ORDER BY strategy_id
