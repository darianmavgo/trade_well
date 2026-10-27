package auth

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// EnsureSchema creates the auth log table if it does not already exist. No
// raw token values are ever stored here — only event types, detail text, and
// timestamps (per the user's "prints tokens, refresh timestamps" — meaning
// the fact and timing of an issue/refresh, not the secret itself).
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("auth/ensure_schema"))
	return err
}
