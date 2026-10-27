UPDATE manage_exit SET
		buy_signal_id = COALESCE((SELECT id FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), 0),
		strategy_id = CASE WHEN strategy_id = '' THEN COALESCE((SELECT strategy_id FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), strategy_name) ELSE strategy_id END,
		as_of_date = CASE WHEN as_of_date = '' THEN COALESCE((SELECT as_of_date FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), '') ELSE as_of_date END,
		next_session = CASE WHEN next_session = '' THEN COALESCE((SELECT next_session FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), '') ELSE next_session END
		WHERE stage_order_id > 0 AND (buy_signal_id = 0 OR strategy_id = '' OR as_of_date = '')
