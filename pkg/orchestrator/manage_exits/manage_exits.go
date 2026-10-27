package manage_exits

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Row is one outcome for the operator email report. Deliberately not
// pkg/send_email's type -- manage_exits stays decoupled from the email
// package and lets the caller (cmd/orchestrator) translate.
type Row struct {
	Account  string
	Strategy string
	Symbol   string
	Signal   string
	Status   string
	Detail   string
}

// Result records the actions taken during the exit window evaluation.
type Result struct {
	TotalChecked     int
	ClosedProfit     int
	ClosedTimeExpiry int
	StillActive      int
	// SkippedNoPosition counts manage_exit rows marked OPEN for which the
	// broker reports no actual held quantity -- exit math was not computed
	// and no exit order was placed for these; see brokerHeldQuantities.
	SkippedNoPosition int
	// SettledNoPosition counts those rows closed from a filled broker sell; see settleOrphan.
	SettledNoPosition int
	Details           []string
	Rows              []Row
}

// brokerHeldQuantities returns, per uppercased symbol, the long quantity the
// broker snapshot in acc actually reports. A manage_exit row with no entry
// here (or a non-positive one) does not correspond to a real held position
// -- a stale/duplicate row, a manual sell outside this system, or a race
// with account_state -- and exit math must not run against it: there is
// nothing real to price a target against, and a market sell would either
// fail outright or (worse) sell shares from an unrelated position.
func brokerHeldQuantities(acc *trader.ResolvedAccount) map[string]float64 {
	held := make(map[string]float64)
	if acc == nil {
		return held
	}
	for _, p := range acc.Details.Positions {
		if p.LongQuantity <= 0 {
			continue
		}
		if sym := strings.ToUpper(strings.TrimSpace(p.Instrument.Symbol)); sym != "" {
			held[sym] += p.LongQuantity
		}
	}
	return held
}

