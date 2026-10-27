-- Statements of pkg/orchestrator/job_runs, read with orchestratorsql.Q("job_runs/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS job_run (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	job TEXT NOT NULL,
	status TEXT NOT NULL,
	summary TEXT NOT NULL DEFAULT '',
	rows_written INTEGER NOT NULL DEFAULT 0,
	started_at DATETIME NOT NULL,
	finished_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_job_run_job_finished ON job_run(job, finished_at DESC);

-- name: last_run
SELECT id, job, status, summary, rows_written, started_at, finished_at
FROM job_run WHERE job = ? ORDER BY id DESC LIMIT 1

-- name: max_id
SELECT COALESCE(MAX(id), 0) FROM job_run WHERE job = ?

-- name: record_job_run
INSERT INTO job_run (job, status, summary, rows_written, started_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?)
