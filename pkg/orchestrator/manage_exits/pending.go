package manage_exits

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// resolvePending settles entries recorded PENDING_FILL (accepted by the broker, fill not
// yet confirmed) from the broker's own order list: a FILLED entry becomes OPEN with its
// exit date corrected from the real fill; a CANCELED/REJECTED/EXPIRED one becomes
// CANCELLED (no position was ever opened); anything else is reported and left. It does
// nothing without the broker's orders. dryRun reports without writing.
func resolvePending(db *sql.DB, acc *trader.ResolvedAccount, orders []schwab.Order, now time.Time, dryRun bool, res *Result) {
	pending, err := GetPendingFillTrades(db, acc.AccountNumber)
	if err != nil {
		res.Details = append(res.Details, fmt.Sprintf("failed to load pending entries: %v", err))
		return
	}
	for _, p := range pending {
		row := Row{Account: acc.AccountNumber, Strategy: p.StrategyName, Symbol: p.Symbol, Signal: "ENTRY"}
		status, filledAt := EntryFill(orders, p.EntryOrderID)
		switch status {
		case "FILLED":
			haveFill := !filledAt.IsZero()
			if !haveFill {
				filledAt = now // filled, but the broker gave no time: an estimate, not the fill date
			}
			exit := ExitDateFor(filledAt, p.MaxTradingDays)
			row.Status = "ENTRY_FILLED"
			row.Detail = fmt.Sprintf("entry order %s filled %s; exit date %s (was %s, provisional)", p.EntryOrderID, filledAt.UTC().Format(time.RFC3339), exit, dashIfEmpty(p.ExitDate))
			if !dryRun {
				if haveFill {
					err = SetFill(db, p.ID, filledAt)
				} else {
					err = promoteWithoutFill(db, p.ID, now)
				}
				if err != nil {
					row.Status, row.Detail = "ERROR", fmt.Sprintf("could not record the fill of order %s: %v", p.EntryOrderID, err)
				} else if px := EntryFillPrice(orders, p.EntryOrderID); px > 0 {
					_ = SetEntryFillPrice(db, p.ID, px)
					row.Detail += fmt.Sprintf("; filled at $%.2f (limit $%.2f)", px, p.EntryPrice)
				}
			}
		case "CANCELED", "REJECTED", "EXPIRED":
			row.Status = "ENTRY_CANCELED"
			row.Detail = fmt.Sprintf("entry order %s is %s at the broker: it never filled, no position was opened", p.EntryOrderID, status)
			if !dryRun {
				_ = CancelPending(db, p.ID, "entry "+status)
			}
		default:
			shown := status
			if shown == "" {
				shown = "not found in the last 30 days of orders"
			}
			row.Status = "PENDING_FILL"
			row.Detail = fmt.Sprintf("entry order %s not filled yet (%s); provisional exit date %s", p.EntryOrderID, shown, dashIfEmpty(p.ExitDate))
		}
		res.Details = append(res.Details, fmt.Sprintf("[%s] %s", p.Symbol, row.Detail))
		res.Rows = append(res.Rows, row)
	}
}
