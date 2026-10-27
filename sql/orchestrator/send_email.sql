-- Statements of pkg/orchestrator/send_email, read with orchestratorsql.Q("send_email/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS send_email (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	to_addr TEXT NOT NULL DEFAULT '',
	subject TEXT NOT NULL DEFAULT '',
	job_context TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	sent_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_send_email_sent_at ON send_email(sent_at DESC);

-- name: log_attempt
INSERT INTO send_email (to_addr, subject, job_context, status, detail, sent_at)
VALUES (?, ?, ?, ?, ?, ?)
