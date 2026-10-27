package scan_market

import (
	"database/sql"
	"strings"
)

// StagedDecision is the scan identity of one staged order: which buy signal,
// on which bar, for which strategy. submit_orders copies it onto manage_exit
// so a later reconciliation can join the live trade back to the signal.
type StagedDecision struct {
	BuySignalID int64
	StrategyID  string
	AsOfDate    string
	NextSession string
}

// DecisionForStagedOrder looks up the buy signal linked to a stage_order, then
// the older one-row-per-day scan_market_signal. A missing table or no row
// returns a zero decision and a nil error: recording the fill must not depend
// on the lookup.
func DecisionForStagedOrder(db *sql.DB, stagedOrderID, signalID int64) (StagedDecision, error) {
	if db == nil {
		return StagedDecision{}, nil
	}
	var d StagedDecision
	if stagedOrderID > 0 {
		err := db.QueryRow(`SELECT id, strategy_id, as_of_date, next_session
			FROM scan_market_buy_signal WHERE staged_order_id = ? ORDER BY id DESC LIMIT 1`, stagedOrderID).
			Scan(&d.BuySignalID, &d.StrategyID, &d.AsOfDate, &d.NextSession)
		if err == nil {
			return d, nil
		}
		if err != sql.ErrNoRows && !noSuchTable(err) {
			return StagedDecision{}, err
		}
	}
	if signalID > 0 {
		var strategy, day string
		err := db.QueryRow(`SELECT strategy_name, signal_date FROM scan_market_signal WHERE id = ?`, signalID).
			Scan(&strategy, &day)
		if err == nil {
			d.StrategyID = strategy
			d.AsOfDate = day
			return d, nil
		}
		if err != sql.ErrNoRows && !noSuchTable(err) {
			return StagedDecision{}, err
		}
	}
	return StagedDecision{}, nil
}

func noSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}
