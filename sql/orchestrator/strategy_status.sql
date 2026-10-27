-- Statements of pkg/orchestrator/strategy_status, read with orchestratorsql.Q("strategy_status/<name>").
-- Each starts with a "-- name:" line.

-- name: broker_qty
SELECT account_number, symbol, COALESCE(long_quantity, 0), fetched_at FROM account_state_position

-- name: last_reason
SELECT reason FROM scan_market_trace WHERE strategy_id = ? AND is_asof = 1
		ORDER BY id DESC LIMIT 1

-- name: load_positions
SELECT id, account_number, symbol, status, quantity, COALESCE(entry_order_id, ''), entry_filled_at,
		entry_time, entry_price, target_profit_price, COALESCE(stop_loss_price, 0), max_trading_days, exit_date, exit_date_source,
		COALESCE(exit_reason, ''), exit_time, exit_reason_code
		FROM manage_exit WHERE strategy_id = ? OR strategy_name = ? ORDER BY id DESC

-- name: load_signals
SELECT id, symbol, as_of_date, next_session, status, buy_limit, take_profit, stop_loss,
		hold_days, COALESCE(staged_order_id, 0), notes FROM scan_market_buy_signal
		WHERE strategy_id = ? AND as_of_date >= ? ORDER BY id DESC

-- name: load_staged
SELECT id, account_number, symbol, status, quantity, entry_price, COALESCE(order_id, ''),
		COALESCE(reject_reason, ''), staged_at FROM stage_order WHERE strategy_name = ? AND staged_at >= ? ORDER BY id DESC

-- name: load_staged_2
SELECT a.staged_order_id, COALESCE(a.broker_order_id, ''), a.status, COALESCE(a.detail, ''), a.submitted_at
		FROM submit_order a JOIN stage_order o ON o.id = a.staged_order_id
		WHERE o.strategy_name = ? AND a.submitted_at >= ? ORDER BY a.id DESC
