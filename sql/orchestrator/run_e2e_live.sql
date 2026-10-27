-- Statements of pkg/orchestrator/run_e2e_live, read with orchestratorsql.Q("run_e2e_live/<name>").
-- Each starts with a "-- name:" line.

-- name: run
SELECT account_number, strategy_name, symbol, quantity, entry_price, status, IFNULL(reject_reason,'') FROM stage_order ORDER BY id DESC LIMIT 6

-- name: run_2
SELECT account_number, symbol, strategy_name, status, entry_time, trading_days_held, max_trading_days FROM manage_exit ORDER BY id DESC LIMIT 10

-- name: run_3
SELECT COUNT(*) FROM check_health_finding WHERE id > ?

-- name: run_4
SELECT account_number, severity, check_name, IFNULL(subject,''), detail FROM check_health_finding WHERE id > ? ORDER BY id

-- name: run_5
SELECT COUNT(*) FROM check_health_finding WHERE id > ? AND severity = 'CRITICAL'
