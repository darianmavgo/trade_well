package manage_exits

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// adoptUntracked gives every held position that has no manage_exit row a managed
// trade with a fill-based exit date. A position lands here when its entry was placed
// by a run whose database this one does not have (a laptop run against the live
// account, a restored older database copy), so it would otherwise be held with no exit date
// and never sold.
//
// It only adopts when it can be sure: exactly one strategy trades the symbol, that
// strategy has a holding window, and the broker shows a FILLED buy on the symbol (its
// close time is the fill time). Anything else is reported in res, never guessed at.
// dryRun reports without writing.
func adoptUntracked(db *sql.DB, acc *trader.ResolvedAccount, strats []strategy.Strategy, orders []schwab.Order, now time.Time, dryRun bool, res *Result) {
	tracked := map[string]bool{}
	rows, err := db.Query(orchestratorsql.Q("manage_exits/adopt_untracked"),
		acc.AccountNumber, TradeStatusOpen, TradeStatusPendingFill)
	if err != nil {
		res.Details = append(res.Details, fmt.Sprintf("adopt: could not read manage_exit: %v", err))
		return
	}
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			tracked[s] = true
		}
	}
	rows.Close()

	held := brokerHeldQuantities(acc)
	syms := make([]string, 0, len(held))
	for sym := range held {
		if !tracked[sym] {
			syms = append(syms, sym)
		}
	}
	sort.Strings(syms)

	flat := schwab.FlattenOrders(orders)
	for _, sym := range syms {
		skip := func(why string) {
			detail := "held but has no manage_exit row and cannot be adopted: " + why
			res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, detail))
			res.Rows = append(res.Rows, Row{Account: acc.AccountNumber, Symbol: sym, Signal: "UNTRACKED", Status: "SKIPPED", Detail: detail})
		}
		var owners []strategy.Strategy
		for _, s := range strats {
			p, ok := s.(strategy.RequiredSymbolsProvider)
			if !ok {
				continue
			}
			for _, rs := range p.RequiredSymbols() {
				if strings.EqualFold(rs, sym) {
					owners = append(owners, s)
					break
				}
			}
		}
		if len(owners) != 1 {
			skip(fmt.Sprintf("%d allowlisted strategies trade it", len(owners)))
			continue
		}
		strat := owners[0]
		hold := strat.DefaultConfig().HoldingWindow
		if hold <= 0 {
			skip(fmt.Sprintf("strategy %s has no holding window", strat.ID()))
			continue
		}
		entry := latestFilledBuy(flat, sym)
		if entry == nil {
			var seen []string
			for i := range flat {
				if trader.OrderSymbol(&flat[i]) == sym {
					ct := "no closeTime"
					if flat[i].CloseTime != nil {
						ct = "closed " + flat[i].CloseTime.Time.UTC().Format(time.RFC3339)
					}
					kind := "BUY"
					if trader.OrderIsSell(&flat[i]) {
						kind = "SELL"
					}
					seen = append(seen, fmt.Sprintf("%s %s (%s)", kind, flat[i].Status, ct))
				}
			}
			skip(fmt.Sprintf("no filled buy on it in the %d orders (%d incl. bracket legs) the broker returned for the last 30 days; orders on %s: %v", len(orders), len(flat), sym, seen))
			continue
		}
		filledAt := entry.CloseTime.Time
		price := 0.0
		if entry.Price != nil {
			price = *entry.Price
		}
		oid := ""
		if entry.OrderID != nil {
			oid = fmt.Sprintf("%d", *entry.OrderID)
		}
		exit := ExitDateFor(filledAt, hold)
		detail := fmt.Sprintf("adopted for strategy %s: entry order %s filled %s, hold %d trading days -> exit date %s",
			strat.ID(), oid, filledAt.UTC().Format(time.RFC3339), hold, exit)
		row := Row{Account: acc.AccountNumber, Strategy: strat.ID(), Symbol: sym, Signal: "ADOPT", Status: "ADOPTED", Detail: detail}
		if dryRun {
			row.Status = "SIMULATED"
			row.Detail = detail + " -- DRY-RUN, nothing written"
		} else if _, err := Open(db, OpenTradeParams{
			AccountNumber: acc.AccountNumber, Symbol: sym, StrategyName: strat.ID(), EntryOrderID: oid,
			EntryPrice: price, Quantity: held[sym], EntryTime: filledAt, FilledAt: filledAt, MaxTradingDays: hold,
		}); err != nil {
			row.Status, row.Detail = "ERROR", "adopt failed: "+err.Error()
		}
		res.Details = append(res.Details, fmt.Sprintf("[%s] %s.", sym, row.Detail))
		res.Rows = append(res.Rows, row)
	}
}

// latestFilledBuy is the most recently filled buy order on symbol, nil if none.
func latestFilledBuy(orders []schwab.Order, symbol string) *schwab.Order {
	var best *schwab.Order
	for i := range orders {
		o := &orders[i]
		if !strings.EqualFold(o.Status, "FILLED") || o.CloseTime == nil || trader.OrderSymbol(o) != symbol || trader.OrderIsSell(o) {
			continue
		}
		if best == nil || o.CloseTime.Time.After(best.CloseTime.Time) {
			best = o
		}
	}
	return best
}
