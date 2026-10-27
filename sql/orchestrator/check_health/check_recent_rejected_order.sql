SELECT symbol, 'rejected: ' || IFNULL(reject_reason, 'unknown reason') || ' (staged ' || staged_at || ')'
			FROM stage_order
			WHERE account_number = ? AND status = 'REJECTED' AND staged_at >= datetime('now', '-3 days')
