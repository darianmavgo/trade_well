SELECT id, symbol, entry_order_id, exit_order_id, COALESCE(exit_time, ''), entry_fill_price, exit_fill_price
		FROM manage_exit WHERE account_number = ? AND status LIKE 'CLOSED%' AND (entry_fill_price <= 0 OR exit_fill_price <= 0)
		ORDER BY id DESC LIMIT 200