// Run evaluates OPEN manage_exit rows and submits live exits when either the
// profit target is reached or the holding window has expired. dryRun
// previews without calling PlaceOrder / mutating rows (except we still bind
// a discovered exit_order_id so the next live run can see fills). strats is
// the account's resolved strategy set (see pkg/strategies) used to fall back
// on a strategy's DefaultConfig when a row is missing its own window/target.
func Run(
	ctx context.Context,
	db *sql.DB,
	tc *trader.TraderClient,
	acc *trader.ResolvedAccount,
	strats []strategy.Strategy,
	now time.Time,
	dryRun bool,
) (*Result, error) {
	if db == nil {
		return nil, fmt.Errorf("database handle is nil")
	}
	if err := EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("manage_exits: ensure schema: %w", err)
	}

	stratByID := make(map[string]strategy.Strategy, len(strats))
	for _, s := range strats {
		stratByID[s.ID()] = s
	}

	res := &Result{}
	var recentOrders []schwab.Order
	if tc != nil {
		raw, listErr := tc.Client.ListAccountOrders(ctx, acc.HashValue, schwab.OrderListRequest{
			From: now.AddDate(0, 0, -30),
			To:   now,
		})
		recentOrders = schwab.FlattenOrders(raw)
		res.Details = append(res.Details, fmt.Sprintf("broker returned %d order(s) for %s in the last 30 days (hash set: %v)", len(raw), acc.AccountNumber, acc.HashValue != ""))
		if listErr != nil {
			res.Details = append(res.Details, fmt.Sprintf("could not list %s's orders from the broker: %v", acc.AccountNumber, listErr))
		}
	}

	// Entries the broker accepted earlier but that were not confirmed filled: once the
	// entry order has filled (or died), settle them from the broker's own record.
	resolvePending(db, acc, recentOrders, now, dryRun, res)

	// Held positions this database has no row for (entered from another run's database).
	adoptUntracked(db, acc, strats, recentOrders, now, dryRun, res)

	// Closed rows recorded before fills were read (or whose exit was settled from a stop's trigger)
	// get the broker's real prices, and their P&L is measured from them.
	for _, line := range BackfillFills(db, acc.AccountNumber, recentOrders, dryRun) {
		res.Details = append(res.Details, line)
	}

	openTrades, err := GetOpenTrades(db, acc.AccountNumber)
	if err != nil {
		res.Rows = append(res.Rows, Row{
			Account: acc.AccountNumber, Signal: "ERROR", Status: "ERROR",
			Detail: fmt.Sprintf("failed to load open trades: %v", err),
		})
		return res, fmt.Errorf("failed to load open trades: %w", err)
	}

	res.TotalChecked = len(openTrades)
	if len(openTrades) == 0 {
		res.Details = append(res.Details, "No open strategy trades found in database.")
		return res, nil
	}

	heldQty := brokerHeldQuantities(acc)
	claimedSells := map[int64]bool{}

	for _, trade := range openTrades {
		sym := strings.ToUpper(trade.Symbol)
		recordEntryFill(db, recentOrders, &trade, dryRun)

		// The entry order's own exit legs decide whether this row is still open. A
		// filled take-profit or stop is this position's exit: settle the row from it
		// and never sell, whatever the account holds in the same symbol today.
		legs := ExitLegs(recentOrders, trade.EntryOrderID)
		if leg := FilledExitLeg(legs); leg != nil {
			row := Row{Account: acc.AccountNumber, Strategy: trade.StrategyName, Symbol: sym}
			settleOrphan(db, trade, leg, dryRun, res, &row)
			res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, row.Detail))
			res.Rows = append(res.Rows, row)
			continue
		}

		if heldQty[sym] <= 0 {
			// The broker already sold it (take-profit, stop, or a manual flatten): settle
			// the row from that sell. Without one, leave it and report.
			if sell := settlingSell(recentOrders, trade, claimedSells); sell != nil {
				claimedSells[*sell.OrderID] = true
				row := Row{Account: acc.AccountNumber, Strategy: trade.StrategyName, Symbol: sym}
				if settleOrphan(db, trade, sell, dryRun, res, &row) {
					res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, row.Detail))
					res.Rows = append(res.Rows, row)
					continue
				}
				res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, row.Detail))
			}
			res.SkippedNoPosition++
			detail := "manage_exit marks this OPEN but the broker reports no held quantity for this symbol -- skipping exit calculation (see check_health's orphan_active_trade finding)"
			res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail))
			res.Rows = append(res.Rows, Row{
				Account: acc.AccountNumber, Strategy: trade.StrategyName, Symbol: sym,
				Signal: "NO_POSITION", Status: "SKIPPED", Detail: detail,
			})
			continue
		}

		correctExitDate(db, &trade, recentOrders, dryRun, res)
		daysHeld := calendar.TradingDaysBetween(sessionDate(trade.EntryTime), sessionDate(now))

		if oid := workingExitLegID(legs); oid != "" && trade.ExitOrderID != oid {
			trade.ExitOrderID = oid
			if !dryRun {
				_ = SetExitOrderID(db, trade.ID, oid)
			}
			res.Details = append(res.Details, fmt.Sprintf("[%s] Bound resting profit-taker order %s.", sym, oid))
		}

		last := 0.0
		if tc != nil {
			if q, errQ := tc.Client.GetQuote(ctx, trade.Symbol); errQ == nil {
				last = trader.ExtractQuoteLastPrice(q)
			}
		}

		holdDays, target := windowForTrade(trade, stratByID)
		throughTarget := target > 0 && last > 0 && last >= target
		// Expiry is the stored exit date: today (New York) has reached it.
		windowExpired := holdDays > 0 && exitDue(trade, now)

		row := Row{Account: acc.AccountNumber, Strategy: trade.StrategyName, Symbol: sym}

		if (throughTarget || windowExpired) && !dryRun {
			if why := cannotSell(recentOrders, trade, heldQty[sym]); why != "" {
				res.StillActive++
				res.Details = append(res.Details, fmt.Sprintf("[%s] exit due but not sold: %s.", sym, why))
				row.Signal, row.Status, row.Detail = "EXIT_BLOCKED", "ERROR", why
				res.Rows = append(res.Rows, row)
				continue
			}
		}

		if throughTarget {
			row.Signal = "EXIT_PROFIT"
			const reason = "QUOTE_THROUGH_TARGET"
			exitPrice := last
			if exitPrice <= 0 {
				exitPrice = trade.TargetProfitPrice
			}
			detail := fmt.Sprintf("Profit target $%.2f hit (last $%.2f, held %d days)", target, exitPrice, daysHeld)
			if dryRun {
				res.ClosedProfit++
				res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail)+" DRY-RUN: would close as CLOSED_PROFIT.")
				row.Status = "SIMULATED"
				row.Detail = detail + " -- would close as CLOSED_PROFIT"
				res.Rows = append(res.Rows, row)
				continue
			}
			if err := ExitAtMarket(ctx, tc, acc, trade.Symbol, trade.Quantity, recentOrders); err != nil {
				res.StillActive++
				res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail)+fmt.Sprintf(" MARKET SELL failed: %v", err))
				row.Status = "ERROR"
				row.Detail = detail + fmt.Sprintf(" -- market sell failed: %v", err)
				res.Rows = append(res.Rows, row)
				continue
			}
			res.ClosedProfit++
			res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail)+" Closing trade.")
			_ = CloseActiveTrade(db, trade.ID, TradeStatusClosedProfit, reason, exitPrice, now)
			row.Status = "CLOSED_PROFIT"
			row.Detail = fmt.Sprintf("closed at $%.2f (held %d days)", exitPrice, daysHeld)
			res.Rows = append(res.Rows, row)
			continue
		}

		if windowExpired {
			row.Signal = "EXIT_TIME_EXPIRY"
			detail := fmt.Sprintf("Held %d/%d trading days; exit date %s (%s) reached -> MARKET EXIT", daysHeld, holdDays, trade.ExitDate, trade.ExitDateSource)
			if dryRun {
				res.ClosedTimeExpiry++
				res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail)+" DRY-RUN: would submit market sell.")
				row.Status = "SIMULATED"
				row.Detail = detail + " -- would submit market sell"
				res.Rows = append(res.Rows, row)
				continue
			}
			if err := ExitAtMarket(ctx, tc, acc, trade.Symbol, trade.Quantity, recentOrders); err != nil {
				res.StillActive++
				res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail)+fmt.Sprintf(" Failed: %v", err))
				row.Status = "ERROR"
				row.Detail = detail + fmt.Sprintf(" -- market sell failed: %v", err)
				res.Rows = append(res.Rows, row)
				continue
			}
			exitPrice := trade.EntryPrice
			if last > 0 {
				exitPrice = last
			}
			res.ClosedTimeExpiry++
			res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail)+" Submitted.")
			_ = CloseActiveTrade(db, trade.ID, TradeStatusClosedTimeExpiry,
				"WINDOW_EXPIRED_MARKET_SELL", exitPrice, now)
			row.Status = "CLOSED_TIME_EXPIRATION"
			row.Detail = fmt.Sprintf("market sell submitted at ~$%.2f (held %d/%d days)", exitPrice, daysHeld, holdDays)
			res.Rows = append(res.Rows, row)
			continue
		}

		res.StillActive++
		_ = UpdateTradingDaysHeld(db, trade.ID, daysHeld)
		remaining := holdDays - daysHeld
		res.Details = append(res.Details, fmt.Sprintf("[%s] Active (held %d/%d days, %d remaining, strategy %s). Target: $%.2f last: $%.2f",
			sym, daysHeld, holdDays, remaining, trade.StrategyName, target, last))
		row.Signal = "HOLD"
		row.Status = "OPEN"
		row.Detail = fmt.Sprintf("held %d/%d days (%d remaining); exit date %s (%s); target $%.2f, last $%.2f", daysHeld, holdDays, remaining, trade.ExitDate, trade.ExitDateSource, target, last)
		res.Rows = append(res.Rows, row)
	}

	return res, nil
}

