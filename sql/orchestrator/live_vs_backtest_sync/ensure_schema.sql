CREATE TABLE IF NOT EXISTS live_vs_backtest_deployed (
	strategy_id TEXT NOT NULL,
	pass TEXT NOT NULL,
	run INTEGER NOT NULL,
	source_path TEXT NOT NULL,
	source_size INTEGER NOT NULL,
	source_mtime TEXT NOT NULL,
	archive_path TEXT NOT NULL,
	trades INTEGER NOT NULL DEFAULT 0,
	win_rate REAL,
	window_start TEXT, window_end TEXT,
	note TEXT NOT NULL DEFAULT '',
	synced_at TEXT NOT NULL,
	PRIMARY KEY (strategy_id, pass)
);
CREATE TABLE IF NOT EXISTS live_vs_backtest_deployed_trade (
	strategy_id TEXT NOT NULL,
	pass TEXT NOT NULL,
	symbol TEXT NOT NULL DEFAULT '',
	exit_date TEXT NOT NULL DEFAULT '',
	win INTEGER NOT NULL,
	return_pct REAL
);
CREATE INDEX IF NOT EXISTS idx_live_vs_backtest_deployed_trade ON live_vs_backtest_deployed_trade(strategy_id, pass, exit_date)
