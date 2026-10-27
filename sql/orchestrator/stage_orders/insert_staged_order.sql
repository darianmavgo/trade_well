INSERT INTO stage_order (
		signal_id, account_number, strategy_name, symbol, instruction,
		quantity, entry_price, take_profit_price, stop_loss_price,
		max_trading_days, status, staged_at, entry_duration, session_date, alloc_pct
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
