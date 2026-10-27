CREATE TABLE IF NOT EXISTS trade_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	strategy_id TEXT NOT NULL,
	account_number TEXT NOT NULL,
	symbol TEXT NOT NULL,
	entry_order_id TEXT NOT NULL,
	exit_order_id TEXT NOT NULL,
	entry_time TEXT NOT NULL,
	exit_time TEXT NOT NULL,
	quantity REAL NOT NULL,
	entry_fill_price REAL NOT NULL,
	exit_fill_price REAL NOT NULL,
	fees REAL NOT NULL DEFAULT 0,
	pnl REAL NOT NULL,
	pnl_pct REAL NOT NULL,
	exit_reason_code TEXT NOT NULL DEFAULT '',
	manage_exit_id INTEGER NOT NULL,
	captured_at TEXT NOT NULL,
	UNIQUE(account_number, entry_order_id, exit_order_id)
);
CREATE INDEX IF NOT EXISTS idx_trade_history_strategy ON trade_history(strategy_id, exit_time);
