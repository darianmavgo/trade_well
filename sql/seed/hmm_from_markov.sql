-- Migrates the markov family to the hmm family: one calm_filter row per distinct trade symbol,
-- read off the QQQ model (hmm_symbol QQQ). Exits are the hmm family's: hold 3 days, no take
-- profit, 8% stop (gridsearch tunes the stop afterwards). The allocation, cash yield and slippage
-- are carried over from the markov row. The markov rows are copied to markov_strategy_retired and
-- markov_strategy is left as it is.
-- Run: sqlite3 refdata/strategies.db < sql/seed/hmm_from_markov.sql
CREATE TABLE IF NOT EXISTS markov_strategy_retired AS SELECT * FROM markov_strategy WHERE 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_markov_strategy_retired_id ON markov_strategy_retired(id);

INSERT OR IGNORE INTO markov_strategy_retired SELECT * FROM markov_strategy;

CREATE TABLE IF NOT EXISTS hmm_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	hmm_symbol       TEXT NOT NULL DEFAULT 'QQQ',
	trade_symbol     TEXT NOT NULL DEFAULT 'QQQ',
	rule             TEXT NOT NULL,
	stress_prob      REAL NOT NULL,
	hold_days        INTEGER NOT NULL,
	take_profit_pct  REAL NOT NULL,
	stop_loss_pct    REAL NOT NULL,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	source_strategy  TEXT,
	source_label     TEXT,
	win_rate         REAL,
	total_trades     INTEGER
);

-- The first markov row (lowest id) of each trade symbol; a duplicate symbol is skipped.
INSERT OR IGNORE INTO hmm_strategy (id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, source_strategy, source_label)
SELECT
	'hmm-calm-' || lower(m.trade_symbol),
	m.trade_symbol || ' Long While QQQ Calm (HMM)',
	'QQQ',
	m.trade_symbol,
	'calm_filter',
	0.5,
	3,
	0.0,
	0.08,
	m.allocation_pct,
	m.cash_yield,
	m.slippage_pct,
	m.id,
	'markov migration'
FROM markov_strategy m
WHERE m.id = (SELECT min(id) FROM markov_strategy WHERE trade_symbol = m.trade_symbol);
