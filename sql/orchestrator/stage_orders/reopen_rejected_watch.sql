UPDATE stage_order SET status = ?, reject_reason = NULL, client_order_id = NULL
		WHERE id = ? AND status = ?
