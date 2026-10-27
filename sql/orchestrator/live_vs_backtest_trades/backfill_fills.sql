UPDATE manage_exit SET entry_fill_price = ?, filled_qty = CASE WHEN ? > 0 THEN ? ELSE filled_qty END
				WHERE id = ? AND entry_fill_price = 0
