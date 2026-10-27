INSERT INTO manage_exit (
		account_number, symbol, strategy_name, entry_order_id, exit_order_id,
		entry_price, target_profit_price, stop_loss_price, quantity,
		entry_time, max_trading_days, trading_days_held, status,
		exit_date, exit_date_source, entry_filled_at,
		strategy_id, as_of_date, next_session, signal_id, buy_signal_id, stage_order_id,
		entry_fill_price, filled_qty
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
