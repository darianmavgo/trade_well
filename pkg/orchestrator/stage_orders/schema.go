package stage_orders

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// EnsureSchema creates the stage_order table if it does not already exist.
// The partial unique index on client_order_id is the actual idempotency
// enforcement behind ClaimStagedOrderForSubmit: it is what makes a second
// concurrent claim attempt fail outright instead of racing a duplicate live
// order submission for the same staged order.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("stage_orders/ensure_schema"))
	if err != nil {
		return err
	}
	// A table made before these columns existed keeps its rows and gains them.
	for _, c := range []struct{ name, decl string }{
		{"entry_duration", "TEXT NOT NULL DEFAULT ''"},
		{"session_date", "TEXT NOT NULL DEFAULT ''"},
		{"alloc_pct", "REAL NOT NULL DEFAULT 0"},
	} {
		if err := dbutil.AddColumn(db, "stage_order", c.name, c.decl); err != nil {
			return err
		}
	}
	return nil
}
