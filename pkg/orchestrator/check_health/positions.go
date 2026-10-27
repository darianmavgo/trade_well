package check_health

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
)

// PositionAudit is the priority section of check_health: one held position judged
// against the books and the strategy that owns it. It answers, per position: is the
// accounting in place (a manage_exit row, its entry order, the real fill), is the
// strategy known, and do the take-profit, stop-loss, hold window and exit date at the
// broker and in the books match what that strategy dictates.
type PositionAudit struct {
	AccountNumber string
	Symbol        string
	Quantity      float64
	AveragePrice  float64
	Strategy      string // "" when no manage_exit row names one
	EntryOrderID  string
	EntryFilledAt string // New York date of the entry fill, "" if unconfirmed
	ExitDate      string // what the books hold
	ExitDateSrc   string
	ExpectedExit  string  // fill date + the strategy's hold window; "" when it cannot be derived
	TakeProfit    float64 // actual, from Schwab's working order (0 = none)
	ExpectedTP    float64 // what the strategy dictates for this entry (0 = strategy states none)
	StopLoss      float64
	ExpectedStop  float64
	Status        string // OK, WARNING or CRITICAL
	Issues        []string
	RunAt         time.Time
}

// positionIssue is one thing wrong with a position: a check name, its severity, its text.
type positionIssue struct{ check, severity, detail string }

