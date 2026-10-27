-- Statements of pkg/orchestrator/weekly_digest, read with orchestratorsql.Q("weekly_digest/<name>").
-- Each starts with a "-- name:" line.

-- name: build
SELECT status, COUNT(*) FROM stage_order WHERE staged_at >= ? GROUP BY status ORDER BY status

-- name: build_2
SELECT status, COUNT(*) FROM submit_order WHERE submitted_at >= ? GROUP BY status ORDER BY status

-- name: build_3
SELECT job, status, COUNT(*), MAX(CASE WHEN status = 'FAILED' THEN summary ELSE '' END)
FROM job_run WHERE finished_at >= ? GROUP BY job, status ORDER BY job

-- name: build_4
SELECT account_number, symbol, COALESCE(long_quantity, 0) - COALESCE(short_quantity, 0),
       COALESCE(market_value, 0), COALESCE(market_value, 0) - (COALESCE(long_quantity, 0) - COALESCE(short_quantity, 0)) * COALESCE(average_price, 0),
       COALESCE(exit_date, '')
FROM account_state_position WHERE COALESCE(long_quantity, 0) <> 0 OR COALESCE(short_quantity, 0) <> 0 ORDER BY market_value DESC
