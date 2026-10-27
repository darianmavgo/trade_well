INSERT INTO check_health_audit (
		account_number, timestamp, total_positions, protected_positions,
		unprotected_positions, orphan_orders, status, notes,
		critical_findings, warning_findings
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