// AuditPosition judges one held position. trade is its OPEN manage_exit row (nil when the
// books have none); takeProfit and stopLoss are the prices of Schwab's working sell
// orders on the symbol (0 when there is none). It is pure: no broker call, no write.
// Findings for a missing or non-conforming take-profit/stop that Run already records
// (missing_profit_taker, missing_stop_loss) are not repeated here, but show in Issues.
func AuditPosition(accountNumber, symbol string, qty, avgPrice float64, trade *manage_exits.Trade,
	takeProfit, stopLoss float64, strats map[string]strategy.Strategy, now time.Time) (PositionAudit, []positionIssue) {

	a := PositionAudit{AccountNumber: accountNumber, Symbol: symbol, Quantity: qty, AveragePrice: avgPrice,
		TakeProfit: takeProfit, StopLoss: stopLoss, RunAt: now, Status: "OK"}
	var issues []positionIssue
	add := func(check, sev, detail string) {
		issues = append(issues, positionIssue{check, sev, detail})
	}

	if trade == nil {
		add("position_untracked", "CRITICAL", "held but the books have no OPEN manage_exit row: no strategy, no exit date, and manage_exits will not sell it (run manage_exits to adopt it)")
	} else {
		a.Strategy, a.EntryOrderID, a.ExitDate, a.ExitDateSrc = trade.StrategyName, trade.EntryOrderID, trade.ExitDate, trade.ExitDateSource
		if trade.EntryFilledAt != nil {
			a.EntryFilledAt = manage_exits.TodayET(*trade.EntryFilledAt)
		}
		if trade.EntryOrderID == "" {
			add("position_no_entry_order", "WARNING", "the books do not record which order bought it, so the fill date cannot be confirmed from the broker")
		}
		if trade.ExitDateSource != manage_exits.ExitDateFill {
			add("position_exit_date_estimated", "WARNING", fmt.Sprintf("exit date %s is still an estimate: the entry fill has not been confirmed", orDash(trade.ExitDate)))
		}
		if trade.ExitDate == "" {
			add("position_no_exit_date", "CRITICAL", "no exit date: this position would be held forever")
		} else if manage_exits.TodayET(now) > trade.ExitDate {
			add("position_exit_overdue", "CRITICAL", fmt.Sprintf("exit date %s has passed and it is still held", trade.ExitDate))
		}
		if trade.Quantity > 0 && abs(trade.Quantity-qty) > 0.0001 {
			add("position_quantity_mismatch", "WARNING", fmt.Sprintf("books track %.4f shares, broker holds %.4f", trade.Quantity, qty))
		}
	}

	if a.Strategy == "" {
		if trade != nil {
			add("position_strategy_unknown", "CRITICAL", "the books name no strategy for this position")
		}
	} else if strat, ok := strats[a.Strategy]; !ok {
		add("position_strategy_unknown", "CRITICAL", fmt.Sprintf("strategy %q is not in the allowlist/registry, so its terms cannot be verified", a.Strategy))
	} else {
		cfg := strat.DefaultConfig()
		if cfg.HoldingWindow > 0 {
			if trade.MaxTradingDays != cfg.HoldingWindow {
				add("position_hold_window_mismatch", "WARNING", fmt.Sprintf("hold window %d trading days but %s dictates %d", trade.MaxTradingDays, a.Strategy, cfg.HoldingWindow))
			}
			if trade.EntryFilledAt != nil {
				a.ExpectedExit = manage_exits.ExitDateFor(*trade.EntryFilledAt, cfg.HoldingWindow)
				if trade.ExitDate != "" && trade.ExitDate != a.ExpectedExit {
					add("position_exit_date_mismatch", "CRITICAL", fmt.Sprintf("exit date %s but the fill on %s plus %s's %d-day window is %s", trade.ExitDate, a.EntryFilledAt, a.Strategy, cfg.HoldingWindow, a.ExpectedExit))
				}
			}
		}
		base := avgPrice
		if trade.EntryPrice > 0 {
			base = trade.EntryPrice
		}
		switch {
		case cfg.TakeProfitPct > 0:
			a.ExpectedTP = base * (1 + cfg.TakeProfitPct)
		case cfg.TargetPct > 1:
			a.ExpectedTP = base * cfg.TargetPct
		}
		if cfg.StopLossPct > 0 && cfg.StopLossPct < 1 {
			a.ExpectedStop = base * cfg.StopLossPct
		}
		if takeProfit > 0 && a.ExpectedTP > 0 && pctDiff(takeProfit, a.ExpectedTP) > contractTolerance {
			add("position_take_profit_mismatch", "WARNING", fmt.Sprintf("broker take-profit $%.2f but %s dictates $%.2f on a $%.2f entry", takeProfit, a.Strategy, a.ExpectedTP, base))
		}
		if stopLoss > 0 && a.ExpectedStop > 0 && pctDiff(stopLoss, a.ExpectedStop) > contractTolerance {
			add("position_stop_loss_mismatch", "WARNING", fmt.Sprintf("broker stop-loss $%.2f but %s dictates $%.2f on a $%.2f entry", stopLoss, a.Strategy, a.ExpectedStop, base))
		}
		if takeProfit <= 0 && a.ExpectedTP > 0 {
			add("position_no_take_profit", "WARNING", fmt.Sprintf("no take-profit at the broker; %s dictates $%.2f", a.Strategy, a.ExpectedTP))
		}
		if stopLoss <= 0 && a.ExpectedStop > 0 {
			add("position_no_stop_loss", "WARNING", fmt.Sprintf("no stop-loss at the broker; %s dictates $%.2f", a.Strategy, a.ExpectedStop))
		}
	}

	for _, i := range issues {
		a.Issues = append(a.Issues, i.detail)
		if i.severity == "CRITICAL" {
			a.Status = "CRITICAL"
		} else if a.Status == "OK" {
			a.Status = "WARNING"
		}
	}
	return a, issues
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

var positionSchema = orchestratorsql.Q("check_health/position_schema")

// RecordPositionAudit stores a run's per-position audits so /check_health can show them.
func RecordPositionAudit(db *sql.DB, a PositionAudit) error {
	if _, err := db.Exec(positionSchema); err != nil {
		return err
	}
	_, err := db.Exec(orchestratorsql.Q("check_health/record_position_audit"),
		a.RunAt.UTC().Format(time.RFC3339), a.AccountNumber, a.Symbol, a.Quantity, a.AveragePrice, a.Strategy,
		a.EntryOrderID, a.EntryFilledAt, a.ExitDate, a.ExitDateSrc, a.ExpectedExit, a.TakeProfit, a.ExpectedTP,
		a.StopLoss, a.ExpectedStop, a.Status, strings.Join(a.Issues, "; "))
	return err
}

// LatestPositionAudits is each account's most recent position audit, worst first.
func LatestPositionAudits(db *sql.DB) ([]PositionAudit, error) {
	if _, err := db.Exec(positionSchema); err != nil {
		return nil, err
	}
	rows, err := db.Query(orchestratorsql.Q("check_health/latest_position_audits"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PositionAudit
	for rows.Next() {
		var a PositionAudit
		var runAt, issues string
		if err := rows.Scan(&runAt, &a.AccountNumber, &a.Symbol, &a.Quantity, &a.AveragePrice, &a.Strategy, &a.EntryOrderID,
			&a.EntryFilledAt, &a.ExitDate, &a.ExitDateSrc, &a.ExpectedExit, &a.TakeProfit, &a.ExpectedTP,
			&a.StopLoss, &a.ExpectedStop, &a.Status, &issues); err != nil {
			return nil, err
		}
		a.RunAt, _ = time.Parse(time.RFC3339, runAt)
		if issues != "" {
			a.Issues = strings.Split(issues, "; ")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Summary is the one-line account of a position for reports.
func (a PositionAudit) Summary() string {
	usd := func(v float64) string {
		if v <= 0 {
			return "none"
		}
		return fmt.Sprintf("$%.2f", v)
	}
	line := fmt.Sprintf("%.0f sh; strategy %s; entry order %s, filled %s; exit date %s (%s; strategy dictates %s); take-profit %s (dictated %s); stop %s (dictated %s)",
		a.Quantity, orDash(a.Strategy), orDash(a.EntryOrderID), orDash(a.EntryFilledAt), orDash(a.ExitDate), orDash(a.ExitDateSrc),
		orDash(a.ExpectedExit), usd(a.TakeProfit), usd(a.ExpectedTP), usd(a.StopLoss), usd(a.ExpectedStop))
	if len(a.Issues) > 0 {
		line += " -- " + strings.Join(a.Issues, "; ")
	}
	return line
}
