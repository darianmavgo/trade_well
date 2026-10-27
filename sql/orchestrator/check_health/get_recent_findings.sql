SELECT id, run_at, account_number, check_name, category, severity, subject, detail
		FROM check_health_finding
		WHERE account_number = ? OR ? = ''
		ORDER BY run_at DESC, id DESC
		LIMIT ?