// ExitLegs are the child orders (the OCO take-profit and stop) of the entry order
// entryOrderID, matched by order id and nothing else.
func ExitLegs(orders []schwab.Order, entryOrderID string) []schwab.Order {
	if strings.TrimSpace(entryOrderID) == "" {
		return nil
	}
	for _, o := range schwab.FlattenOrders(orders) {
		if o.OrderID == nil || fmt.Sprintf("%d", *o.OrderID) != entryOrderID {
			continue
		}
		if all := schwab.FlattenOrders([]schwab.Order{o}); len(all) > 1 {
			return all[1:]
		}
		return nil
	}
	return nil
}

// FilledExitLeg is the exit leg that filled (the position's real exit), or nil.
func FilledExitLeg(legs []schwab.Order) *schwab.Order {
	for i := range legs {
		o := &legs[i]
		if strings.ToUpper(o.Status) == "FILLED" && o.OrderID != nil && o.CloseTime != nil && trader.OrderIsSell(o) {
			return o
		}
	}
	return nil
}

// workingExitLegID is the id of the resting take-profit limit among legs, or "".
func workingExitLegID(legs []schwab.Order) string {
	for i := range legs {
		o := &legs[i]
		switch strings.ToUpper(o.Status) {
		case "WORKING", "PENDING_ACTIVATION", "QUEUED":
		default:
			continue
		}
		if strings.EqualFold(o.OrderType, trader.OrderTypeLimit) && trader.OrderIsSell(o) && o.OrderID != nil {
			return fmt.Sprintf("%d", *o.OrderID)
		}
	}
	return ""
}

