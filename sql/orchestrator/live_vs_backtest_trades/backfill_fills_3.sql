UPDATE manage_exit SET stage_order_id = (
		SELECT id FROM stage_order WHERE order_id = manage_exit.entry_order_id ORDER BY id DESC LIMIT 1
	) WHERE stage_order_id = 0 AND entry_order_id != ''
		AND EXISTS (SELECT 1 FROM stage_order WHERE order_id = manage_exit.entry_order_id)
