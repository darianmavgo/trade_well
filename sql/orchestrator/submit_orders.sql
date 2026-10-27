-- Statements of pkg/orchestrator/submit_orders, read with orchestratorsql.Q("submit_orders/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS submit_order (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	staged_order_id INTEGER NOT NULL,
	broker_order_id TEXT,
	symbol TEXT NOT NULL,
	quantity REAL NOT NULL,
	status TEXT NOT NULL,
	detail TEXT,
	submitted_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_submit_order_staged ON submit_order(staged_order_id);

-- name: log_attempt
INSERT INTO submit_order (staged_order_id, broker_order_id, symbol, quantity, status, detail, submitted_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)

-- name: run
UPDATE stage_order SET entry_price = ?, take_profit_price = ?, stop_loss_price = ? WHERE id = ?

-- name: run_2
UPDATE stage_order SET stop_loss_price = ? WHERE id = ?
