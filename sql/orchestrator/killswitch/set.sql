INSERT INTO killswitch (id, engaged, changed_at, changed_by, reason) VALUES (1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET engaged = excluded.engaged, changed_at = excluded.changed_at,
	changed_by = excluded.changed_by, reason = excluded.reason
