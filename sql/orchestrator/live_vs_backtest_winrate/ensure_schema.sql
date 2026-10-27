CREATE TABLE IF NOT EXISTS live_vs_backtest_winrate (
	strategy_id TEXT PRIMARY KEY,
	is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT,
	is_trades INTEGER NOT NULL DEFAULT 0,
	is_win_rate REAL,
	oos_trades INTEGER NOT NULL DEFAULT 0,
	oos_win_rate REAL,
	evaluated_at TEXT,
	synced_at TEXT NOT NULL
)
