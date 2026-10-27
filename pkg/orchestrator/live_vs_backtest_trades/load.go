package live_vs_backtest_trades

import (
	"database/sql"
	"fmt"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

func loadSignals(db *sql.DB) ([]buySig, error) {
	rows, err := db.Query(orchestratorsql.Q("live_vs_backtest_trades/load_signals"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []buySig
	for rows.Next() {
		var s buySig
		if err := rows.Scan(&s.id, &s.strategyID, &s.symbol, &s.asOf, &s.next,
			&s.close, &s.buyLimit, &s.takeProfit, &s.stopLoss, &s.holdDays, &s.status,
			&s.notes, &s.stagedID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadStages(db *sql.DB) ([]stageRow, error) {
	rows, err := db.Query(orchestratorsql.Q("live_vs_backtest_trades/load_stages"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stageRow
	for rows.Next() {
		var s stageRow
		var staged flexTime
		if err := rows.Scan(&s.id, &s.signalID, &s.account, &s.strategy, &s.symbol,
			&s.qty, &s.entry, &s.tp, &s.sl, &s.hold, &s.status,
			&s.reason, &s.orderID, &staged); err != nil {
			return nil, err
		}
		s.stagedAt = staged.s
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadExits(db *sql.DB) ([]exitRow, error) {
	rows, err := db.Query(orchestratorsql.Q("live_vs_backtest_trades/load_exits"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exitRow
	for rows.Next() {
		var e exitRow
		var entry, filled flexTime
		if err := rows.Scan(&e.id, &e.account, &e.symbol, &e.strategy,
			&e.strategyID, &e.asOf, &e.next, &e.signalID, &e.buySignalID, &e.stageID, &e.orderID,
			&e.entryLimit, &e.entryFill, &e.exitFill, &e.exitPrice, &e.filledQty, &e.fees, &e.qty,
			&e.status, &e.exitReason, &e.reasonCode, &e.exitDate, &e.pnlPct,
			&entry, &filled, &e.hold); err != nil {
			return nil, err
		}
		e.entryTime = entry.s
		e.filledAt = filled.s
		out = append(out, e)
	}
	return out, rows.Err()
}

// flexTime accepts the string or time.Time values SQLite hands back for a DATETIME column.
type flexTime struct{ s string }

func (f *flexTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		f.s = ""
	case string:
		f.s = v
	case []byte:
		f.s = string(v)
	case time.Time:
		f.s = v.UTC().Format(time.RFC3339)
	default:
		return fmt.Errorf("live_vs_backtest_trades: cannot scan %T as a time", src)
	}
	return nil
}
