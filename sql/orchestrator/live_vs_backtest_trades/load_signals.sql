SELECT id, strategy_id, symbol, as_of_date, next_session,
		close, buy_limit, take_profit, stop_loss, hold_days, status,
		COALESCE(notes, ''), COALESCE(staged_order_id, 0)
		FROM scan_market_buy_signal
