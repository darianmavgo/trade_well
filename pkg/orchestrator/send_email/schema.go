package send_email

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// EnsureSchema creates the send_email table if it does not already exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("send_email/ensure_schema"))
	return err
}
