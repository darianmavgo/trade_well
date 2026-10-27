INSERT INTO scan_market_buy_signal (
	scan_run_id, stack_id, strategy_id, strategy_name, symbol, direction,
	as_of_date, next_session, price, close, buy_limit, take_profit, stop_loss,
	allocation_pct, hold_days, stack_priority, status, staged_order_id,
	raw_json, notes, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
