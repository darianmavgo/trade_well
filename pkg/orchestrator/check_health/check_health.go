package check_health

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// addFinding appends f to report.Findings, bumps the matching severity
// counter, mirrors it into Recommendations for the existing plain-text
// display/email path, and (if db is non-nil) persists it. Callers pass a nil
// db for a finding RunSQLHealthChecks already persisted itself.
func (report *Result) addFinding(db *sql.DB, f Finding) {
	report.Findings = append(report.Findings, f)
	switch f.Severity {
	case "CRITICAL":
		report.CriticalFindings++
	default:
		report.WarningFindings++
	}
	label := f.Subject
	if label == "" {
		label = f.CheckName
	}
	report.Recommendations = append(report.Recommendations, fmt.Sprintf("[%s/%s] %s", f.Severity, label, f.Detail))
	if db != nil {
		_ = RecordFinding(db, &f)
	}
}

// Run performs a deep inspection of all positions, working orders, and
// scheduled exits. strats is the caller's pre-resolved
// map[string]strategy.Strategy (via pkg/strategies) used by the
// strategy-contract audit -- Run never calls backtestgosqlite's global
// registry itself.
func Run(
	ctx context.Context,
	db *sql.DB,
	tc *trader.TraderClient,
	acc *trader.ResolvedAccount,
	strats map[string]strategy.Strategy,
) (*Result, error) {
	if err := EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("check_health: ensure schema: %w", err)
	}
	now := time.Now().UTC()
	cash := trader.GetBuyingPower(&acc.Details)
	liqVal := acc.Details.CurrentBalances.LiquidationValue

	report := &Result{
		AccountNumber:    acc.AccountNumber,
		Timestamp:        now,
		CashAvailable:    cash,
		LiquidationValue: liqVal,
		OverallStatus:    "HEALTHY",
	}
	workingOrders := make(map[string][]schwab.Order)
	if tc != nil {
		req := schwab.OrderListRequest{
			From:   now.AddDate(0, 0, -30),
			To:     now,
			Status: "WORKING",
		}
		orders, err := tc.Client.ListAccountOrders(ctx, acc.HashValue, req)
		if err == nil {
			// Flatten brackets: the take-profit and stop of a filled TRIGGER entry are
			// its child orders, not top-level orders.
			for _, ord := range manage_exits.WorkingOrders(orders) {
				if !trader.OrderIsSell(&ord) {
					continue // a working BUY is an entry, not protection
				}
				legs, errLegs := trader.ParseOrderLegs(&ord)
				if errLegs == nil && len(legs) > 0 {
					sym := strings.ToUpper(legs[0].Instrument.Symbol)
					workingOrders[sym] = append(workingOrders[sym], ord)
				}
			}
		}
	}

	// 2. Fetch active trade schedules from the orchestrator database (Turso)
	scheduledTrades := make(map[string]manage_exits.Trade)
	var openTrades []manage_exits.Trade
	if db != nil {
		var err error
		openTrades, err = manage_exits.GetOpenTrades(db, acc.AccountNumber)
		if err == nil {
			for _, t := range openTrades {
				scheduledTrades[strings.ToUpper(t.Symbol)] = t
			}
		}
	}

	// 3. Inspect each open position
	report.TotalPositions = len(acc.Details.Positions)
	activeSymbols := make(map[string]bool)

	for _, pos := range acc.Details.Positions {
		sym := strings.ToUpper(pos.Instrument.Symbol)
		activeSymbols[sym] = true

		ph := PositionHealth{
			Symbol:       sym,
			Quantity:     pos.LongQuantity,
			AveragePrice: pos.AveragePrice,
			MarketValue:  pos.MarketValue,
			Status:       "SECURE",
		}

		// Check for working profit-taker and stop-loss orders
		orders := workingOrders[sym]
		for _, ord := range orders {
			if ord.OrderType == trader.OrderTypeLimit && ord.Price != nil {
				ph.HasProfitTaker = true
				ph.ProfitTakerPrice = *ord.Price
			}
			if ord.OrderType == trader.OrderTypeStop || ord.OrderType == trader.OrderTypeStopLimit {
				ph.HasStopLoss = true
				if ord.StopPrice != nil {
					ph.StopLossPrice = *ord.StopPrice
				}
			}
		}

		// Check for scheduled exit tracking in Turso
		var scheduled manage_exits.Trade
		if st, ok := scheduledTrades[sym]; ok {
			scheduled = st
			ph.HasScheduledExit = true
			ph.DaysHeld = calendar.TradingDaysBetween(st.EntryTime, now)
			ph.MaxDays = st.MaxTradingDays
			if !ph.HasProfitTaker && st.TargetProfitPrice > 0 {
				ph.HasProfitTaker = true
				ph.ProfitTakerPrice = st.TargetProfitPrice
			}
			if !ph.HasStopLoss && st.StopLossPrice > 0 {
				ph.HasStopLoss = true
				ph.StopLossPrice = st.StopLossPrice
			}
		} else {
			ph.Warnings = append(ph.Warnings, "No scheduled exit tracking entry in database")
		}

		// Priority audit: accounting, strategy, take-profit/stop/hold window/exit date.
		var tradePtr *manage_exits.Trade
		if ph.HasScheduledExit {
			tradePtr = &scheduled
		}
		brokerTP, brokerStop := 0.0, 0.0
		for _, ord := range workingOrders[sym] {
			if ord.OrderType == trader.OrderTypeLimit && ord.Price != nil {
				brokerTP = *ord.Price
			}
			if (ord.OrderType == trader.OrderTypeStop || ord.OrderType == trader.OrderTypeStopLimit) && ord.StopPrice != nil {
				brokerStop = *ord.StopPrice
			}
		}
		audit, pissues := AuditPosition(acc.AccountNumber, sym, pos.LongQuantity, pos.AveragePrice, tradePtr, brokerTP, brokerStop, strats, now)
		for _, pi := range pissues {
			ph.Warnings = append(ph.Warnings, pi.detail)
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: pi.check,
				Category: "position", Severity: pi.severity, Subject: sym, Detail: pi.detail,
			})
		}
		report.PositionAudits = append(report.PositionAudits, audit)
		if pissues != nil && ph.Status == "SECURE" && audit.Status == "CRITICAL" {
			ph.Status = "UNPROTECTED"
		}

		// A tracked position recorded with no take-profit (a strategy that exits on hold days,
		// such as the hmm family) is not expected to have a profit taker at the broker.
		noTPByDesign := ph.HasScheduledExit && scheduled.TargetProfitPrice <= 0
		if !ph.HasProfitTaker && !noTPByDesign {
			ph.Warnings = append(ph.Warnings, "Missing active Take-Profit limit order")
			ph.Status = "UNPROTECTED"
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: "missing_profit_taker",
				Category: "position_protection", Severity: "WARNING",
				Subject: sym, Detail: "Missing an active take-profit limit order",
			})
		}

		if !ph.HasStopLoss {
			ph.Warnings = append(ph.Warnings, "No crisis stop-loss order placed")
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: "missing_stop_loss",
				Category: "position_protection", Severity: "WARNING",
				Subject: sym, Detail: "No crisis stop-loss order placed",
			})
		}

		// A "profit taker" resting at or below cost would lock in a loss, not
		// a profit -- almost always a stale/mis-set order from a prior signal.
		if ph.HasProfitTaker && ph.ProfitTakerPrice > 0 && pos.AveragePrice > 0 && ph.ProfitTakerPrice <= pos.AveragePrice {
			ph.Warnings = append(ph.Warnings, "Take-profit order is at or below average cost")
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: "take_profit_below_cost",
				Category: "position_protection", Severity: "CRITICAL",
				Subject: sym, Detail: fmt.Sprintf("Take-profit $%.2f is at or below average cost $%.2f", ph.ProfitTakerPrice, pos.AveragePrice),
			})
		}

		if ph.Status == "SECURE" {
			report.ProtectedPositions++
		} else {
			report.UnprotectedPositions++
		}

		report.Positions = append(report.Positions, ph)
	}

	if db != nil {
		if len(report.PositionAudits) == 0 { // marker: this run saw no positions
			_ = RecordPositionAudit(db, PositionAudit{AccountNumber: acc.AccountNumber, RunAt: now, Status: "OK"})
		}
		for _, pa := range report.PositionAudits {
			_ = RecordPositionAudit(db, pa)
		}
	}

	// 4. Check for orphan orders (working sell orders on symbols where position is 0)
	for sym, ords := range workingOrders {
		if !activeSymbols[sym] {
			report.OrphanOrders += len(ords)
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: "orphan_working_order",
				Category: "order_hygiene", Severity: "CRITICAL",
				Subject: sym, Detail: fmt.Sprintf("%d working order(s) on %s with 0 shares held -- review or cancel", len(ords), sym),
			})
		}
	}

	// 5. Strategy-contract audit: every PENDING staged order and
	// every open, actively-managed trade gets its recorded terms (hold
	// window, take-profit, stop-loss, allocation) checked against what its
	// originating strategy's own DefaultConfig() actually dictates.
	if db != nil {
		auditStrategyContract(report, db, acc, strats, openTrades, now)
	}

	// 6. Data-driven checks defined in the check_health table, executed
	// against the Turso account_state mirror (job-lifecycle drift, staleness, risk
	// limits, data integrity) -- see schema.go's defaultHealthChecks.
	if db != nil {
		sqlFindings, err := RunSQLHealthChecks(db, acc.AccountNumber, now)
		if err != nil {
			report.Recommendations = append(report.Recommendations, fmt.Sprintf("Failed to run SQL-defined health checks: %v", err))
		}
		for _, f := range sqlFindings {
			report.addFinding(nil, f)
		}
	}

	// 7. Roll every finding's severity up into one overall status.
	report.OverallStatus = "HEALTHY"
	if report.WarningFindings > 0 {
		report.OverallStatus = "WARNING"
	}
	if report.CriticalFindings > 0 {
		report.OverallStatus = "ACTION_REQUIRED"
	}

	// 8. Persist the account-level rollup into SQLite. Every individual
	// finding was already persisted as it was recorded: live-data findings
	// via addFinding's db argument above, SQL-defined findings by
	// RunSQLHealthChecks itself.
	if db != nil {
		notes := fmt.Sprintf("Positions: %d (Protected: %d, Unprotected: %d), Orphan Orders: %d, Findings: %d critical / %d warning",
			report.TotalPositions, report.ProtectedPositions, report.UnprotectedPositions, report.OrphanOrders,
			report.CriticalFindings, report.WarningFindings)
		_ = RecordAudit(db, &Audit{
			AccountNumber:        acc.AccountNumber,
			Timestamp:            now,
			TotalPositions:       report.TotalPositions,
			ProtectedPositions:   report.ProtectedPositions,
			UnprotectedPositions: report.UnprotectedPositions,
			OrphanOrders:         report.OrphanOrders,
			Status:               report.OverallStatus,
			Notes:                notes,
			CriticalFindings:     report.CriticalFindings,
			WarningFindings:      report.WarningFindings,
		})
	}

	return report, nil
}
