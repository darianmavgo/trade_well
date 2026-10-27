SELECT id, job, status, summary, rows_written, started_at, finished_at
FROM job_run WHERE job = ? ORDER BY id DESC LIMIT 1
