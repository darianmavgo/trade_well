CREATE TABLE live_vs_backtest_report_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE live_vs_backtest_report_note (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	severity TEXT NOT NULL,
	message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS live_vs_backtest_report_round_trip (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_number TEXT NOT NULL,
	strategy_id TEXT NOT NULL DEFAULT '',
	symbol TEXT NOT NULL,
	greenlit INTEGER NOT NULL DEFAULT 0,
	buy_order_id TEXT NOT NULL,
	sell_order_id TEXT NOT NULL DEFAULT '',
	buy_price REAL NOT NULL,
	sell_price REAL NOT NULL DEFAULT 0,
	qty REAL NOT NULL,
	fees REAL NOT NULL DEFAULT 0,
	cash_pnl REAL,
	return_pct REAL,
	opened_at TEXT NOT NULL DEFAULT '',
	closed_at TEXT NOT NULL DEFAULT '',
	manage_exit_id INTEGER NOT NULL DEFAULT 0,
	manage_status TEXT NOT NULL DEFAULT ''
);
