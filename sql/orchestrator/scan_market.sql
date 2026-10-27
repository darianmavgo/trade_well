-- Statements of pkg/orchestrator/scan_market, read with orchestratorsql.Q("scan_market/<name>").
-- Each starts with a "-- name:" line.

-- name: copy_markov_model_asset_predictions_to_turso
INSERT OR REPLACE INTO markov_prediction_live (symbol, date, state, prob_bull, prob_bear, signal, trained_through, scan_run_id, saved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)

-- name: decision_for_staged_order
SELECT id, strategy_id, as_of_date, next_session
			FROM scan_market_buy_signal WHERE staged_order_id = ? ORDER BY id DESC LIMIT 1

-- name: decision_for_staged_order_2
SELECT strategy_name, signal_date FROM scan_market_signal WHERE id = ?

-- name: ensure_bars_schema
CREATE TABLE IF NOT EXISTS market_bars (
	symbol TEXT NOT NULL,
	date TEXT NOT NULL,
	open REAL NOT NULL,
	high REAL NOT NULL,
	low REAL NOT NULL,
	close REAL NOT NULL,
	adj_close REAL NOT NULL,
	volume INTEGER NOT NULL,
	PRIMARY KEY (symbol, date)
) WITHOUT ROWID

-- name: ensure_schema
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

-- name: insert_buy_signal
INSERT INTO scan_market_buy_signal (
	scan_run_id, stack_id, strategy_id, strategy_name, symbol, direction,
	as_of_date, next_session, price, close, buy_limit, take_profit, stop_loss,
	allocation_pct, hold_days, stack_priority, status, staged_order_id,
	raw_json, notes, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)

-- name: pending_buy_signals
SELECT id, scan_run_id, stack_id, strategy_id, strategy_name, symbol, direction,
	as_of_date, next_session, price, close, buy_limit, take_profit, stop_loss,
	allocation_pct, hold_days, stack_priority, status, staged_order_id,
	raw_json, notes, created_at
FROM scan_market_buy_signal WHERE status = ? ORDER BY scan_run_id DESC, stack_priority ASC, id ASC

-- name: recent_for
SELECT created_at, as_of_date, next_session, stack_id, strategy_id, symbol, close, buy_limit,
       take_profit, stop_loss, allocation_pct, hold_days, status, notes
FROM scan_market_buy_signal WHERE as_of_date >= ? ORDER BY as_of_date DESC, id DESC

-- name: recent_for_2
SELECT COALESCE(signal_date, ''), strategy_name, COALESCE(indicator_data, '')
FROM scan_market_signal WHERE signal_type = 'NO_BUY' AND signal_date >= ?
ORDER BY signal_date DESC, strategy_name

-- name: save_market_working_copy_to_turso
SELECT substr(CAST(Date AS TEXT), 1, 10) AS d, open, high, low, close, COALESCE("Adj Close", close) AS adj, COALESCE(volume, 0) AS volume
			 FROM backtest_start
			 WHERE symbol = ? AND timeframe = '1d' AND length(CAST(Date AS TEXT)) = 10 AND CAST(Date AS TEXT) >= ?
			 ORDER BY Date

-- name: save_trace
INSERT INTO scan_market_trace (scan_run_id, strategy_id, symbol, session_date, is_asof, entry, reason, facts_json, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)

-- name: set_buy_signal_status
UPDATE scan_market_buy_signal
SET status = ?, staged_order_id = COALESCE(?, staged_order_id),
	notes = CASE WHEN ? = '' THEN notes WHEN notes = '' THEN ? ELSE notes || '; ' || ? END
WHERE id = ?

-- name: update_signal_status
UPDATE scan_market_signal SET status = ? WHERE id = ?

-- name: upsert_signal_day
INSERT INTO scan_market_signal (symbol, strategy_name, signal_type, price, indicator_data, generated_at, status, signal_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(strategy_name, signal_date) DO UPDATE SET
			symbol=excluded.symbol,
			signal_type=excluded.signal_type,
			price=excluded.price,
			indicator_data=excluded.indicator_data,
			generated_at=excluded.generated_at,
			status=excluded.status

-- name: upsert_signal_day_2
SELECT id FROM scan_market_signal WHERE strategy_name = ? AND signal_date = ?
