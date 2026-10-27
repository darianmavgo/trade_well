-- Statements of pkg/orchestrator/appserve, read with orchestratorsql.Q("appserve/<name>").
-- Each starts with a "-- name:" line.

-- name: appserve_schema_ddl
CREATE TABLE IF NOT EXISTS appserve (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event TEXT,
  detail TEXT,
  occurred_at TIMESTAMP
);

-- name: log_startup
INSERT INTO appserve (event, detail, occurred_at) VALUES (?, ?, ?)

-- name: recent_e2_eruns
SELECT status, summary, finished_at FROM job_run WHERE job = ? ORDER BY id DESC LIMIT 5

-- name: recent_job_runs
SELECT job, status, finished_at FROM job_run ORDER BY id DESC LIMIT 8
