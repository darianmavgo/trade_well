SELECT symbol,
			       'exit date ' || CASE WHEN exit_date <> '' THEN exit_date ELSE 'unset' END
			       || ' is still ' || CASE WHEN exit_date_source = '' THEN 'unset' ELSE exit_date_source END
			       || ', not from the fill of entry order ' || IFNULL(entry_order_id, '?')
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN' AND exit_date_source <> 'fill'
			  AND entry_time <= datetime('now', '-1 day')
