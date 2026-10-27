CREATE TABLE IF NOT EXISTS appconfig (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	db_path TEXT NOT NULL,
	backtest_root TEXT NOT NULL,
	market_db TEXT NOT NULL,
	livescan_bin TEXT NOT NULL,
	strategy_allowlist TEXT NOT NULL,
	loaded_at DATETIME NOT NULL
);
