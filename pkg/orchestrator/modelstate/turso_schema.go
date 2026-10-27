package modelstate

import (
	"database/sql"
	"fmt"
)

// The tables below are in Turso. Nothing in this file opens a SQLite file.

// ensureHMMSchema creates the two tables the predictions are stored in.
func ensureHMMSchema(db *sql.DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS hmm_prediction_live (
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
) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS hmm_state_prob_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	state INTEGER NOT NULL,
	prob REAL NOT NULL,
	next_prob REAL NOT NULL,
	PRIMARY KEY (symbol, trained_at, date, state)
) WITHOUT ROWID`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("modelstate: create schema: %w", err)
		}
	}
	return nil
}

func ensureMarkovSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS markov_extension_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	state INTEGER NOT NULL,
	prob_bull REAL NOT NULL,
	prob_bear REAL NOT NULL,
	signal REAL NOT NULL,
	PRIMARY KEY (symbol, trained_at, date)
) WITHOUT ROWID`)
	if err != nil {
		return fmt.Errorf("modelstate: create markov_extension_live: %w", err)
	}
	return nil
}