// cannotSell is why a due exit must not go out as a market sell ("" when it may):
// the entry order is not a confirmed fill, or the broker holds fewer shares than
// the row owns.
func cannotSell(orders []schwab.Order, trade Trade, held float64) string {
	if st, _ := EntryFill(orders, trade.EntryOrderID); st != "" && st != "FILLED" {
		return fmt.Sprintf("entry order %s is %s, not filled", trade.EntryOrderID, st)
	}
	if held < trade.Quantity {
		return fmt.Sprintf("broker holds %.0f %s, row owns %.0f", held, trade.Symbol, trade.Quantity)
	}
	return ""
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// exitDue reports whether trade has reached its exit date: today (New York) is on or
// after it. A trade with no exit date or no holding window is never due by date.
func exitDue(trade Trade, now time.Time) bool {
	return trade.MaxTradingDays > 0 && trade.ExitDate != "" && TodayET(now) >= trade.ExitDate
}

// correctExitDate makes trade's exit date come from the entry order's real fill date.
// A row whose date is still an estimate is looked up by entry order id in orders; when
// that order FILLED, the fill time is recorded and the date recomputed (trade is updated
// in place). A row from before exit dates existed gets a provisional one. dryRun updates
// trade in memory but writes nothing.
func correctExitDate(db *sql.DB, trade *Trade, orders []schwab.Order, dryRun bool, res *Result) {
	sym := strings.ToUpper(trade.Symbol)
	if trade.ExitDateSource != ExitDateFill {
		if st, filledAt := EntryFill(orders, trade.EntryOrderID); st == "FILLED" && !filledAt.IsZero() {
			newDate := ExitDateFor(filledAt, trade.MaxTradingDays)
			res.Details = append(res.Details, fmt.Sprintf("[%s] Exit date corrected from the entry fill: %s (was %s, %s); order %s filled %s.",
				sym, newDate, dashIfEmpty(trade.ExitDate), dashIfEmpty(trade.ExitDateSource), trade.EntryOrderID, filledAt.UTC().Format(time.RFC3339)))
			if !dryRun {
				if err := SetFill(db, trade.ID, filledAt); err != nil {
					res.Details = append(res.Details, fmt.Sprintf("[%s] failed to record the fill: %v", sym, err))
				}
			}
			ft := filledAt
			trade.EntryTime, trade.EntryFilledAt, trade.ExitDate, trade.ExitDateSource = filledAt, &ft, newDate, ExitDateFill
		}
	}
	if trade.ExitDate == "" { // a row from before exit dates existed
		trade.ExitDate, trade.ExitDateSource = ExitDateFor(trade.EntryTime, trade.MaxTradingDays), ExitDateProvisional
		if !dryRun {
			_ = SetExitDate(db, trade.ID, trade.ExitDate, trade.ExitDateSource)
		}
	}
}
