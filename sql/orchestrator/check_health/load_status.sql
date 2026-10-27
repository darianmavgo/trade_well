SELECT id, account_number, timestamp, total_positions, protected_positions, unprotected_positions,
		       orphan_orders, status, IFNULL(notes, ''), critical_findings, warning_findings
		FROM check_health_audit
		WHERE id IN (SELECT MAX(id) FROM check_health_audit GROUP BY account_number)
		ORDER BY account_number
