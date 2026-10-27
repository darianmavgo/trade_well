SELECT f.id, f.run_at, f.account_number, f.check_name, f.category, f.severity, IFNULL(f.subject, ''), f.detail
		FROM check_health_finding f
		WHERE f.run_at = (SELECT MAX(x.run_at) FROM check_health_finding x WHERE x.account_number = f.account_number)
		ORDER BY CASE f.severity WHEN 'CRITICAL' THEN 0 ELSE 1 END, f.account_number, f.check_name, f.subject
		LIMIT 200
