-- Statements of pkg/orchestrator/appconfig, read with orchestratorsql.Q("appconfig/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS appconfig (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	db_path TEXT NOT NULL,
	backtest_root TEXT NOT NULL,
	market_db TEXT NOT NULL,
	livescan_bin TEXT NOT NULL,
	strategy_allowlist TEXT NOT NULL,
	loaded_at DATETIME NOT NULL
);

-- name: log_used
INSERT INTO appconfig (db_path, backtest_root, market_db, livescan_bin, strategy_allowlist, loaded_at) VALUES (?, ?, ?, ?, ?, ?)
