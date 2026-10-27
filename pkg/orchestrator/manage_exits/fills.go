package manage_exits

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// What the broker really executed is read from each order's execution legs (schwab.Order.FillPrice).
// An order's Price and StopPrice are the limit and the trigger: a limit buy at 271.76 can fill at
// 256.69 and a stop at 217.41 can fill lower. P&L is measured from the fill when it is known and
// from the limit/trigger only when it is not.

// EntryFillPrice is the average price the entry order entryOrderID executed at, 0 when the order
// is not in orders or has not executed. Children are searched, as in EntryFill.
func EntryFillPrice(orders []schwab.Order, entryOrderID string) float64 {
	if strings.TrimSpace(entryOrderID) == "" {
		return 0
	}
	for _, o := range schwab.FlattenOrders(orders) {
		if o.OrderID == nil || fmt.Sprintf("%d", *o.OrderID) != entryOrderID {
			continue
		}
		if px, _, ok := o.FillPrice(""); ok {
			return px
		}
		return 0
	}
	return 0
}

// SetEntryFillPrice records what the entry really cost on trade id and, when the trade has
// already closed, re-measures its P&L from that price. A price of 0 changes nothing.
func SetEntryFillPrice(db *sql.DB, id int64, price float64) error {
	if price <= 0 {
		return nil
	}
	if _, err := db.Exec(orchestratorsql.Q("manage_exits/set_entry_fill_price"), price, id); err != nil {
		return err
	}
	return repriceClosed(db, id)
}

// CloseActiveTradeFilled closes trade id. orderPrice is the exit order's own limit or trigger,
// fillPrice what it really executed at (0 when unknown). The row's exit_price and P&L use the
// fill when there is one; exit_fill_price records it. Entry cost is the entry fill when known.
func CloseActiveTradeFilled(db *sql.DB, id int64, status, reason string, orderPrice, fillPrice float64, exitTime time.Time) error {
	var entryPrice, entryFill, qty float64
	if err := db.QueryRow(orchestratorsql.Q("manage_exits/close_active_trade_filled"), id).Scan(&entryPrice, &entryFill, &qty); err != nil {
		return err
	}
	exitPrice := orderPrice
	if fillPrice > 0 {
		exitPrice = fillPrice
	}
	pnl, pnlPct := measure(entryPrice, entryFill, exitPrice, qty)
	_, err := db.Exec(orchestratorsql.Q("manage_exits/close_active_trade_filled_2"), status, reason, ExitReasonCode(status, reason), exitPrice, fillPrice, exitTime, pnl, pnlPct, id)
	return err
}

// measure is the P&L in dollars and percent of a position: from the entry fill when known,
// otherwise from the entry limit.
func measure(entryLimit, entryFill, exitPrice, qty float64) (pnl, pct float64) {
	basis := entryLimit
	if entryFill > 0 {
		basis = entryFill
	}
	pnl = (exitPrice - basis) * qty
	if basis > 0 {
		pct = (exitPrice - basis) / basis * 100
	}
	return pnl, pct
}

// repriceClosed re-measures a closed row from its recorded fills. A row that is not closed, or
// has neither fill, is left alone.
func repriceClosed(db *sql.DB, id int64) error {
	var status string
	var entryPrice, entryFill, exitPrice, exitFill, qty float64
	if err := db.QueryRow(orchestratorsql.Q("manage_exits/reprice_closed"), id).
		Scan(&status, &entryPrice, &entryFill, &exitPrice, &exitFill, &qty); err != nil {
		return err
	}
	if !strings.HasPrefix(status, "CLOSED") || (entryFill <= 0 && exitFill <= 0) {
		return nil
	}
	if exitFill > 0 {
		exitPrice = exitFill
	}
	pnl, pct := measure(entryPrice, entryFill, exitPrice, qty)
	_, err := db.Exec(orchestratorsql.Q("manage_exits/reprice_closed_2"), exitPrice, pnl, pct, id)
	return err
}

// recordEntryFill gives an OPEN trade its entry fill price from the broker's order list, in the
// database and on trade itself, so a later close is measured from it. Nothing is written in
// dryRun. A trade that already has one, or whose order shows no execution, is left alone.
func recordEntryFill(db *sql.DB, orders []schwab.Order, trade *Trade, dryRun bool) {
	if trade.EntryFillPrice > 0 {
		return
	}
	px := EntryFillPrice(orders, trade.EntryOrderID)
	if px <= 0 {
		return
	}
	if !dryRun {
		if err := SetEntryFillPrice(db, trade.ID, px); err != nil {
			return
		}
	}
	trade.EntryFillPrice = px
}

// backfillWindow is how far from a row's exit_time a sell may have closed to be taken as its exit:
// a stop settled from the broker's record matches to the second, a market exit we submitted
// fills some minutes after the row was written.
const (
	backfillBefore = 2 * time.Minute
	backfillAfter  = 30 * time.Minute
)

