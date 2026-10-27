-- Statements of pkg/orchestrator/ready, read with orchestratorsql.Q("ready/<name>").
-- Each starts with a "-- name:" line.

-- name: cash_check
SELECT account_number, COALESCE(account_type, ''), COALESCE(available_funds, 0), COALESCE(raw_json, ''), fetched_at FROM account_state_account ORDER BY account_number

-- name: database_check
SELECT count(*) FROM sqlite_master WHERE type='table' AND name = ?

-- name: database_check_2
SELECT count(*), MAX(finished_at) FROM job_run

-- name: market_check
SELECT MAX(date) FROM market_bars WHERE symbol = ?

-- name: read_markov_model_asset_trained_through
SELECT trained_through FROM markov_model_meta WHERE symbol = ?
