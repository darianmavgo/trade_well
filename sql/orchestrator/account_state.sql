-- Statements of pkg/orchestrator/account_state, read with orchestratorsql.Q("account_state/<name>").
-- Each starts with a "-- name:" line.

-- name: sync_with_progress
SELECT COUNT(*) FROM account_state_order

-- name: sync_with_progress_2
SELECT COUNT(*) FROM account_state_transaction
