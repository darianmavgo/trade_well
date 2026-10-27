package submit_orders

import (
	"database/sql"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// logAttempt records one submission attempt (successful or not) to the
// submit_order audit table. Errors writing the log are swallowed -- this is
// an audit trail, not the source of truth for order/trade state.
func logAttempt(db *sql.DB, stagedOrderID int64, brokerOrderID, symbol string, quantity float64, status, detail string, at time.Time) {
	if db == nil {
		return
	}
	_, _ = db.Exec(orchestratorsql.Q("submit_orders/log_attempt"), stagedOrderID, brokerOrderID, symbol, quantity, status, detail, at)
}
