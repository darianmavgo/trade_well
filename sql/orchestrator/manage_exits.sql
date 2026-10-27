-- Statements of pkg/orchestrator/manage_exits, read with orchestratorsql.Q("manage_exits/<name>").
-- Each starts with a "-- name:" line.

-- name: adopt_untracked
SELECT UPPER(symbol) FROM manage_exit WHERE account_number = ? AND status IN (?, ?)

-- name: backfill_fills
SELECT id, symbol, entry_order_id, exit_order_id, COALESCE(exit_time, ''), entry_fill_price, exit_fill_price
		FROM manage_exit WHERE account_number = ? AND status LIKE 'CLOSED%' AND (entry_fill_price <= 0 OR exit_fill_price <= 0)
		ORDER BY id DESC LIMIT 200

-- name: backfill_fills_2
UPDATE manage_exit SET exit_fill_price = ? WHERE id = ?

-- name: cancel_pending
UPDATE manage_exit SET status = ?, exit_reason = ?, exit_reason_code = ? WHERE id = ? AND status = ?

-- name: cancel_pending_by_order
UPDATE manage_exit SET status = ?, exit_reason = ?, exit_reason_code = ? WHERE account_number = ? AND entry_order_id = ? AND status = ?

-- name: close_active_trade_filled
SELECT entry_price, entry_fill_price, quantity FROM manage_exit WHERE id = ?

-- name: close_active_trade_filled_2
UPDATE manage_exit
		SET status = ?, exit_reason = ?, exit_reason_code = ?, exit_price = ?, exit_fill_price = ?, exit_time = ?, pnl = ?, pnl_pct = ?
		WHERE id = ?

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS manage_exit (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_number TEXT NOT NULL,
	symbol TEXT NOT NULL,
	strategy_name TEXT NOT NULL,
	entry_order_id TEXT,
	exit_order_id TEXT,
	entry_price REAL NOT NULL,
	target_profit_price REAL NOT NULL,
	stop_loss_price REAL DEFAULT 0,
	quantity REAL NOT NULL,
	entry_time DATETIME NOT NULL,
	max_trading_days INTEGER NOT NULL DEFAULT 8,
	trading_days_held INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'OPEN',
	exit_reason TEXT,
	exit_time DATETIME,
	exit_price REAL DEFAULT 0,
	pnl REAL DEFAULT 0,
	pnl_pct REAL DEFAULT 0,
	exit_date TEXT NOT NULL DEFAULT '',
	exit_date_source TEXT NOT NULL DEFAULT '',
	entry_filled_at DATETIME,
	strategy_id TEXT NOT NULL DEFAULT '',
	as_of_date TEXT NOT NULL DEFAULT '',
	next_session TEXT NOT NULL DEFAULT '',
	signal_id INTEGER NOT NULL DEFAULT 0,
	buy_signal_id INTEGER NOT NULL DEFAULT 0,
	stage_order_id INTEGER NOT NULL DEFAULT 0,
	entry_fill_price REAL NOT NULL DEFAULT 0,
	exit_fill_price REAL NOT NULL DEFAULT 0,
	filled_qty REAL NOT NULL DEFAULT 0,
	fees REAL NOT NULL DEFAULT 0,
	exit_reason_code TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_manage_exit_status ON manage_exit(status);
CREATE INDEX IF NOT EXISTS idx_manage_exit_acc_sym ON manage_exit(account_number, symbol);

-- name: insert_sql
INSERT INTO manage_exit (
		account_number, symbol, strategy_name, entry_order_id, exit_order_id,
		entry_price, target_profit_price, stop_loss_price, quantity,
		entry_time, max_trading_days, trading_days_held, status,
		exit_date, exit_date_source, entry_filled_at,
		strategy_id, as_of_date, next_session, signal_id, buy_signal_id, stage_order_id,
		entry_fill_price, filled_qty
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)

-- name: open
SELECT id FROM manage_exit WHERE account_number = ? AND entry_order_id = ? AND status IN (?, ?) ORDER BY id DESC LIMIT 1

-- name: promote_without_fill
SELECT max_trading_days FROM manage_exit WHERE id = ?

-- name: promote_without_fill_2
UPDATE manage_exit SET status = ?, exit_date = CASE WHEN exit_date_source = ? THEN exit_date ELSE ? END WHERE id = ? AND status = ?

-- name: register_pending
SELECT id FROM manage_exit WHERE account_number = ? AND entry_order_id = ? ORDER BY id DESC LIMIT 1

-- name: reprice_closed
SELECT status, entry_price, entry_fill_price, exit_price, exit_fill_price, quantity FROM manage_exit WHERE id = ?

-- name: reprice_closed_2
UPDATE manage_exit SET exit_price = ?, pnl = ?, pnl_pct = ? WHERE id = ?

-- name: set_entry_fill_price
UPDATE manage_exit SET entry_fill_price = ? WHERE id = ?

-- name: set_exit_date
UPDATE manage_exit SET exit_date = ?, exit_date_source = ? WHERE id = ?

-- name: set_exit_order_id
UPDATE manage_exit SET exit_order_id = ? WHERE id = ?

-- name: set_fill
SELECT max_trading_days, status FROM manage_exit WHERE id = ?

-- name: set_fill_2
UPDATE manage_exit SET status = ?, entry_time = ?, entry_filled_at = ?, exit_date = ?, exit_date_source = ? WHERE id = ?

-- name: update_trading_days_held
UPDATE manage_exit SET trading_days_held = ? WHERE id = ?

-- name: write_decision
UPDATE manage_exit SET
		strategy_id = CASE WHEN ? != '' THEN ? ELSE strategy_id END,
		as_of_date = CASE WHEN ? != '' THEN ? ELSE as_of_date END,
		next_session = CASE WHEN ? != '' THEN ? ELSE next_session END,
		signal_id = CASE WHEN ? > 0 THEN ? ELSE signal_id END,
		buy_signal_id = CASE WHEN ? > 0 THEN ? ELSE buy_signal_id END,
		stage_order_id = CASE WHEN ? > 0 THEN ? ELSE stage_order_id END,
		entry_fill_price = CASE WHEN ? > 0 THEN ? ELSE entry_fill_price END,
		filled_qty = CASE WHEN ? > 0 THEN ? ELSE filled_qty END
		WHERE id = ?
