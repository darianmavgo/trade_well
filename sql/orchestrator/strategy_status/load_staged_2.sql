SELECT a.staged_order_id, COALESCE(a.broker_order_id, ''), a.status, COALESCE(a.detail, ''), a.submitted_at
		FROM submit_order a JOIN stage_order o ON o.id = a.staged_order_id
		WHERE o.strategy_name = ? AND a.submitted_at >= ? ORDER BY a.id DESC
