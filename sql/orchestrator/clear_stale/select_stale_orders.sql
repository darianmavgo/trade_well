SELECT id, account_number, symbol, strategy_name, IFNULL(reject_reason, ''), staged_at
		FROM stage_order
		WHERE status = ?
		ORDER BY id
