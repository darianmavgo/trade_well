INSERT INTO live_vs_backtest_trade (
		account_number, strategy_id, symbol, as_of_date, next_session,
		buy_signal_id, signal_id, stage_order_id, manage_exit_id, entry_order_id,
		live_outcome, skip_reason, entry_limit, entry_fill_price, exit_fill_price,
		filled_qty, fees, live_exit_date, live_exit_reason, live_return_pct,
		model_outcome, model_entry_price, model_exit_price, model_exit_date, model_exit_reason, model_return_pct,
		entry_slippage_bps, exit_slippage_bps, entry_vs_limit_bps, fill_delay_sessions,
		signal_match, outcome_match, built_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
