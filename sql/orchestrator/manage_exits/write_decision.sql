UPDATE manage_exit SET
		strategy_id = CASE WHEN ? != '' THEN ? ELSE strategy_id END,
		as_of_date = CASE WHEN ? != '' THEN ? ELSE as_of_date END,
		next_session = CASE WHEN ? != '' THEN ? ELSE next_session END,
		signal_id = CASE WHEN ? > 0 THEN ? ELSE signal_id END,
		buy_signal_id = CASE WHEN ? > 0 THEN ? ELSE buy_signal_id END,
		stage_order_id = CASE WHEN ? > 0 THEN ? ELSE stage_order_id END,
		entry_fill_price = CASE WHEN ? > 0 THEN ? ELSE entry_fill_price END,
		filled_qty = CASE WHEN ? > 0 THEN ? ELSE filled_qty END
		WHERE id = ?
