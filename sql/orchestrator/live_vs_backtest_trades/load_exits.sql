SELECT id, account_number, symbol, strategy_name,
		COALESCE(strategy_id, ''), COALESCE(as_of_date, ''), COALESCE(next_session, ''),
		COALESCE(signal_id, 0), COALESCE(buy_signal_id, 0), COALESCE(stage_order_id, 0),
		COALESCE(entry_order_id, ''),
		COALESCE(entry_price, 0), COALESCE(entry_fill_price, 0), COALESCE(exit_fill_price, 0),
		COALESCE(exit_price, 0), COALESCE(filled_qty, 0), COALESCE(fees, 0), COALESCE(quantity, 0),
		status, COALESCE(exit_reason, ''), COALESCE(exit_reason_code, ''), COALESCE(exit_date, ''),
		COALESCE(pnl_pct, 0), entry_time, entry_filled_at, COALESCE(max_trading_days, 0)
		FROM manage_exit
