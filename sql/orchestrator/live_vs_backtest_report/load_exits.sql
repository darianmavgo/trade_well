SELECT id, account_number, symbol,
		CASE WHEN COALESCE(strategy_id, '') != '' THEN strategy_id ELSE strategy_name END,
		COALESCE(entry_order_id, ''), status
		FROM manage_exit
