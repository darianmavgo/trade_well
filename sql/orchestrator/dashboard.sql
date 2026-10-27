-- Statements of pkg/orchestrator/dashboard, read with orchestratorsql.Q("dashboard/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_db
SELECT count(*) FROM sqlite_master
