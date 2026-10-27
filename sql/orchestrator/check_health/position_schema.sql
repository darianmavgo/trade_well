CREATE TABLE IF NOT EXISTS check_health_position (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_at DATETIME NOT NULL,
	account_number TEXT NOT NULL,
	symbol TEXT NOT NULL,
	quantity REAL NOT NULL,
	average_price REAL NOT NULL,
	strategy TEXT NOT NULL DEFAULT '',
	entry_order_id TEXT NOT NULL DEFAULT '',
	entry_fill_date TEXT NOT NULL DEFAULT '',
	exit_date TEXT NOT NULL DEFAULT '',
	exit_date_source TEXT NOT NULL DEFAULT '',
	expected_exit_date TEXT NOT NULL DEFAULT '',
	take_profit REAL NOT NULL DEFAULT 0,
	expected_take_profit REAL NOT NULL DEFAULT 0,
	stop_loss REAL NOT NULL DEFAULT 0,
	expected_stop_loss REAL NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	issues TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_check_health_position_run ON check_health_position(account_number, run_at);
