-- Statements of pkg/orchestrator/stage_orders, read with orchestratorsql.Q("stage_orders/<name>").
-- Each starts with a "-- name:" line.

-- name: cancel_watching_order
UPDATE stage_order SET status = ?, reject_reason = ? WHERE id = ? AND status = ?

-- name: claim_staged_order_for_submit
UPDATE stage_order
SET status = ?, client_order_id = ?
WHERE id = ? AND status = ?

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS stage_order (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	signal_id INTEGER,
	account_number TEXT NOT NULL,
	strategy_name TEXT NOT NULL,
	symbol TEXT NOT NULL,
	instruction TEXT NOT NULL DEFAULT 'BUY',
	quantity REAL NOT NULL,
	entry_price REAL NOT NULL,
	take_profit_price REAL NOT NULL,
	stop_loss_price REAL NOT NULL,
	max_trading_days INTEGER NOT NULL DEFAULT 8,
	status TEXT NOT NULL DEFAULT 'PENDING',
	reject_reason TEXT,
	order_id TEXT,
	client_order_id TEXT,
	staged_at DATETIME NOT NULL,
	submitted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_stage_order_client_order_id ON stage_order(client_order_id) WHERE client_order_id IS NOT NULL AND client_order_id != '';

-- name: filled_this_month
SELECT strategy_name, strategy_id, symbol, COALESCE(entry_filled_at, entry_time)
		FROM manage_exit WHERE (account_number = ? OR ? = '')

-- name: held_symbols
SELECT symbol FROM manage_exit WHERE status = 'OPEN' AND account_number = ?

-- name: insert_staged_order
INSERT INTO stage_order (
		signal_id, account_number, strategy_name, symbol, instruction,
		quantity, entry_price, take_profit_price, stop_loss_price,
		max_trading_days, status, staged_at, entry_duration, session_date, alloc_pct
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)

-- name: latest_staged_id
SELECT id FROM stage_order WHERE account_number = ? AND strategy_name = ? AND symbol = ? ORDER BY id DESC LIMIT 1

-- name: mark_staged_order_rejected
UPDATE stage_order SET status = ?, reject_reason = ? WHERE id = ?

-- name: mark_staged_order_submitted
UPDATE stage_order SET status = ?, order_id = ?, submitted_at = ? WHERE id = ?

-- name: promote_watching_order
UPDATE stage_order SET status = ?, quantity = ? WHERE id = ? AND status = ?

-- name: release_staged_order_claim
UPDATE stage_order SET status = ? WHERE id = ? AND status = ?

-- name: reopen_rejected_order
UPDATE stage_order SET status = ?, reject_reason = NULL, client_order_id = NULL
		WHERE id = ? AND status = ? AND COALESCE(order_id, '') = ''

-- name: reopen_rejected_watch
UPDATE stage_order SET status = ?, reject_reason = NULL, client_order_id = NULL
		WHERE id = ? AND status = ?

-- name: run
UPDATE scan_market_signal SET status = 'ERROR_STAGE_ORDER' WHERE id = ?

-- name: run_2
UPDATE scan_market_signal SET status = 'STAGED' WHERE id = ?
