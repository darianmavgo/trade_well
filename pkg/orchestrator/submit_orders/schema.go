package submit_orders

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// EnsureSchema creates the submit_order audit table if it does not already
// exist. This is a new, purely additive log -- one row per submission
// attempt (successful or not) -- with no prior equivalent; it doesn't
// replace any existing stage_order/manage_exit write.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("submit_orders/ensure_schema"))
	return err
}
