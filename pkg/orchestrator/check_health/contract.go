package check_health

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// contractTolerance absorbs float rounding between what was computed at
// staging time and what re-deriving the strategy's DefaultConfig() produces
// now -- not real drift.
const contractTolerance = 0.005 // 0.5%

// strategyContractIssue is one mismatch between what a staged/submitted/
// executed order actually recorded and what the strategy's own
// DefaultConfig() dictates for an entry at that price.
type strategyContractIssue struct {
	CheckName string
	Severity  string
	Detail    string
}

// auditOrderAgainstStrategy re-derives what strats[strategyName]'s
// DefaultConfig() dictates for a long entry at entryPrice -- hold window,
// take-profit, stop-loss -- and reports every mismatch against what was
// actually recorded. Every strategy this system stages orders for trades
// long only, so the long-side config fields are the right ones to compare
// against. strats is resolved once by cmd/orchestrator via pkg/strategies
// rather than looked up here -- no package below cmd/orchestrator calls
// backtestgosqlite's global registry directly.
//
// This cannot know about a per-signal HoldDaysOverride/TakeProfit baked into
// the original Signal, or a scan_market -max-days/-profit CLI override
// applied on top of the strategy's defaults -- those are legitimate,
// intentional deviations. So every mismatch here is WARNING (a human should
// confirm it was intentional), except a strategy that has vanished from the
// registry entirely, which is CRITICAL -- nothing about that order can be
// verified at all.
func auditOrderAgainstStrategy(strats map[string]strategy.Strategy, strategyName string, entryPrice, takeProfit, stopLoss float64, maxTradingDays int) []strategyContractIssue {
	strat, ok := strats[strategyName]
	if !ok {
		return []strategyContractIssue{{
			CheckName: "unknown_strategy",
			Severity:  "CRITICAL",
			Detail:    fmt.Sprintf("strategy %q was not resolved for this run -- its dictated terms can no longer be verified", strategyName),
		}}
	}
	cfg := strat.DefaultConfig()

	var issues []strategyContractIssue

	if cfg.HoldingWindow > 0 && maxTradingDays != cfg.HoldingWindow {
		issues = append(issues, strategyContractIssue{
			CheckName: "hold_window_mismatch",
			Severity:  "WARNING",
			Detail: fmt.Sprintf("recorded max_trading_days=%d but %s's DefaultConfig dictates %d (may be an intentional -max-days override or a per-signal HoldDaysOverride)",
				maxTradingDays, strategyName, cfg.HoldingWindow),
		})
	}

	if entryPrice > 0 {
		switch {
		case cfg.TakeProfitPct > 0:
			expected := entryPrice * (1 + cfg.TakeProfitPct)
			if pctDiff(takeProfit, expected) > contractTolerance {
				issues = append(issues, strategyContractIssue{
					CheckName: "take_profit_mismatch",
					Severity:  "WARNING",
					Detail: fmt.Sprintf("recorded take-profit $%.2f but %s's TakeProfitPct (%.1f%%) dictates $%.2f (may be an intentional -profit override or a per-signal TakeProfit)",
						takeProfit, strategyName, cfg.TakeProfitPct*100, expected),
				})
			}
		case cfg.TargetPct > 1:
			expected := entryPrice * cfg.TargetPct
			if pctDiff(takeProfit, expected) > contractTolerance {
				issues = append(issues, strategyContractIssue{
					CheckName: "take_profit_mismatch",
					Severity:  "WARNING",
					Detail: fmt.Sprintf("recorded take-profit $%.2f but %s's TargetPct (%.2fx) dictates $%.2f",
						takeProfit, strategyName, cfg.TargetPct, expected),
				})
			}
		}

		// StopLossPct is a direct multiplier on entry price (e.g. 0.93 for a
		// 7% stop), not an offset -- see StrategyConfig's doc comment.
		if cfg.StopLossPct > 0 && cfg.StopLossPct < 1 {
			expected := entryPrice * cfg.StopLossPct
			if stopLoss <= 0 {
				issues = append(issues, strategyContractIssue{
					CheckName: "stop_loss_mismatch",
					Severity:  "WARNING",
					Detail:    fmt.Sprintf("no stop-loss recorded but %s's StopLossPct (%.1f%% below entry) dictates $%.2f", strategyName, (1-cfg.StopLossPct)*100, expected),
				})
			} else if pctDiff(stopLoss, expected) > contractTolerance {
				issues = append(issues, strategyContractIssue{
					CheckName: "stop_loss_mismatch",
					Severity:  "WARNING",
					Detail: fmt.Sprintf("recorded stop-loss $%.2f but %s's StopLossPct (%.1f%% below entry) dictates $%.2f",
						stopLoss, strategyName, (1-cfg.StopLossPct)*100, expected),
				})
			}
		}
	}

	return issues
}

