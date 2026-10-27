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
