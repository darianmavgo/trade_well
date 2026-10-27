SELECT id, symbol, as_of_date, next_session, status, buy_limit, take_profit, stop_loss,
		hold_days, COALESCE(staged_order_id, 0), notes FROM scan_market_buy_signal
		WHERE strategy_id = ? AND as_of_date >= ? ORDER BY id DESC
