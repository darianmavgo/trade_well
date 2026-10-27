-- Statements of pkg/orchestrator/risk, read with orchestratorsql.Q("risk/<name>").
-- Each starts with a "-- name:" line.

-- name: all
SELECT account_number FROM risk_state ORDER BY account_number

-- name: auto_kill_reason
SELECT status FROM job_run WHERE job = ? ORDER BY id DESC LIMIT ?

-- name: counts
SELECT COUNT(*) FROM stage_order
WHERE account_number = ? AND instruction = 'BUY'
  AND status IN ('SUBMITTING', 'SUBMITTED', 'SUBMITTED_PENDING_FILL')
  AND COALESCE(submitted_at, staged_at) >= ?

-- name: counts_2
SELECT COUNT(*) FROM account_state_position WHERE account_number = ? AND long_quantity > 0

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS risk_equity (
	account_number TEXT NOT NULL,
	session_date TEXT NOT NULL,
	equity REAL NOT NULL,
	recorded_at DATETIME NOT NULL,
	PRIMARY KEY (account_number, session_date)
);
CREATE TABLE IF NOT EXISTS risk_state (
	account_number TEXT PRIMARY KEY,
	assessed_at DATETIME NOT NULL,
	session_date TEXT NOT NULL,
	equity REAL NOT NULL,
	peak REAL NOT NULL,
	drawdown REAL NOT NULL,
	day_change REAL NOT NULL,
	gross REAL NOT NULL,
	scale REAL NOT NULL,
	halt_entries INTEGER NOT NULL,
	max_gross REAL NOT NULL,
	reasons TEXT NOT NULL
)

-- name: history
SELECT account_number, session_date, equity FROM risk_equity ORDER BY session_date DESC, account_number LIMIT ?

-- name: load
SELECT assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons,
	max_order_pct, max_orders_per_day, max_positions
FROM risk_state WHERE account_number = ?

-- name: run_job
SELECT account_number, COALESCE(liquidation_value, 0), fetched_at FROM account_state_account ORDER BY account_number

-- name: run_job_2
DELETE FROM risk_equity WHERE account_number = ? AND session_date < ?

-- name: run_job_3
SELECT equity FROM risk_equity WHERE account_number = ? AND session_date < ? ORDER BY session_date DESC LIMIT 1

-- name: run_job_4
SELECT MAX(equity) FROM risk_equity WHERE account_number = ? AND session_date <> ?

-- name: run_job_5
SELECT COALESCE(SUM(market_value), 0) FROM account_state_position WHERE account_number = ? AND long_quantity > 0

-- name: run_job_6
INSERT INTO risk_equity (account_number, session_date, equity, recorded_at) VALUES (?,?,?,?)
ON CONFLICT(account_number, session_date) DO UPDATE SET equity = excluded.equity, recorded_at = excluded.recorded_at

-- name: save
INSERT INTO risk_state (account_number, assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons,
	max_order_pct, max_orders_per_day, max_positions)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(account_number) DO UPDATE SET assessed_at=excluded.assessed_at, session_date=excluded.session_date,
	equity=excluded.equity, peak=excluded.peak, drawdown=excluded.drawdown, day_change=excluded.day_change,
	gross=excluded.gross, scale=excluded.scale, halt_entries=excluded.halt_entries, max_gross=excluded.max_gross, reasons=excluded.reasons,
	max_order_pct=excluded.max_order_pct, max_orders_per_day=excluded.max_orders_per_day, max_positions=excluded.max_positions