// BackfillFills gives closed rows of accountNumber the fills they were recorded without: the
// entry fill from the entry order, and the exit fill from the exit order or, with none recorded,
// from the earliest FILLED sell of the symbol that closed within a few minutes of the row's exit
// time (each sell is used once). Rows are re-measured from the fills. It returns one line per row
// it changed; with dryRun it reports without writing. Rows whose orders are older than the
// broker's order list are left as they are.
func BackfillFills(db *sql.DB, accountNumber string, orders []schwab.Order, dryRun bool) []string {
	if len(orders) == 0 {
		return nil
	}
	rows, err := db.Query(orchestratorsql.Q("manage_exits/backfill_fills"), accountNumber)
	if err != nil {
		return []string{fmt.Sprintf("could not read closed rows to backfill fills: %v", err)}
	}
	type gap struct {
		id                  int64
		symbol, entry, exit string
		exitTime            string
		entryFill, exitFill float64
	}
	var gaps []gap
	for rows.Next() {
		var g gap
		if err := rows.Scan(&g.id, &g.symbol, &g.entry, &g.exit, &g.exitTime, &g.entryFill, &g.exitFill); err != nil {
			rows.Close()
			return []string{fmt.Sprintf("could not read closed rows to backfill fills: %v", err)}
		}
		gaps = append(gaps, g)
	}
	rows.Close()

	flat := schwab.FlattenOrders(orders)
	claimed := map[int64]bool{}
	var lines []string
	for _, g := range gaps {
		var entryPx, exitPx float64
		if g.entryFill <= 0 {
			entryPx = EntryFillPrice(flat, g.entry)
		}
		if g.exitFill <= 0 {
			exitPx = exitFillFor(flat, g.symbol, g.exit, g.exitTime, claimed)
		}
		if entryPx <= 0 && exitPx <= 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] row %d: broker fills recorded (entry %s, exit %s); P&L re-measured from them",
			g.symbol, g.id, priceOrDash(entryPx), priceOrDash(exitPx)))
		if dryRun {
			continue
		}
		if entryPx > 0 {
			if _, err := db.Exec(orchestratorsql.Q("manage_exits/set_entry_fill_price"), entryPx, g.id); err != nil {
				lines[len(lines)-1] += fmt.Sprintf(" -- failed: %v", err)
				continue
			}
		}
		if exitPx > 0 {
			if _, err := db.Exec(orchestratorsql.Q("manage_exits/backfill_fills_2"), exitPx, g.id); err != nil {
				lines[len(lines)-1] += fmt.Sprintf(" -- failed: %v", err)
				continue
			}
		}
		if err := repriceClosed(db, g.id); err != nil {
			lines[len(lines)-1] += fmt.Sprintf(" -- failed to re-measure: %v", err)
		}
	}
	return lines
}

func priceOrDash(p float64) string {
	if p <= 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f", p)
}

// exitFillFor is the fill price of the sell that closed a row: its own exit order when one is
// recorded, otherwise the earliest unclaimed FILLED sell of symbol that closed within
// backfillBefore/backfillAfter of exitTime.
func exitFillFor(orders []schwab.Order, symbol, exitOrderID, exitTime string, claimed map[int64]bool) float64 {
	sym := strings.ToUpper(symbol)
	if exitOrderID != "" {
		for _, o := range orders {
			if o.OrderID != nil && fmt.Sprintf("%d", *o.OrderID) == exitOrderID && strings.ToUpper(o.Status) == "FILLED" {
				if px, _, ok := o.FillPrice(""); ok {
					claimed[*o.OrderID] = true
					return px
				}
			}
		}
	}
	at, ok := parseStoredTime(exitTime)
	if !ok {
		return 0
	}
	var best *schwab.Order
	for i := range orders {
		o := &orders[i]
		if strings.ToUpper(o.Status) != "FILLED" || o.OrderID == nil || claimed[*o.OrderID] || o.CloseTime == nil {
			continue
		}
		if !trader.OrderIsSell(o) || trader.OrderSymbol(o) != sym {
			continue
		}
		d := o.CloseTime.Time.Sub(at)
		if d < -backfillBefore || d > backfillAfter {
			continue
		}
		if _, _, has := o.FillPrice(""); !has {
			continue
		}
		if best == nil || o.CloseTime.Time.Before(best.CloseTime.Time) {
			best = o
		}
	}
	if best == nil {
		return 0
	}
	claimed[*best.OrderID] = true
	px, _, _ := best.FillPrice("")
	return px
}

// parseStoredTime reads a time as the database stored it: Go's time.Time printed by the driver,
// with or without a fractional second and a zone name.
func parseStoredTime(s string) (time.Time, bool) {
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700 -0700",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700 -0700",
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
