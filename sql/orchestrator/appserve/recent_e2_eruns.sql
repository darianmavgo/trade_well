SELECT status, summary, finished_at FROM job_run WHERE job = ? ORDER BY id DESC LIMIT 5
