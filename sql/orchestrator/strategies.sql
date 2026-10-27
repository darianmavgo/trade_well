-- Statements of pkg/orchestrator/strategies, read with orchestratorsql.Q("strategies/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS strategies (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	strategy_id TEXT NOT NULL,
	config_json TEXT NOT NULL,
	resolved_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_strategies_strategy_id ON strategies(strategy_id, resolved_at DESC);

-- name: log_resolved
INSERT INTO strategies (strategy_id, config_json, resolved_at) VALUES (?, ?, ?)

-- name: read_bundled_strategy_asset_info
SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?

-- name: read_bundled_strategy_asset_info_2
SELECT value FROM bundle_meta WHERE key = 'alloc_pct'
