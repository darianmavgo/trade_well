SELECT strategy_id, COALESCE(account_number, ''), symbol, status, exit_reason, exit_reason_code, entry_time, exit_time,
		COALESCE(quantity, 0), COALESCE(entry_price, 0), COALESCE(entry_fill_price, 0), COALESCE(exit_fill_price, 0), COALESCE(exit_price, 0), COALESCE(pnl, 0), COALESCE(pnl_pct, 0), stop_loss
		FROM winrate_recent ORDER BY exit_time DESC, strategy_id, account_number
