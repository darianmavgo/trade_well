package scan_market

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// EnsureSchema creates scan_market's tables if they do not already exist.
func EnsureSchema(db *sql.DB) error {
	if err := EnsureBarsSchema(db); err != nil {
		return err
	}
	_, err := db.Exec(orchestratorsql.Q("scan_market/ensure_schema"))
	return err
}
