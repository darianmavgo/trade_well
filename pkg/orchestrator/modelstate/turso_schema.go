package modelstate

import (
	"database/sql"
	"fmt"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// The tables below are in Turso. Nothing in this file opens a SQLite file.

// ensureHMMSchema creates the two tables the predictions are stored in.
func ensureHMMSchema(db *sql.DB) error {
	for _, ddl := range []string{
		orchestratorsql.Q("modelstate/ensure_hmmschema"),
		orchestratorsql.Q("modelstate/ensure_hmmschema_2"),
	} {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("modelstate: create schema: %w", err)
		}
	}
	return nil
}

func ensureMarkovSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("modelstate/ensure_markov_schema"))
	if err != nil {
		return fmt.Errorf("modelstate: create markov_extension_live: %w", err)
	}
	return nil
}
