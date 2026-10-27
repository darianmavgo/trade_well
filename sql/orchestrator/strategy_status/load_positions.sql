SELECT id, account_number, symbol, status, quantity, COALESCE(entry_order_id, ''), entry_filled_at,
		entry_time, entry_price, target_profit_price, COALESCE(stop_loss_price, 0), max_trading_days, exit_date, exit_date_source,
		COALESCE(exit_reason, ''), exit_time, exit_reason_code
		FROM manage_exit WHERE strategy_id = ? OR strategy_name = ? ORDER BY id DESC
