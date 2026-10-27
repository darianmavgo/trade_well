package submit_orders

import (
	"database/sql"
	"time"
)

// logAttempt records one submission attempt (successful or not) to the
// submit_order audit table. Errors writing the log are swallowed -- this is
// an audit trail, not the source of truth for order/trade state.
func logAttempt(db *sql.DB, stagedOrderID int64, brokerOrderID, symbol string, quantity float64, status, detail string, at time.Time) {
	if db == nil {
		return
	}
	_, _ = db.Exec(`
	INSERT INTO submit_order (staged_order_id, broker_order_id, symbol, quantity, status, detail, submitted_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	`, stagedOrderID, brokerOrderID, symbol, quantity, status, detail, at)
}
