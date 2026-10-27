-- Statements of pkg/orchestrator/auth, read with orchestratorsql.Q("auth/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS auth (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	logged_at DATETIME NOT NULL
);

-- name: log_event
INSERT INTO auth (event, detail, logged_at) VALUES (?, ?, ?)
