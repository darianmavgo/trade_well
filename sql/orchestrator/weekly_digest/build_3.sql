SELECT job, status, COUNT(*), MAX(CASE WHEN status = 'FAILED' THEN summary ELSE '' END)
FROM job_run WHERE finished_at >= ? GROUP BY job, status ORDER BY job
