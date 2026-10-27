UPDATE scan_market_buy_signal
SET status = ?, staged_order_id = COALESCE(?, staged_order_id),
	notes = CASE WHEN ? = '' THEN notes WHEN notes = '' THEN ? ELSE notes || '; ' || ? END
WHERE id = ?
