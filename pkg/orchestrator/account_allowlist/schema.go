package account_allowlist

import (
	"database/sql"
	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

var schemaDDL = orchestratorsql.Q("account_allowlist/schema_ddl")

// EnsureSchema creates the account_allowlist audit table if it doesn't exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(schemaDDL)
	return err
}