// pctDiff is the relative difference between actual and expected, used with
// contractTolerance so float rounding never trips a mismatch finding.
func pctDiff(actual, expected float64) float64 {
	if expected == 0 {
		return 0
	}
	return math.Abs(actual-expected) / math.Abs(expected)
}

// auditStagedOrderAllocation flags a PENDING staged order whose notional
// significantly exceeds what the strategy's AllocationPct dictates against
// the account's current equity. Under-allocation is deliberately not
// flagged: a stack overlay legitimately sizes to less than its AllocationPct
// when leftover cash after the primary is smaller -- that is correct
// behavior, not drift. Equity moves day to day, so this only runs against
// still-PENDING orders (closest to today's real balance) and uses a
// generous 10% tolerance over the dictated amount.
func auditStagedOrderAllocation(strats map[string]strategy.Strategy, strategyName string, quantity, entryPrice, currentEquity float64) *strategyContractIssue {
	if currentEquity <= 0 || quantity <= 0 || entryPrice <= 0 {
		return nil
	}
	strat, ok := strats[strategyName]
	if !ok {
		return nil // already reported by auditOrderAgainstStrategy
	}
	cfg := strat.DefaultConfig()
	if cfg.AllocationPct <= 0 {
		return nil
	}
	expectedNotional := currentEquity * cfg.AllocationPct
	actualNotional := quantity * entryPrice
	if actualNotional > expectedNotional*1.10 {
		return &strategyContractIssue{
			CheckName: "allocation_exceeds_strategy",
			Severity:  "WARNING",
			Detail: fmt.Sprintf("staged $%.2f notional exceeds %s's AllocationPct (%.0f%% of today's $%.2f equity = $%.2f)",
				actualNotional, strategyName, cfg.AllocationPct*100, currentEquity, expectedNotional),
		}
	}
	return nil
}

// auditStrategyContract checks every PENDING staged order and every open,
// actively-managed trade for acc against what its originating strategy's
// DefaultConfig() actually dictates, recording a finding for every mismatch
// found. A SUBMITTED staged order is not audited: it is a confirmed fill that
// never changes status again, so it would be re-audited against the current
// registry forever (a renamed strategy would raise unknown_strategy for good),
// and its terms are audited through its open trade while that trade is held. openTrades is passed in rather than re-queried since
// Run already loaded it for the position-protection checks.
func auditStrategyContract(report *Result, db *sql.DB, acc *trader.ResolvedAccount, strats map[string]strategy.Strategy, openTrades []manage_exits.Trade, now time.Time) {
	staged, err := stage_orders.GetStagedOrders(db, acc.AccountNumber)
	if err != nil {
		report.Recommendations = append(report.Recommendations, fmt.Sprintf("Failed to load staged orders for strategy-contract audit: %v", err))
	}
	for _, so := range staged {
		if so.Status != stage_orders.StagedOrderPending {
			continue
		}
		subject := fmt.Sprintf("%s (staged id=%d, %s)", so.Symbol, so.ID, so.Status)
		for _, issue := range auditOrderAgainstStrategy(strats, so.StrategyName, so.EntryPrice, so.TakeProfitPrice, so.StopLossPrice, so.MaxTradingDays) {
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: issue.CheckName,
				Category: "strategy_contract", Severity: issue.Severity,
				Subject: subject, Detail: issue.Detail,
			})
		}
		if issue := auditStagedOrderAllocation(strats, so.StrategyName, so.Quantity, so.EntryPrice, report.LiquidationValue); issue != nil {
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: issue.CheckName,
				Category: "strategy_contract", Severity: issue.Severity,
				Subject: subject, Detail: issue.Detail,
			})
		}
	}

	for _, t := range openTrades {
		subject := fmt.Sprintf("%s (trade id=%d, %s)", t.Symbol, t.ID, t.Status)
		for _, issue := range auditOrderAgainstStrategy(strats, t.StrategyName, t.EntryPrice, t.TargetProfitPrice, t.StopLossPrice, t.MaxTradingDays) {
			report.addFinding(db, Finding{
				RunAt: now, AccountNumber: acc.AccountNumber, CheckName: issue.CheckName,
				Category: "strategy_contract", Severity: issue.Severity,
				Subject: subject, Detail: issue.Detail,
			})
		}
	}
}
