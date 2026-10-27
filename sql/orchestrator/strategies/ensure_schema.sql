CREATE TABLE IF NOT EXISTS strategies (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	strategy_id TEXT NOT NULL,
	config_json TEXT NOT NULL,
	resolved_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_strategies_strategy_id ON strategies(strategy_id, resolved_at DESC);
