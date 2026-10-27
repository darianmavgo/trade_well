SELECT p.run_at, p.account_number, p.symbol, p.quantity, p.average_price, p.strategy, p.entry_order_id,
		p.entry_fill_date, p.exit_date, p.exit_date_source, p.expected_exit_date, p.take_profit, p.expected_take_profit,
		p.stop_loss, p.expected_stop_loss, p.status, p.issues
		FROM check_health_position p
		WHERE p.run_at = (SELECT MAX(x.run_at) FROM check_health_position x WHERE x.account_number = p.account_number)
		AND p.symbol <> ''
		ORDER BY CASE p.status WHEN 'CRITICAL' THEN 0 WHEN 'WARNING' THEN 1 ELSE 2 END, p.account_number, p.symbol
