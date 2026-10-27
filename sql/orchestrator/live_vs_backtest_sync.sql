-- Statements of pkg/orchestrator/live_vs_backtest_sync, read with orchestratorsql.Q("live_vs_backtest_sync/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
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

-- name: run
SELECT strategy_id, pass, run, path, rel, trades, COALESCE(win_rate, 0),
		COALESCE(window_start, ''), COALESCE(window_end, ''), note FROM bs_pick ORDER BY strategy_id, pass

-- name: run_2
INSERT INTO live_vs_backtest_deployed (strategy_id, pass, run, source_path, source_size, source_mtime,
			archive_path, trades, win_rate, window_start, window_end, note, synced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(strategy_id, pass) DO UPDATE SET run = excluded.run, source_path = excluded.source_path,
			source_size = excluded.source_size, source_mtime = excluded.source_mtime, archive_path = excluded.archive_path,
			trades = excluded.trades, win_rate = excluded.win_rate, window_start = excluded.window_start,
			window_end = excluded.window_end, note = excluded.note, synced_at = excluded.synced_at

-- name: save_trades
DELETE FROM live_vs_backtest_deployed_trade WHERE strategy_id = ? AND pass = ?

-- name: save_trades_2
SELECT symbol, exit_date, win, return_pct FROM bs_pick_trades WHERE strategy_id = ? AND pass = ? ORDER BY exit_date

-- name: scan_backtest_result_asset_performance_summary
SELECT COUNT(*) FROM sqlite_master WHERE name = 'performance_summary'

-- name: scan_backtest_result_asset_performance_summary_2
SELECT strategy_id, COALESCE(total_trades, 0), win_rate, start_date, end_date FROM performance_summary

-- name: scan_backtest_result_asset_performance_summary_3
SELECT t.strategy_id, COALESCE(t.symbol, ''), COALESCE(t.exit_date, ''), CASE WHEN t.net_pnl > 0 THEN 1 ELSE 0 END, t.return_pct
		FROM trades t JOIN performance_summary p ON p.run_id = t.run_id AND p.strategy_id = t.strategy_id

-- name: status
SELECT source_path, source_size, source_mtime, run FROM live_vs_backtest_deployed WHERE strategy_id = ? AND pass = ?
