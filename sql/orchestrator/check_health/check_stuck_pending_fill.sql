SELECT symbol, 'entry order ' || IFNULL(entry_order_id, '?') || ' accepted ' || entry_time || ' and still PENDING_FILL (provisional exit date ' || exit_date || ')'
			FROM manage_exit
			WHERE account_number = ? AND status = 'PENDING_FILL' AND entry_time <= datetime('now', '-3 days')
