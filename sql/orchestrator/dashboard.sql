-- Statements of pkg/orchestrator/dashboard, read with orchestratorsql.Q("dashboard/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_db
SELECT count(*) FROM sqlite_master

-- name: log_server_start
INSERT INTO dashboard (port, started_at) VALUES (?, ?)

-- name: schema_ddl
CREATE TABLE IF NOT EXISTS dashboard (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  port TEXT,
  started_at TIMESTAMP
);
