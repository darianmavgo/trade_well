SELECT id FROM manage_exit WHERE account_number = ? AND entry_order_id = ? AND status IN (?, ?) ORDER BY id DESC LIMIT 1
