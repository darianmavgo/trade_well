package stage_orders

import (
	"database/sql"
	"time"
)

// InsertStagedOrder queues a bracket order for pkg/submit_orders.
func InsertStagedOrder(db *sql.DB, o *StagedOrder) (int64, error) {
	query := `
	INSERT INTO stage_order (
		signal_id, account_number, strategy_name, symbol, instruction,
		quantity, entry_price, take_profit_price, stop_loss_price,
		max_trading_days, status, staged_at, entry_duration, session_date, alloc_pct
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	res, err := db.Exec(query,
		o.SignalID, o.AccountNumber, o.StrategyName, o.Symbol, o.Instruction,
		o.Quantity, o.EntryPrice, o.TakeProfitPrice, o.StopLossPrice,
		o.MaxTradingDays, o.Status, o.StagedAt, o.EntryDuration, o.SessionDate, o.AllocPct,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanStagedOrders(rows *sql.Rows) ([]StagedOrder, error) {
	defer rows.Close()

	var out []StagedOrder
	for rows.Next() {
		var o StagedOrder
		var signalID sql.NullInt64
		var rejectReason, orderID, clientOrderID sql.NullString
		var submittedAt sql.NullTime
		if err := rows.Scan(
			&o.ID, &signalID, &o.AccountNumber, &o.StrategyName, &o.Symbol, &o.Instruction,
			&o.Quantity, &o.EntryPrice, &o.TakeProfitPrice, &o.StopLossPrice,
			&o.MaxTradingDays, &o.Status, &rejectReason, &orderID, &clientOrderID,
			&o.StagedAt, &submittedAt, &o.EntryDuration, &o.SessionDate, &o.AllocPct,
		); err != nil {
			return nil, err
		}
		if signalID.Valid {
			o.SignalID = signalID.Int64
		}
		if rejectReason.Valid {
			o.RejectReason = rejectReason.String
		}
		if orderID.Valid {
			o.OrderID = orderID.String
		}
		if clientOrderID.Valid {
			o.ClientOrderID = clientOrderID.String
		}
		if submittedAt.Valid {
			t := submittedAt.Time
			o.SubmittedAt = &t
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

const stagedOrderColumns = `id, signal_id, account_number, strategy_name, symbol, instruction,
	       quantity, entry_price, take_profit_price, stop_loss_price,
	       max_trading_days, status, reject_reason, order_id, client_order_id, staged_at, submitted_at,
	       entry_duration, session_date, alloc_pct`

// GetPendingStagedOrders returns PENDING stage_order rows for accountNumber
// ("" matches every account), oldest first, ready for pkg/submit_orders.
func GetPendingStagedOrders(db *sql.DB, accountNumber string) ([]StagedOrder, error) {
	rows, err := db.Query(`
	SELECT `+stagedOrderColumns+`
	FROM stage_order
	WHERE status = 'PENDING' AND (account_number = ? OR ? = '')
	ORDER BY staged_at ASC
	`, accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanStagedOrders(rows)
}

// GetStagedOrders returns stage_order rows for accountNumber ("" matches
// every account) regardless of status, most recently staged first.
func GetStagedOrders(db *sql.DB, accountNumber string) ([]StagedOrder, error) {
	rows, err := db.Query(`
	SELECT `+stagedOrderColumns+`
	FROM stage_order
	WHERE account_number = ? OR ? = ''
	ORDER BY staged_at DESC
	`, accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanStagedOrders(rows)
}

// GetOrdersAwaitingFillConfirmation returns stage_order rows that were
// accepted by the broker on a prior run but never confirmed filled (see
// StagedOrderPendingFill), oldest first -- ready for pkg/submit_orders to
// re-check their real status before this account's open positions are
// considered complete.
func GetOrdersAwaitingFillConfirmation(db *sql.DB, accountNumber string) ([]StagedOrder, error) {
	rows, err := db.Query(`
	SELECT `+stagedOrderColumns+`
	FROM stage_order
	WHERE status = 'SUBMITTED_PENDING_FILL' AND (account_number = ? OR ? = '')
	ORDER BY staged_at ASC
	`, accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanStagedOrders(rows)
}

// MarkStagedOrderSubmitted records a live order submission CONFIRMED filled
// -- the caller has already verified this with the broker, not merely that
// PlaceOrder returned without error.
func MarkStagedOrderSubmitted(db *sql.DB, id int64, orderID string, submittedAt time.Time) error {
	_, err := db.Exec(`UPDATE stage_order SET status = ?, order_id = ?, submitted_at = ? WHERE id = ?`,
		StagedOrderSubmitted, orderID, submittedAt, id)
	return err
}

// MarkStagedOrderPendingFill records that the broker accepted the order (an
// order ID exists) but its fill has not yet been confirmed. See
// StagedOrderPendingFill.
func MarkStagedOrderPendingFill(db *sql.DB, id int64, orderID string, submittedAt time.Time) error {
	_, err := db.Exec(`UPDATE stage_order SET status = ?, order_id = ?, submitted_at = ? WHERE id = ?`,
		StagedOrderPendingFill, orderID, submittedAt, id)
	return err
}

// MarkStagedOrderRejected records why a staged order was not submitted.
func MarkStagedOrderRejected(db *sql.DB, id int64, reason string) error {
	_, err := db.Exec(`UPDATE stage_order SET status = ?, reject_reason = ? WHERE id = ?`,
		StagedOrderRejected, reason, id)
	return err
}

// ClaimStagedOrderForSubmit atomically moves PENDING -> SUBMITTING and sets
// client_order_id. Only one winner succeeds (rowsAffected == 1).
func ClaimStagedOrderForSubmit(db *sql.DB, id int64, clientOrderID string) (bool, error) {
	res, err := db.Exec(`
UPDATE stage_order
SET status = ?, client_order_id = ?
WHERE id = ? AND status = ?`,
		StagedOrderSubmitting, clientOrderID, id, StagedOrderPending)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReleaseStagedOrderClaim moves a SUBMITTING row back to PENDING, for an order that is known not
// to have reached the broker (its credentials were refused before any order was sent). The
// client_order_id stays, so the retry is the same intent.
func ReleaseStagedOrderClaim(db *sql.DB, id int64) error {
	_, err := db.Exec(`UPDATE stage_order SET status = ? WHERE id = ? AND status = ?`,
		StagedOrderPending, id, StagedOrderSubmitting)
	return err
}

// GetSubmittingStagedOrders returns rows left in SUBMITTING after a crash /
// restart -- must be reconciled, never blind re-PlaceOrder.
func GetSubmittingStagedOrders(db *sql.DB, accountNumber string) ([]StagedOrder, error) {
	rows, err := db.Query(`
	SELECT `+stagedOrderColumns+`
	FROM stage_order
	WHERE status = ? AND (account_number = ? OR ? = '')
	ORDER BY staged_at ASC
	`, StagedOrderSubmitting, accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanStagedOrders(rows)
}

// GetWatchingStagedOrders returns the WATCHING rows (see StagedOrderWatching) for
// accountNumber ("" matches every account), oldest first, ordered by symbol within a
// staging run so a trigger pass works through them in a fixed order.
func GetWatchingStagedOrders(db *sql.DB, accountNumber string) ([]StagedOrder, error) {
	rows, err := db.Query(`
	SELECT `+stagedOrderColumns+`
	FROM stage_order
	WHERE status = ? AND (account_number = ? OR ? = '')
	ORDER BY staged_at ASC, symbol ASC
	`, StagedOrderWatching, accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanStagedOrders(rows)
}

// PromoteWatchingOrder turns a WATCHING row into a PENDING one at quantity qty, so
// pkg/submit_orders places it. It reports whether the row was still WATCHING.
func PromoteWatchingOrder(db *sql.DB, id int64, qty float64) (bool, error) {
	res, err := db.Exec(`UPDATE stage_order SET status = ?, quantity = ? WHERE id = ? AND status = ?`,
		StagedOrderPending, qty, id, StagedOrderWatching)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// CancelWatchingOrder closes a WATCHING row that will not be placed (its limit was
// never reached by the close, or there was no cash for it when it triggered).
func CancelWatchingOrder(db *sql.DB, id int64, reason string) error {
	_, err := db.Exec(`UPDATE stage_order SET status = ?, reject_reason = ? WHERE id = ? AND status = ?`,
		StagedOrderCancelled, reason, id, StagedOrderWatching)
	return err
}

// GetStagedOrder returns the stage_order row with this id, or nil when there is none.
func GetStagedOrder(db *sql.DB, id int64) (*StagedOrder, error) {
	rows, err := db.Query(`SELECT `+stagedOrderColumns+` FROM stage_order WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	got, err := scanStagedOrders(rows)
	if err != nil || len(got) == 0 {
		return nil, err
	}
	return &got[0], nil
}

