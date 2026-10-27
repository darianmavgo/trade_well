package stage_orders

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

func setRiskState(t *testing.T, db *sql.DB, acct string, scale float64, halt int, maxGross float64) {
	t.Helper()
	if err := risk.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO risk_state (account_number, assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		acct, time.Now().UTC(), "2026-10-15", 1000.0, 1200.0, 0.1667, -0.01, 0.2, scale, halt, maxGross, "test: drawdown"); err != nil {
		t.Fatal(err)
	}
}

func riskTestAccount() *trader.ResolvedAccount {
	acc := &trader.ResolvedAccount{AccountNumber: "111"}
	acc.Details.CurrentBalances.LiquidationValue = 10000
	acc.Details.CurrentBalances.CashAvailableForTrading = 10000
	return acc
}

func riskTestSignals() []scan_market.Signal {
	return []scan_market.Signal{{StrategyID: "s1", Priority: 1, AllocPct: 0.5, Symbol: "AAA", Price: 10, NextSession: "2026-10-16"}}
}

func stageWithRisk(t *testing.T, scale float64, halt int, maxGross float64) (*Result, *sql.DB) {
	t.Helper()
	db := setupTestDB(t)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	setRiskState(t, db, "111", scale, halt, maxGross)
	res, err := Run(context.Background(), db, riskTestAccount(), "s1", riskTestSignals(), nil, 0, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	return res, db
}

func firstRow(t *testing.T, res *Result) Row {
	t.Helper()
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %+v", res.Rows)
	}
	return res.Rows[0]
}

func TestRunWithoutRiskStateSizesNormally(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	res, err := Run(context.Background(), db, riskTestAccount(), "s1", riskTestSignals(), nil, 0, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r := firstRow(t, res); r.Status != "SIMULATED" || !strings.Contains(r.Detail, "500 shares") {
		t.Fatalf("row = %+v", r)
	}
}

func TestRunRiskHaltSkipsEveryEntry(t *testing.T) {
	res, _ := stageWithRisk(t, 0, 1, 1)
	if r := firstRow(t, res); r.Status != "SKIPPED" || !strings.Contains(r.Detail, "risk halt") {
		t.Fatalf("row = %+v", r)
	}
}

func TestRunRiskScaleShrinksEntries(t *testing.T) {
	res, _ := stageWithRisk(t, 0.5, 0, 1)
	if r := firstRow(t, res); r.Status != "SIMULATED" || !strings.Contains(r.Detail, "250 shares") {
		t.Fatalf("half size should be 250 shares of the 500: %+v", r)
	}
}

func TestRunRiskExposureCapLimitsCash(t *testing.T) {
	// The cap is 3% of the account's $10000 equity: $300 of room, so 28 shares at $10 (95% of room).
	res, _ := stageWithRisk(t, 1, 0, 0.03)
	if r := firstRow(t, res); r.Status != "SIMULATED" || !strings.Contains(r.Detail, "28 shares") {
		t.Fatalf("row = %+v", r)
	}
}

// A signal with no valid allocation is skipped with a reason, never sized at a guessed share.
func TestNoValidAllocationIsSkippedNotGuessed(t *testing.T) {
	for _, alloc := range []float64{0, -0.1, 1.5} {
		sized := SizeStackEntries(10000, 10000, nil, []scan_market.Signal{{StrategyID: "s", Symbol: "AAA", Price: 10, AllocPct: alloc}})
		if len(sized) != 1 || sized[0].Quantity != 0 || !strings.Contains(sized[0].Reason, "no valid allocation") {
			t.Errorf("alloc %v: %+v", alloc, sized)
		}
		watched := WatchEntries(10000, nil, []scan_market.Signal{{StrategyID: "s", Symbol: "AAA", Price: 10, AllocPct: alloc}})
		if len(watched) != 1 || watched[0].Quantity != 0 || !strings.Contains(watched[0].Reason, "no valid allocation") {
			t.Errorf("watch alloc %v: %+v", alloc, watched)
		}
	}
	// A flat 20% of $10,000 at $10 is 200 shares.
	sized := SizeStackEntries(10000, 10000, nil, []scan_market.Signal{{StrategyID: "s", Symbol: "AAA", Price: 10, AllocPct: 0.20}})
	if sized[0].Quantity != 200 {
		t.Errorf("20%% allocation sized %v shares, want 200", sized[0].Quantity)
	}
}
