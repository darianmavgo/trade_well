UPDATE manage_exit
		SET status = ?, exit_reason = ?, exit_reason_code = ?, exit_price = ?, exit_fill_price = ?, exit_time = ?, pnl = ?, pnl_pct = ?
		WHERE id = ?
