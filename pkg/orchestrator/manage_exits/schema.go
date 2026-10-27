package manage_exits

import (
	"database/sql"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// EnsureSchema creates the manage_exit table if it does not already exist and
// adds the exit-date columns to a table created before they existed.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("manage_exits/ensure_schema"))
	if err != nil {
		return err
	}
	// A table created before these columns existed keeps its rows and gains them.
	for _, c := range []struct{ name, decl string }{
		{"exit_date", "TEXT NOT NULL DEFAULT ''"},
		{"exit_date_source", "TEXT NOT NULL DEFAULT ''"},
		{"entry_filled_at", "DATETIME"},
		{"strategy_id", "TEXT NOT NULL DEFAULT ''"},
		{"as_of_date", "TEXT NOT NULL DEFAULT ''"},
		{"next_session", "TEXT NOT NULL DEFAULT ''"},
		{"signal_id", "INTEGER NOT NULL DEFAULT 0"},
		{"buy_signal_id", "INTEGER NOT NULL DEFAULT 0"},
		{"stage_order_id", "INTEGER NOT NULL DEFAULT 0"},
		{"entry_fill_price", "REAL NOT NULL DEFAULT 0"},
		{"exit_fill_price", "REAL NOT NULL DEFAULT 0"},
		{"filled_qty", "REAL NOT NULL DEFAULT 0"},
		{"fees", "REAL NOT NULL DEFAULT 0"},
		{"exit_reason_code", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := dbutil.AddColumn(db, "manage_exit", c.name, c.decl); err != nil {
			return err
		}
	}
	return nil
}
