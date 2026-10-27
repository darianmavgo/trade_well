-- Statements of pkg/orchestrator/killswitch, read with orchestratorsql.Q("killswitch/<name>").
-- Each starts with a "-- name:" line.

-- name: current
SELECT engaged, changed_at, changed_by, reason FROM killswitch WHERE id = 1

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS killswitch (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	engaged INTEGER NOT NULL DEFAULT 0,
	changed_at DATETIME,
	changed_by TEXT,
	reason TEXT
)

-- name: set
INSERT INTO killswitch (id, engaged, changed_at, changed_by, reason) VALUES (1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET engaged = excluded.engaged, changed_at = excluded.changed_at,
	changed_by = excluded.changed_by, reason = excluded.reason
