package check_health

import (
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
)

func issueChecks(is []positionIssue) map[string]string {
	m := map[string]string{}
	for _, i := range is {
		m[i.check] = i.severity
	}
	return m
}

func teclTerms(t *testing.T) (map[string]strategy.Strategy, int, float64) {
	strats := teclStrats(t)
	cfg := strats[tecl].DefaultConfig()
	return strats, cfg.HoldingWindow, cfg.TakeProfitPct
}

func TestAuditPositionWithNoBooksIsCritical(t *testing.T) {
	strats, _, _ := teclTerms(t)
	a, is := AuditPosition("1", "MARA", 100, 13.06, nil, 0, 0, strats, time.Now())
	if a.Status != "CRITICAL" || issueChecks(is)["position_untracked"] != "CRITICAL" {
		t.Errorf("audit=%+v issues=%+v", a, is)
	}
}

func TestAuditPositionConformingPositionIsOK(t *testing.T) {
	strats, hold, _ := teclTerms(t)
	filled := time.Date(2026, 9, 24, 13, 30, 0, 0, time.UTC)
	tr := &manage_exits.Trade{StrategyName: tecl, EntryOrderID: "9", EntryPrice: 13.06, Quantity: 100, MaxTradingDays: hold,
		ExitDate: manage_exits.ExitDateFor(filled, hold), ExitDateSource: manage_exits.ExitDateFill, EntryFilledAt: &filled}
	now := filled // day of entry: nothing overdue
	a, is := AuditPosition("1", "MARA", 100, 13.06, tr, 0, 0, strats, now)
	for _, i := range is {
		switch i.check {
		case "position_no_take_profit", "position_no_stop_loss": // strategy states them, broker has none: expected here
		default:
			t.Errorf("unexpected issue %+v (audit %+v)", i, a)
		}
	}
	if a.ExpectedExit != tr.ExitDate {
		t.Errorf("expected exit %s, books %s", a.ExpectedExit, tr.ExitDate)
	}
}

func TestAuditPositionCatchesWrongDatesAndUnknownStrategy(t *testing.T) {
	strats, hold, _ := teclTerms(t)
	filled := time.Date(2026, 9, 24, 13, 30, 0, 0, time.UTC)
	wrong := &manage_exits.Trade{StrategyName: tecl, EntryOrderID: "9", EntryPrice: 13.06, Quantity: 100, MaxTradingDays: hold + 5,
		ExitDate: "2027-01-04", ExitDateSource: manage_exits.ExitDateFill, EntryFilledAt: &filled}
	_, is := AuditPosition("1", "MARA", 100, 13.06, wrong, 0, 0, strats, filled)
	c := issueChecks(is)
	if c["position_exit_date_mismatch"] != "CRITICAL" || c["position_hold_window_mismatch"] != "WARNING" {
		t.Errorf("issues = %+v", is)
	}

	nodate := &manage_exits.Trade{StrategyName: tecl, MaxTradingDays: hold}
	_, is = AuditPosition("1", "MARA", 100, 13.06, nodate, 0, 0, strats, filled)
	c = issueChecks(is)
	if c["position_no_exit_date"] != "CRITICAL" || c["position_no_entry_order"] != "WARNING" || c["position_exit_date_estimated"] != "WARNING" {
		t.Errorf("issues = %+v", is)
	}

	gone := &manage_exits.Trade{StrategyName: "retired_strategy", ExitDate: "2026-09-25", ExitDateSource: manage_exits.ExitDateFill, EntryOrderID: "1"}
	_, is = AuditPosition("1", "MARA", 100, 13.06, gone, 0, 0, strats, filled)
	if issueChecks(is)["position_strategy_unknown"] != "CRITICAL" {
		t.Errorf("issues = %+v", is)
	}

	late := &manage_exits.Trade{StrategyName: tecl, EntryOrderID: "9", MaxTradingDays: hold, ExitDate: "2026-09-25",
		ExitDateSource: manage_exits.ExitDateFill}
	_, is = AuditPosition("1", "MARA", 100, 13.06, late, 0, 0, strats, time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC))
	if issueChecks(is)["position_exit_overdue"] != "CRITICAL" {
		t.Errorf("issues = %+v", is)
	}
}

func TestAuditPositionComparesBrokerTakeProfitAndStopToTheStrategy(t *testing.T) {
	strats, hold, tpPct := teclTerms(t)
	if tpPct <= 0 {
		t.Skip("streak-voo-buy-tecl states no take-profit percent")
	}
	filled := time.Date(2026, 9, 24, 13, 30, 0, 0, time.UTC)
	tr := &manage_exits.Trade{StrategyName: tecl, EntryOrderID: "9", EntryPrice: 10, Quantity: 1, MaxTradingDays: hold,
		ExitDate: manage_exits.ExitDateFor(filled, hold), ExitDateSource: manage_exits.ExitDateFill, EntryFilledAt: &filled}
	right := 10 * (1 + tpPct)
	if _, is := AuditPosition("1", "MARA", 1, 10, tr, right, 0, strats, filled); issueChecks(is)["position_take_profit_mismatch"] != "" {
		t.Errorf("a conforming take-profit was flagged: %+v", is)
	}
	if _, is := AuditPosition("1", "MARA", 1, 10, tr, right*1.5, 0, strats, filled); issueChecks(is)["position_take_profit_mismatch"] != "WARNING" {
		t.Errorf("a wrong take-profit was not flagged: %+v", is)
	}
}
