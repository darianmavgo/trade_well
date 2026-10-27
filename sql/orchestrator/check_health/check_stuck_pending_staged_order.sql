SELECT symbol, 'staged ' || staged_at || ' still PENDING'
			FROM stage_order
			WHERE account_number = ? AND status = 'PENDING' AND staged_at <= datetime('now', '-2 days')
