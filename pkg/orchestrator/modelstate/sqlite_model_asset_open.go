package modelstate

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// openModelAsset opens a copy of a backtestgosqlite model asset (hmm_models.db or
// markov_models.db). It is the only place this package opens a model file.
func openModelAsset(path string) (*sql.DB, error) {
	local, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, fmt.Errorf("modelstate: open model database %s: %w", path, err)
	}
	local.SetMaxOpenConns(1)
	return local, nil
}
