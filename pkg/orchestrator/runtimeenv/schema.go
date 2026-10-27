package runtimeenv

import (
	"database/sql"
	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

var schemaDDL = orchestratorsql.Q("runtimeenv/schema_ddl")

// EnsureSchema creates the runtimeenv audit table if it doesn't exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(schemaDDL)
	return err
}