// GetRestingDayEntries returns the day-limit entries the broker accepted for sessionDate
// that have not filled yet (SUBMITTED_PENDING_FILL), oldest first. These are the orders
// that hold buying power at the broker.
func GetRestingDayEntries(db *sql.DB, accountNumber, sessionDate string) ([]StagedOrder, error) {
	rows, err := db.Query(`
	SELECT `+stagedOrderColumns+`
	FROM stage_order
	WHERE status = ? AND entry_duration = ? AND session_date = ? AND (account_number = ? OR ? = '')
	ORDER BY staged_at ASC, symbol ASC
	`, StagedOrderPendingFill, EntryDurationDay, sessionDate, accountNumber, accountNumber)
	if err != nil {
		return nil, err
	}
	return scanStagedOrders(rows)
}

// ReopenRejectedWatch puts a row the broker refused for lack of buying power back to
// WATCHING, so a later pass can try it again when buying power frees up. It reports
// whether the row was still REJECTED.
func ReopenRejectedWatch(db *sql.DB, id int64) (bool, error) {
	res, err := db.Exec(`UPDATE stage_order SET status = ?, reject_reason = NULL, client_order_id = NULL
		WHERE id = ? AND status = ?`, StagedOrderWatching, id, StagedOrderRejected)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ReopenRejectedOrder puts a REJECTED row that never reached the broker (it has no broker order
// id) back to PENDING with a fresh client_order_id, for submit_orders to try again. It reports
// whether the row qualified: a row that is not REJECTED, or that has an order id, is left alone.
func ReopenRejectedOrder(db *sql.DB, id int64) (bool, error) {
	res, err := db.Exec(`UPDATE stage_order SET status = ?, reject_reason = NULL, client_order_id = NULL
		WHERE id = ? AND status = ? AND COALESCE(order_id, '') = ''`, StagedOrderPending, id, StagedOrderRejected)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// CancelRestingOrder closes a resting entry the caller has cancelled at the broker and
// confirmed CANCELED. It reports whether the row was still waiting on a fill.
func CancelRestingOrder(db *sql.DB, id int64, reason string) (bool, error) {
	res, err := db.Exec(`UPDATE stage_order SET status = ?, reject_reason = ? WHERE id = ? AND status = ?`,
		StagedOrderCancelled, reason, id, StagedOrderPendingFill)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
