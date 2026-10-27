-- Statements of pkg/orchestrator/account_allowlist, read with orchestratorsql.Q("account_allowlist/<name>").
-- Each starts with a "-- name:" line.

-- name: resolve_from_db
SELECT account_number, hash_value, account_type, cash_balance, liquidation_value
		FROM account_state_account WHERE account_number = ?

-- name: resolve_from_db_2
SELECT symbol, asset_type, long_quantity, short_quantity, average_price, market_value
		FROM account_state_position WHERE account_number = ?
