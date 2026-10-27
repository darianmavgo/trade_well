SELECT strategy_name, strategy_id, symbol, COALESCE(entry_filled_at, entry_time)
		FROM manage_exit WHERE (account_number = ? OR ? = '')
