package scan_market

import (
	"database/sql"
	"strings"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
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
		err := db.QueryRow(orchestratorsql.Q("scan_market/decision_for_staged_order"), stagedOrderID).
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
		err := db.QueryRow(orchestratorsql.Q("scan_market/decision_for_staged_order_2"), signalID).
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
