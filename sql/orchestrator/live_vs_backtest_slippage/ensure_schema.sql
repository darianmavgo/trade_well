CREATE TABLE IF NOT EXISTS live_vs_backtest_slippage (
	strategy_id TEXT PRIMARY KEY,
	fills INTEGER NOT NULL,
	avg_entry_slippage_bps REAL,
	avg_exit_slippage_bps REAL,
	worst_entry_slippage_bps REAL,
	avg_entry_vs_limit_bps REAL,
	avg_fill_delay_sessions REAL,
	total_fees REAL NOT NULL DEFAULT 0,
	built_at TEXT NOT NULL
)
