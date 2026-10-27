SELECT id, strategy_id, as_of_date, next_session
			FROM scan_market_buy_signal WHERE staged_order_id = ? ORDER BY id DESC LIMIT 1
