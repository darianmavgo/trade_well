CREATE TABLE IF NOT EXISTS scan_market_signal (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	symbol TEXT NOT NULL,
	strategy_name TEXT NOT NULL,
	signal_type TEXT NOT NULL,
	price REAL NOT NULL,
	indicator_data TEXT,
	generated_at DATETIME NOT NULL,
	status TEXT NOT NULL DEFAULT 'PENDING',
	signal_date TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_scan_market_signal_day ON scan_market_signal(strategy_name, signal_date);

CREATE TABLE IF NOT EXISTS scan_market_buy_signal (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	scan_run_id TEXT NOT NULL,
	stack_id TEXT NOT NULL DEFAULT '',
	strategy_id TEXT NOT NULL,
	strategy_name TEXT NOT NULL DEFAULT '',
	symbol TEXT NOT NULL,
	direction TEXT NOT NULL DEFAULT 'LONG',
	as_of_date TEXT NOT NULL,
	next_session TEXT NOT NULL DEFAULT '',
	price REAL NOT NULL DEFAULT 0,
	close REAL NOT NULL DEFAULT 0,
	buy_limit REAL NOT NULL DEFAULT 0,
	take_profit REAL NOT NULL DEFAULT 0,
	stop_loss REAL NOT NULL DEFAULT 0,
	allocation_pct REAL NOT NULL DEFAULT 0,
	hold_days INTEGER NOT NULL DEFAULT 0,
	stack_priority INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'NEW',
	staged_order_id INTEGER,
	raw_json TEXT NOT NULL DEFAULT '',
	notes TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scan_market_buy_signal_created ON scan_market_buy_signal(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_scan_market_buy_signal_status ON scan_market_buy_signal(status);
CREATE INDEX IF NOT EXISTS idx_scan_market_buy_signal_strategy_asof ON scan_market_buy_signal(strategy_id, as_of_date);

-- Why each strategy did or did not enter, on each of its latest five sessions, from the pipeline's
-- own signal_trace. is_asof marks the session the scan decided on (the nightly verdict).
CREATE TABLE IF NOT EXISTS scan_market_trace (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	scan_run_id TEXT NOT NULL,
	strategy_id TEXT NOT NULL,
	symbol TEXT NOT NULL DEFAULT '',
	session_date TEXT NOT NULL,
	is_asof INTEGER NOT NULL DEFAULT 0,
	entry INTEGER NOT NULL DEFAULT 0,
	reason TEXT NOT NULL DEFAULT '',
	facts_json TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scan_market_trace_strategy ON scan_market_trace(strategy_id, is_asof, id DESC);
CREATE INDEX IF NOT EXISTS idx_scan_market_trace_run ON scan_market_trace(scan_run_id);

-- Markov predictions scored after a model's training cutoff (with the counts frozen at it), kept
-- here because the model file on the instance is rebuilt from the bundle on every cold start.
CREATE TABLE IF NOT EXISTS markov_prediction_live (
	symbol TEXT NOT NULL,
	date TEXT NOT NULL,
	state INTEGER NOT NULL,
	prob_bull REAL NOT NULL,
	prob_bear REAL NOT NULL,
	signal REAL NOT NULL,
	trained_through TEXT NOT NULL DEFAULT '',
	scan_run_id TEXT NOT NULL DEFAULT '',
	saved_at DATETIME NOT NULL,
	PRIMARY KEY (symbol, date)
);
