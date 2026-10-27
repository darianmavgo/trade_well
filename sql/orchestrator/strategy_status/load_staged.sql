SELECT id, account_number, symbol, status, quantity, entry_price, COALESCE(order_id, ''),
		COALESCE(reject_reason, ''), staged_at FROM stage_order WHERE strategy_name = ? AND staged_at >= ? ORDER BY id DESC
