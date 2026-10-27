package stage_orders

import (
	"database/sql"

	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// EnsureSchema creates the stage_order table if it does not already exist.
// The partial unique index on client_order_id is the actual idempotency
// enforcement behind ClaimStagedOrderForSubmit: it is what makes a second
// concurrent claim attempt fail outright instead of racing a duplicate live
// order submission for the same staged order.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS stage_order (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	signal_id INTEGER,
	account_number TEXT NOT NULL,
	strategy_name TEXT NOT NULL,
	symbol TEXT NOT NULL,
	instruction TEXT NOT NULL DEFAULT 'BUY',
	quantity REAL NOT NULL,
	entry_price REAL NOT NULL,
	take_profit_price REAL NOT NULL,
	stop_loss_price REAL NOT NULL,
	max_trading_days INTEGER NOT NULL DEFAULT 8,
	status TEXT NOT NULL DEFAULT 'PENDING',
	reject_reason TEXT,
	order_id TEXT,
	client_order_id TEXT,
	staged_at DATETIME NOT NULL,
	submitted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_stage_order_client_order_id ON stage_order(client_order_id) WHERE client_order_id IS NOT NULL AND client_order_id != '';
`)
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
