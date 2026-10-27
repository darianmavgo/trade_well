package manage_exits

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// StatusSettledLate is the report status of a row closed from a sell that happened on an
// earlier day. The database row still gets the real closing status; the report must not
// look like a fresh profitable exit, so it gets its own label.
const StatusSettledLate = "SETTLED_LATE"

// settlingSell finds the broker's record that a position already left the account: the
// earliest FILLED sell of trade's symbol that closed at or after the entry fill and that
// no other row in this run has claimed. nil means the order list holds no such sale, and
// the caller must leave the row alone: with no evidence, "no position at the broker" may
// just be a stale account snapshot, and closing the row would orphan a real holding.
func settlingSell(orders []schwab.Order, trade Trade, claimed map[int64]bool) *schwab.Order {
	lower := trade.EntryTime
	if trade.EntryFilledAt != nil {
		lower = *trade.EntryFilledAt
	}
	sym := strings.ToUpper(trade.Symbol)
	var best *schwab.Order
	for i := range orders {
		o := &orders[i]
		if strings.ToUpper(o.Status) != "FILLED" || o.OrderID == nil || claimed[*o.OrderID] || o.CloseTime == nil {
			continue
		}
		if !trader.OrderIsSell(o) || trader.OrderSymbol(o) != sym || o.CloseTime.Time.Before(lower) {
			continue
		}
		if best == nil || o.CloseTime.Time.Before(best.CloseTime.Time) {
			best = o
		}
	}
	return best
}

// settleOrphan closes an OPEN manage_exit row whose position the broker no longer shows,
// using the filled sell found by settlingSell. The take-profit limit, the stop and a market
// sell map to CLOSED_PROFIT, CLOSED_STOP_LOSS and CLOSED_MANUAL. The row records what the
// order really executed at, read from its execution legs (exit_fill_price). Only when the list
// shows none does it fall back to the order's own limit or stop price, or the entry price for a
// market sell (pnl 0), and says so. It reports whether the row was settled. dryRun reports
// without writing.
func settleOrphan(db *sql.DB, trade Trade, sell *schwab.Order, dryRun bool, res *Result, row *Row) bool {
	status, reason, price := TradeStatusClosedManual, "BROKER_SELL_FILLED", trade.EntryPrice
	switch strings.ToUpper(sell.OrderType) {
	case "LIMIT":
		if sell.Price != nil && *sell.Price > 0 {
			status, reason, price = TradeStatusClosedProfit, "PROFIT_TAKER_FILLED", *sell.Price
		}
	case "STOP", "STOP_LIMIT":
		if sell.StopPrice != nil && *sell.StopPrice > 0 {
			status, reason, price = TradeStatusClosedStopLoss, "STOP_LOSS_FILLED", *sell.StopPrice
		}
	}
	exitTime := sell.CloseTime.Time
	// The fill is what the broker executed; the order's own limit or stop price is only where it
	// triggered. A stop that fires on a gap fills below its trigger.
	fill, _, filled := sell.FillPrice("")
	if filled {
		price = fill
	} else {
		fill = 0
	}
	priceNote := "the order's limit/stop price: the order list showed no execution, so this is not the fill"
	switch {
	case filled:
		priceNote = "the broker's fill price"
	case strings.ToUpper(sell.OrderType) == "MARKET":
		priceNote = "the entry price as a placeholder (pnl unknown)"
	}
	detail := fmt.Sprintf("LATE BOOKKEEPING, NOT A NEW TRADE: the broker sold this position on %s (%s sell order %d); the database only caught up now and closed the row as %s. Recorded price $%.2f is %s",
		exitTime.In(job_runs.EasternTime()).Format("Mon 2006-01-02 15:04 MST"), strings.ToUpper(sell.OrderType), *sell.OrderID, status, price, priceNote)
	row.Signal, row.Detail = "SETTLE_BROKER_SELL", detail
	if dryRun {
		row.Status = "SIMULATED"
		row.Detail += " -- would close"
		return true
	}
	if err := CloseActiveTradeFilled(db, trade.ID, status, reason, price, fill, exitTime); err != nil {
		row.Status, row.Detail = "ERROR", fmt.Sprintf("could not close the row for sell order %d: %v", *sell.OrderID, err)
		return false
	}
	_ = SetExitOrderID(db, trade.ID, fmt.Sprintf("%d", *sell.OrderID))
	row.Status = StatusSettledLate
	res.SettledNoPosition++
	return true
}
