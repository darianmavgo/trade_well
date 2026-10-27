package stage_orders

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Row is one outcome (BUY/NO_BUY/SKIPPED/ERROR) for the operator email
// report. Deliberately not pkg/send_email's type -- stage_orders stays
// decoupled from the email package and lets the caller (cmd/orchestrator)
// translate.
type Row struct {
	Account  string
	Strategy string
	Symbol   string
	Signal   string
	Status   string
	Detail   string
}

// Result summarizes one Run call.
type Result struct {
	SymbolsScanned int
	SignalsFound   int
	OrdersStaged   int
	Details        []string
	Rows           []Row
}

// Run sizes scan_market's candidate signals against acc's live cash/equity
// and stages orders for the ones that fit. members must be the stack's
// strategies, resolved and ordered by priority by the caller (pkg/strategies)
// -- Run never looks strategies up in the global registry itself.
func Run(ctx context.Context, db *sql.DB, acc *trader.ResolvedAccount, stackID string, signals []scan_market.Signal, members []strategy.Strategy, maxTradingDays int, dryRun bool, profitPctOverride float64) (*Result, error) {
	res := &Result{}
	now := time.Now().UTC()
	var accNum string
	if acc != nil {
		accNum = acc.AccountNumber
	}

	if db != nil {
		if err := EnsureSchema(db); err != nil {
			return res, fmt.Errorf("stage_orders: ensure schema: %w", err)
		}
	}

	memberByID := make(map[string]strategy.Strategy, len(members))
	for _, m := range members {
		memberByID[m.ID()] = m
	}

	held := heldSymbols(db, acc, accNum)
	addPendingStagedSymbols(db, accNum, held)

	equity, cash := accountCapital(acc)
	res.Details = append(res.Details, fmt.Sprintf("Ledger: equity $%.2f, cash $%.2f (overlays use leftover only; no live preemption).", equity, cash))

	// A limit-entry strategy's orders are watched, not placed at once (see WatchEntries),
	// and one that takes a name out of the rotation after a fill skips the names that
	// filled earlier this month. Everything else is sized against the cash left, as ever.
	var normal, watch []scan_market.Signal
	var cooled []SizedEntry
	filledByMonth := map[string]map[string]bool{}
	for _, c := range signals {
		var cfg strategy.StrategyConfig
		if m := memberByID[c.StrategyID]; m != nil {
			cfg = m.DefaultConfig()
		}
		if cfg.OneFillPerMonth {
			month := SessionMonth(c.NextSession, now)
			filled, ok := filledByMonth[month]
			if !ok {
				filled = FilledThisMonth(db, accNum, month)
				filledByMonth[month] = filled
			}
			if filled[c.StrategyID+"|"+strings.ToUpper(strings.TrimSpace(c.Symbol))] {
				cooled = append(cooled, SizedEntry{Signal: c, Reason: "filled earlier in " + month + ": out of the rotation until next month"})
				continue
			}
		}
		if cfg.EntryLimitPct > 0 {
			watch = append(watch, c)
		} else {
			normal = append(normal, c)
		}
	}
	// The risk state limits new entries only: a halt skips every one, a drawdown step scales
	// the equity they are sized against, and the exposure cap trims the cash they may use.
	rs, rerr := risk.Load(db, accNum)
	var sized []SizedEntry
	if rerr != nil && !dryRun {
		// Fail closed: the halt and the caps live in the risk state, so a live run that cannot
		// read it stages no new entries. A dry run reports it and sizes as if unrestricted.
		res.Details = append(res.Details, fmt.Sprintf("Risk: state unreadable, no entries staged: %v", rerr))
		for _, c := range append(append([]scan_market.Signal(nil), normal...), watch...) {
			sized = append(sized, SizedEntry{Signal: c, Reason: "risk state unreadable"})
		}
	} else if rs.HaltEntries {
		res.Details = append(res.Details, "Risk: "+rs.String())
		for _, c := range append(append([]scan_market.Signal(nil), normal...), watch...) {
			sized = append(sized, SizedEntry{Signal: c, Reason: "risk halt: " + strings.Join(rs.Reasons, "; ")})
		}
	} else {
		if rerr != nil {
			res.Details = append(res.Details, fmt.Sprintf("risk state unreadable, sizing unrestricted (dry run): %v", rerr))
		}
		if room := rs.GrossRoom(equity, heldMarketValue(acc)); room < cash {
			res.Details = append(res.Details, fmt.Sprintf("Risk: exposure cap %.0f%% of equity leaves $%.2f for new entries.", rs.MaxGross*100, room))
			cash = room
		}
		if rs.Scale < 1 {
			res.Details = append(res.Details, "Risk: "+rs.String())
			equity *= rs.Scale
		}
		sized = SizeStackEntries(equity, cash, held, normal)
		sized = append(sized, WatchEntries(equity, held, watch)...)
	}
	sized = append(sized, cooled...)
	res.SymbolsScanned = len(signals)
	var notes []string
	var firstBuy *SizedEntry
	var latestDate string
	for _, e := range sized {
		if e.Raw.Date != "" {
			latestDate = e.Raw.Date
		}
		if e.Quantity <= 0 {
			msg := fmt.Sprintf("[%s/%s] skip: %s", e.StrategyID, e.Symbol, e.Reason)
			res.Details = append(res.Details, msg)
			notes = append(notes, msg)
			res.Rows = append(res.Rows, Row{
				Account: accNum, Strategy: e.StrategyID, Symbol: e.Symbol,
				Signal: "BUY", Status: "SKIPPED", Detail: e.Reason,
			})
			continue
		}
		res.SignalsFound++
		strat := memberByID[e.StrategyID]
		holdDays, takeProfit, stopLoss := strategy.WindowFor(strat, e.Raw)
		if maxTradingDays > 0 {
			holdDays = maxTradingDays
		}
		if profitPctOverride > 0 && e.Price > 0 {
			takeProfit = e.Price * (1 + profitPctOverride/100.0)
		}
		detail := fmt.Sprintf("[%s/%s] BUY P%d: %.0f shares @ $%.2f (Est: $%.2f) -> TP $%.2f, SL $%.2f, Hold %d days",
			e.StrategyID, e.Symbol, e.Priority, e.Quantity, e.Price, e.Notional, takeProfit, stopLoss, holdDays)
		res.Details = append(res.Details, detail)
		notes = append(notes, detail)
		rowDetail := fmt.Sprintf("%.0f shares @ $%.2f (Est: $%.2f) -> TP $%.2f, SL $%.2f, hold %d day(s)",
			e.Quantity, e.Price, e.Notional, takeProfit, stopLoss, holdDays)
		if e.Watch {
			rowDetail = "WATCH day limit, placed if the low reaches it: " + rowDetail
		}
		if firstBuy == nil {
			cp := e
			firstBuy = &cp
		}

		status := "PENDING"
		if dryRun {
			status = "SIMULATED"
		}
		var signalID int64
		if db != nil {
			id, err := scan_market.UpsertSignalDay(db, e.StrategyID, e.Raw.Date, e.Symbol, "BUY", e.Price, detail, status, now)
			if err != nil {
				res.Details = append(res.Details, fmt.Sprintf("Failed to record strategy signal: %v", err))
			} else {
				signalID = id
			}
		}
		if dryRun {
			res.Rows = append(res.Rows, Row{
				Account: accNum, Strategy: e.StrategyID, Symbol: e.Symbol,
				Signal: "BUY", Status: "SIMULATED", Detail: rowDetail,
			})
			continue
		}
		if db == nil {
			res.Rows = append(res.Rows, Row{
				Account: accNum, Strategy: e.StrategyID, Symbol: e.Symbol,
				Signal: "BUY", Status: "ERROR", Detail: rowDetail + " -- no database connection; order not staged",
			})
			continue
		}
		stagedStatus := StagedOrderPending
		entryDuration := ""
		if e.Watch {
			stagedStatus = StagedOrderWatching
			entryDuration = EntryDurationDay
		}
		stagedID, err := InsertStagedOrder(db, &StagedOrder{
			SignalID:        signalID,
			AccountNumber:   accNum,
			StrategyName:    e.StrategyID,
			Symbol:          e.Symbol,
			Instruction:     trader.InstructionBuy,
			Quantity:        e.Quantity,
			EntryPrice:      e.Price,
			TakeProfitPrice: takeProfit,
			StopLossPrice:   stopLoss,
			MaxTradingDays:  holdDays,
			Status:          stagedStatus,
			StagedAt:        now,
			EntryDuration:   entryDuration,
			SessionDate:     e.NextSession,
			AllocPct:        e.AllocPct,
		})
		if err != nil {
			res.Details = append(res.Details, fmt.Sprintf("[%s] Failed to stage order: %v", e.Symbol, err))
			if signalID > 0 {
				_, _ = db.Exec(orchestratorsql.Q("stage_orders/run"), signalID)
			}
			res.Rows = append(res.Rows, Row{
				Account: accNum, Strategy: e.StrategyID, Symbol: e.Symbol,
				Signal: "BUY", Status: "ERROR", Detail: rowDetail + fmt.Sprintf(" -- failed to stage order: %v", err),
			})
			continue
		}
		res.OrdersStaged++
		res.Details = append(res.Details, fmt.Sprintf("[%s] Order staged (stage_order id=%d).", e.Symbol, stagedID))
		res.Rows = append(res.Rows, Row{
			Account: accNum, Strategy: e.StrategyID, Symbol: e.Symbol,
			Signal: "BUY", Status: "STAGED", Detail: fmt.Sprintf("%s (stage_order id=%d)", rowDetail, stagedID),
		})
		if signalID > 0 {
			_, _ = db.Exec(orchestratorsql.Q("stage_orders/run_2"), signalID)
		}
	}

	if db != nil {
		stackType, stackSym, stackPx, stackStatus := "NO_BUY", stackID, 0.0, "EVALUATED"
		if firstBuy != nil && firstBuy.Quantity > 0 {
			stackType, stackSym, stackPx = "BUY", firstBuy.Symbol, firstBuy.Price
			stackStatus = "PENDING"
			if dryRun {
				stackStatus = "SIMULATED"
			} else if res.OrdersStaged > 0 {
				stackStatus = "STAGED"
			}
		}
		_, _ = scan_market.UpsertSignalDay(db, stackID, latestDate, stackSym, stackType, stackPx, strings.Join(notes, "\n"), stackStatus, now)
	}

	return res, nil
}
