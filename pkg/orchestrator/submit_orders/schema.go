package submit_orders

import "database/sql"

// EnsureSchema creates the submit_order audit table if it does not already
// exist. This is a new, purely additive log -- one row per submission
// attempt (successful or not) -- with no prior equivalent; it doesn't
// replace any existing stage_order/manage_exit write.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS submit_order (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	staged_order_id INTEGER NOT NULL,
	broker_order_id TEXT,
	symbol TEXT NOT NULL,
	quantity REAL NOT NULL,
	status TEXT NOT NULL,
	detail TEXT,
	submitted_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_submit_order_staged ON submit_order(staged_order_id);
`)
	return err
}
