-- Statements of pkg/orchestrator/modelstate, read with orchestratorsql.Q("modelstate/<name>").
-- Each starts with a "-- name:" line.

-- name: build_daily_bars_working_copy_from_turso
SELECT date, close, adj_close, volume FROM daily_bars_live WHERE symbol = ? ORDER BY date

-- name: ensure_bars
SELECT MAX(date) FROM daily_bars_live WHERE symbol = ?

-- name: ensure_bars_schema
CREATE TABLE IF NOT EXISTS daily_bars_live (
	symbol TEXT NOT NULL,
	date TEXT NOT NULL,
	close REAL NOT NULL,
	adj_close REAL NOT NULL,
	volume INTEGER NOT NULL,
	source TEXT NOT NULL,
	PRIMARY KEY (symbol, date)
) WITHOUT ROWID

-- name: ensure_hmmschema
CREATE TABLE IF NOT EXISTS hmm_prediction_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	ret REAL NOT NULL,
	vol REAL NOT NULL,
	regime INTEGER NOT NULL,
	regime_prob REAL NOT NULL,
	next_regime INTEGER NOT NULL,
	next_regime_prob REAL NOT NULL,
	loglik_bar REAL NOT NULL,
	in_sample INTEGER NOT NULL,
	PRIMARY KEY (symbol, trained_at, date)
) WITHOUT ROWID

-- name: ensure_hmmschema_2
CREATE TABLE IF NOT EXISTS hmm_state_prob_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	state INTEGER NOT NULL,
	prob REAL NOT NULL,
	next_prob REAL NOT NULL,
	PRIMARY KEY (symbol, trained_at, date, state)
) WITHOUT ROWID

-- name: ensure_markov_schema
CREATE TABLE IF NOT EXISTS markov_extension_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	state INTEGER NOT NULL,
	prob_bull REAL NOT NULL,
	prob_bear REAL NOT NULL,
	signal REAL NOT NULL,
	PRIMARY KEY (symbol, trained_at, date)
) WITHOUT ROWID

-- name: held_tail
SELECT date, adj_close FROM daily_bars_live WHERE symbol = ? AND date >= ? ORDER BY date

-- name: hydrate_hmmmodel_asset_from_turso_one
SELECT date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample
		FROM hmm_prediction_live WHERE symbol = ? AND trained_at = ? AND date > ? ORDER BY date

-- name: hydrate_hmmmodel_asset_from_turso_one_2
INSERT OR REPLACE INTO hmm_prediction
			(symbol, date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample)
			VALUES (?,?,?,?,?,?,?,?,?,?)

-- name: hydrate_hmmmodel_asset_from_turso_one_3
SELECT date, state, prob, next_prob FROM hmm_state_prob_live
		WHERE symbol = ? AND trained_at = ? AND date > ? ORDER BY date, state

-- name: hydrate_hmmmodel_asset_from_turso_one_4
INSERT OR REPLACE INTO hmm_state_prob (symbol, date, state, prob, next_prob) VALUES (?,?,?,?,?)

-- name: hydrate_hmmmodel_asset_from_turso_one_5
UPDATE hmm_model_meta SET last_date = ? WHERE symbol = ?

-- name: hydrate_markov_model_asset_from_turso_one
SELECT date, state, prob_bull, prob_bear, signal FROM markov_extension_live
		WHERE symbol = ? AND trained_at = ? AND date > ? ORDER BY date

-- name: hydrate_markov_model_asset_from_turso_one_2
INSERT OR REPLACE INTO markov_prediction (symbol, date, state, prob_bull, prob_bear, signal) VALUES (?,?,?,?,?,?)

-- name: hydrate_markov_model_asset_from_turso_one_3
UPDATE markov_model_meta SET trained_through = COALESCE(trained_through, last_date), bars = bars + ?, last_date = ? WHERE symbol = ?

-- name: latest_hmmprediction_in_turso
SELECT date, regime, regime_prob, next_regime, next_regime_prob
		FROM hmm_prediction_live WHERE symbol = ? AND trained_at = ? ORDER BY date DESC LIMIT 1

-- name: persist_hmmmodel_asset_to_turso_one
SELECT MAX(date) FROM hmm_prediction_live WHERE symbol = ? AND trained_at = ?

-- name: persist_hmmmodel_asset_to_turso_one_2
SELECT date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample
		FROM hmm_prediction WHERE symbol = ? AND date > ? ORDER BY date

-- name: persist_hmmmodel_asset_to_turso_one_3
INSERT OR REPLACE INTO hmm_prediction_live
			(symbol, trained_at, date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)

-- name: persist_hmmmodel_asset_to_turso_one_4
SELECT date, state, prob, next_prob FROM hmm_state_prob
		WHERE symbol = ? AND date > ? ORDER BY date, state

-- name: persist_hmmmodel_asset_to_turso_one_5
INSERT OR REPLACE INTO hmm_state_prob_live (symbol, trained_at, date, state, prob, next_prob)
			VALUES (?,?,?,?,?,?)

-- name: persist_markov_model_asset_to_turso_one
SELECT MAX(date) FROM markov_extension_live WHERE symbol = ? AND trained_at = ?

-- name: persist_markov_model_asset_to_turso_one_2
SELECT date, state, prob_bull, prob_bear, signal FROM markov_prediction
		WHERE symbol = ? AND date > ? ORDER BY date

-- name: persist_markov_model_asset_to_turso_one_3
INSERT OR REPLACE INTO markov_extension_live (symbol, trained_at, date, state, prob_bull, prob_bear, signal) VALUES (?,?,?,?,?,?,?)

-- name: read_hmmmodel_asset_meta
SELECT symbol, trained_at, trained_through, last_date FROM hmm_model_meta ORDER BY symbol

-- name: read_markov_model_asset_meta
SELECT symbol, trained_at, last_date, trained_through FROM markov_model_meta ORDER BY symbol

-- name: store_bars
DELETE FROM daily_bars_live WHERE symbol = ?

-- name: store_bars_2
INSERT OR REPLACE INTO daily_bars_live (symbol, date, close, adj_close, volume, source) VALUES
