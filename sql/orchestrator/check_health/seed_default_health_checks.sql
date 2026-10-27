INSERT INTO check_health (name, category, severity, enabled, query, description, created_at)
			VALUES (?, ?, ?, 1, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				category = excluded.category,
				severity = excluded.severity,
				query = excluded.query,
				description = excluded.description
