package check_health

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	for _, ensure := range []func(*sql.DB) error{EnsureSchema, manage_exits.EnsureSchema, stage_orders.EnsureSchema} {
		if err := ensure(db); err != nil {
			t.Fatalf("failed to init schema: %v", err)
		}
	}
	return db
}

// tecl is the strategy the contract tests run against: the bundled streak-voo-buy-tecl row,
// resolved the way the jobs resolve it. Its real DefaultConfig(): AllocationPct 0.65,
// TakeProfitPct 0.05, HoldingWindow 8, no stop-loss.
const tecl = "streak-voo-buy-tecl"

func teclStrats(t *testing.T) map[string]strategy.Strategy {
	t.Helper()
	t.Setenv("APP_FOLDER", t.TempDir())
	got, err := strategies.ResolveGreenlit(nil, []string{tecl})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]strategy.Strategy{tecl: got[0]}
}

func TestHealthChecksSeededOnInit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	checks, err := ListChecks(db)
	if err != nil {
		t.Fatalf("ListChecks error: %v", err)
	}
	if len(checks) != len(defaultHealthChecks) {
		t.Fatalf("got %d seeded checks, want %d", len(checks), len(defaultHealthChecks))
	}
	for _, c := range checks {
		if !c.Enabled {
			t.Errorf("check %q expected enabled by default", c.Name)
		}
		if c.Query == "" {
			t.Errorf("check %q has an empty query", c.Name)
		}
	}
}

func TestSetHealthCheckEnabledPersistsAcrossReinit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	if err := SetEnabled(db, "duplicate_open_trade", false); err != nil {
		t.Fatalf("SetEnabled error: %v", err)
	}
	if err := SetEnabled(db, "no-such-check", true); err == nil {
		t.Fatal("expected an error toggling an unknown check name")
	}

	// Re-running EnsureSchema (as every bootstrap call does) must not clobber
	// the operator's disable choice, only refresh query/description.
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("re-init failed: %v", err)
	}

	checks, err := ListChecks(db)
	if err != nil {
		t.Fatalf("ListChecks error: %v", err)
	}
	for _, c := range checks {
		if c.Name == "duplicate_open_trade" && c.Enabled {
			t.Fatal("disabled check was re-enabled by EnsureSchema")
		}
	}
}

func TestRunSQLHealthChecks_StaleTimeExpiredTrade(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id, err := manage_exits.Open(db, manage_exits.OpenTradeParams{
		AccountNumber:     "74234603",
		Symbol:            "MARA",
		StrategyName:      tecl,
		EntryPrice:        10,
		TargetProfitPrice: 11,
		Quantity:          10,
		EntryTime:         time.Now().UTC().AddDate(0, 0, -20),
		MaxTradingDays:    8,
	})
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	// Open() always starts trading_days_held at 0 (a freshly opened trade
	// really has held 0 days) -- manage_exits advances it over time via
	// UpdateTradingDaysHeld, so simulate that aging here.
	if err := manage_exits.UpdateTradingDaysHeld(db, id, 9); err != nil {
		t.Fatalf("UpdateTradingDaysHeld error: %v", err)
	}

	runAt := time.Now().UTC()
	findings, err := RunSQLHealthChecks(db, "74234603", runAt)
	if err != nil {
		t.Fatalf("RunSQLHealthChecks error: %v", err)
	}

	found := false
	for _, f := range findings {
		if f.CheckName == "stale_time_expired_trade" {
			found = true
			if f.Severity != "CRITICAL" {
				t.Errorf("expected CRITICAL severity, got %s", f.Severity)
			}
			if f.Subject != "MARA" {
				t.Errorf("expected subject MARA, got %s", f.Subject)
			}
		}
	}
	if !found {
		t.Fatal("expected stale_time_expired_trade finding for a trade past its window")
	}

	persisted, err := GetRecentFindings(db, "74234603", 10)
	if err != nil {
		t.Fatalf("GetRecentFindings error: %v", err)
	}
	if len(persisted) != len(findings) {
		t.Fatalf("expected every finding persisted, got %d rows for %d findings", len(persisted), len(findings))
	}
}

func TestRunSQLHealthChecks_DuplicateOpenTrade(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	for range 2 {
		_, err := manage_exits.Open(db, manage_exits.OpenTradeParams{
			AccountNumber:     "74234603",
			Symbol:            "TECL",
			StrategyName:      "sig-voo-buy-tecl",
			EntryPrice:        50,
			TargetProfitPrice: 55,
			Quantity:          10,
			EntryTime:         time.Now().UTC(),
			MaxTradingDays:    8,
		})
		if err != nil {
			t.Fatalf("Open error: %v", err)
		}
	}

	findings, err := RunSQLHealthChecks(db, "74234603", time.Now().UTC())
	if err != nil {
		t.Fatalf("RunSQLHealthChecks error: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.CheckName == "duplicate_open_trade" && f.Subject == "TECL" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected duplicate_open_trade finding for two OPEN rows on the same symbol")
	}
}

func TestRunSQLHealthChecks_DisabledCheckIsSkipped(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := manage_exits.Open(db, manage_exits.OpenTradeParams{
		AccountNumber:     "74234603",
		Symbol:            "PDD",
		StrategyName:      "pdd_tree",
		EntryPrice:        50,
		TargetProfitPrice: 55,
		Quantity:          10,
		EntryTime:         time.Now().UTC().AddDate(0, 0, -20),
		MaxTradingDays:    8,
	})
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	if err := SetEnabled(db, "stale_time_expired_trade", false); err != nil {
		t.Fatalf("SetEnabled error: %v", err)
	}

	findings, err := RunSQLHealthChecks(db, "74234603", time.Now().UTC())
	if err != nil {
		t.Fatalf("RunSQLHealthChecks error: %v", err)
	}
	for _, f := range findings {
		if f.CheckName == "stale_time_expired_trade" {
			t.Fatal("disabled check must not run")
		}
	}
}

func TestRunHealthCheck_ActionRequiredOnCriticalFinding(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id, err := manage_exits.Open(db, manage_exits.OpenTradeParams{
		AccountNumber:     "74234603",
		Symbol:            "NVDA",
		StrategyName:      "Momentum5Pct",
		EntryPrice:        100,
		TargetProfitPrice: 105,
		Quantity:          10,
		EntryTime:         time.Now().UTC().AddDate(0, 0, -20),
		MaxTradingDays:    8,
	})
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	if err := manage_exits.UpdateTradingDaysHeld(db, id, 9); err != nil {
		t.Fatalf("UpdateTradingDaysHeld error: %v", err)
	}

	acc := &trader.ResolvedAccount{
		AccountNumber: "74234603",
		Details: trader.SecuritiesAccount{
			AccountNumber: "74234603",
			CurrentBalances: trader.Balances{
				CashBalance:      2300.0,
				LiquidationValue: 2300.0,
			},
			Positions: []trader.Position{
				{
					LongQuantity: 10,
					AveragePrice: 100.0,
					MarketValue:  1000.0,
					Instrument:   trader.PositionInstrument{Symbol: "NVDA"},
				},
			},
		},
	}

	report, err := Run(context.Background(), db, nil, acc, nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if report.OverallStatus != "ACTION_REQUIRED" {
		t.Errorf("expected ACTION_REQUIRED with a stale expired trade, got %s", report.OverallStatus)
	}
	if report.CriticalFindings == 0 {
		t.Error("expected at least one critical finding")
	}
}

func TestHealthCheckAudit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	acc := &trader.ResolvedAccount{
		AccountNumber: "74234603",
		Details: trader.SecuritiesAccount{
			AccountNumber: "74234603",
			CurrentBalances: trader.Balances{
				CashBalance:      2300.0,
				LiquidationValue: 2300.0,
			},
			Positions: []trader.Position{
				{
					LongQuantity: 10,
					AveragePrice: 100.0,
					MarketValue:  1000.0,
					Instrument:   trader.PositionInstrument{Symbol: "NVDA"},
				},
			},
		},
	}

	report, err := Run(context.Background(), db, nil, acc, nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if report.TotalPositions != 1 {
		t.Errorf("expected 1 total position, got %d", report.TotalPositions)
	}
	if report.UnprotectedPositions != 1 {
		t.Errorf("expected 1 unprotected position, got %d", report.UnprotectedPositions)
	}
	// A held position the books know nothing about has no strategy and no exit date: CRITICAL.
	if report.OverallStatus != "ACTION_REQUIRED" {
		t.Errorf("expected ACTION_REQUIRED for an untracked position, got %s", report.OverallStatus)
	}
	if len(report.PositionAudits) != 1 || report.PositionAudits[0].Status != "CRITICAL" {
		t.Errorf("position audit = %+v", report.PositionAudits)
	}
	got, err := LatestPositionAudits(db)
	if err != nil || len(got) != 1 || got[0].Symbol != "NVDA" {
		t.Errorf("stored position audits = %+v err=%v", got, err)
	}
}

func TestAuditOrderAgainstStrategy_UnknownStrategy(t *testing.T) {
	issues := auditOrderAgainstStrategy(map[string]strategy.Strategy{}, "no-such-strategy-xyz", 100, 105, 95, 8)
	if len(issues) != 1 || issues[0].CheckName != "unknown_strategy" || issues[0].Severity != "CRITICAL" {
		t.Fatalf("expected a single CRITICAL unknown_strategy issue, got %+v", issues)
	}
}

func TestAuditOrderAgainstStrategy_MatchingTermsNoIssues(t *testing.T) {
	strats := teclStrats(t)
	entry := 10.0
	tp := entry * 1.05                                                 // matches TakeProfitPct 0.05
	issues := auditOrderAgainstStrategy(strats, tecl, entry, tp, 0, 8) // no stop dictated; matches HoldingWindow 8
	if len(issues) != 0 {
		t.Fatalf("expected no issues for terms matching streak-voo-buy-tecl's DefaultConfig, got %+v", issues)
	}
}

func TestAuditOrderAgainstStrategy_HoldWindowMismatch(t *testing.T) {
	strats := teclStrats(t)
	entry := 10.0
	issues := auditOrderAgainstStrategy(strats, tecl, entry, entry*1.05, 0, 1)
	if len(issues) != 1 || issues[0].CheckName != "hold_window_mismatch" || issues[0].Severity != "WARNING" {
		t.Fatalf("expected a single hold_window_mismatch WARNING, got %+v", issues)
	}
}

func TestAuditOrderAgainstStrategy_TakeProfitMismatch(t *testing.T) {
	strats := teclStrats(t)
	entry := 10.0
	// Recorded TP implies a wildly different percentage than streak-voo-buy-tecl's 5%.
	issues := auditOrderAgainstStrategy(strats, tecl, entry, entry*1.50, 0, 8)
	if len(issues) != 1 || issues[0].CheckName != "take_profit_mismatch" {
		t.Fatalf("expected a single take_profit_mismatch issue, got %+v", issues)
	}
}

func TestAuditOrderAgainstStrategy_ToleratesFloatRounding(t *testing.T) {
	strats := teclStrats(t)
	entry := 10.0
	// Within contractTolerance (0.5%) of the dictated values.
	issues := auditOrderAgainstStrategy(strats, tecl, entry, entry*1.0502, 0, 8)
	if len(issues) != 0 {
		t.Fatalf("expected sub-tolerance rounding to produce no issues, got %+v", issues)
	}
}

func TestAuditStagedOrderAllocation(t *testing.T) {
	strats := teclStrats(t)
	// streak-voo-buy-tecl trades the bundle's flat allocation (20%); equity $100k -> dictated notional $20,000.
	if issue := auditStagedOrderAllocation(strats, tecl, 2000, 10, 100000); issue != nil {
		t.Errorf("expected exact-match allocation to pass, got %+v", issue)
	}
	// Under-allocation (a leftover-cash-capped overlay) must never be flagged.
	if issue := auditStagedOrderAllocation(strats, tecl, 100, 10, 100000); issue != nil {
		t.Errorf("expected under-allocation to be ignored, got %+v", issue)
	}
	// Well over the dictated + 10% tolerance must be flagged.
	issue := auditStagedOrderAllocation(strats, tecl, 20000, 10, 100000)
	if issue == nil || issue.CheckName != "allocation_exceeds_strategy" {
		t.Fatalf("expected an allocation_exceeds_strategy issue for gross over-allocation, got %+v", issue)
	}
}

func TestAuditStrategyContract_Integration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	strats := teclStrats(t)

	// PENDING staged order with a hold window that contradicts streak-voo-buy-tecl's
	// DefaultConfig (8 days) -- should surface as a finding.
	_, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    tecl,
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        100,
		EntryPrice:      10,
		TakeProfitPrice: 10.5,
		StopLossPrice:   9.2,
		MaxTradingDays:  1, // streak-voo-buy-tecl dictates 8
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertStagedOrder error: %v", err)
	}

	// An OPEN trade whose terms correctly match streak-voo-buy-tecl's DefaultConfig --
	// must NOT produce a finding.
	_, err = manage_exits.Open(db, manage_exits.OpenTradeParams{
		AccountNumber:     "74234603",
		Symbol:            "PDD",
		StrategyName:      tecl,
		EntryPrice:        20,
		TargetProfitPrice: 21, // 20 * 1.05
		Quantity:          10,
		EntryTime:         time.Now().UTC(),
		MaxTradingDays:    8,
	})
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}

	report := &Result{AccountNumber: "74234603", LiquidationValue: 100000}
	openTrades, err := manage_exits.GetOpenTrades(db, "74234603")
	if err != nil {
		t.Fatalf("GetOpenTrades error: %v", err)
	}

	auditStrategyContract(report, db, &trader.ResolvedAccount{AccountNumber: "74234603"}, strats, openTrades, time.Now().UTC())

	if len(report.Findings) != 1 {
		t.Fatalf("expected exactly 1 finding (the staged order's hold-window mismatch), got %d: %+v", len(report.Findings), report.Findings)
	}
	f := report.Findings[0]
	if f.CheckName != "hold_window_mismatch" || f.Category != "strategy_contract" {
		t.Errorf("unexpected finding: %+v", f)
	}

	persisted, err := GetRecentFindings(db, "74234603", 10)
	if err != nil {
		t.Fatalf("GetRecentFindings error: %v", err)
	}
	if len(persisted) != 1 {
		t.Fatalf("expected the finding to be persisted, got %d rows", len(persisted))
	}
}

func TestAuditStrategyContract_SkipsRejectedStagedOrders(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	strats := teclStrats(t)

	id, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    tecl,
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        100,
		EntryPrice:      10,
		TakeProfitPrice: 999, // wildly wrong, but the order was rejected/dead
		StopLossPrice:   1,
		MaxTradingDays:  99,
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertStagedOrder error: %v", err)
	}
	if err := stage_orders.MarkStagedOrderRejected(db, id, "test rejection"); err != nil {
		t.Fatalf("MarkStagedOrderRejected error: %v", err)
	}

	report := &Result{AccountNumber: "74234603", LiquidationValue: 100000}
	auditStrategyContract(report, db, &trader.ResolvedAccount{AccountNumber: "74234603"}, strats, nil, time.Now().UTC())

	if len(report.Findings) != 0 {
		t.Fatalf("expected a REJECTED staged order to be excluded from the contract audit, got %+v", report.Findings)
	}
}

// A SUBMITTED staged order is a confirmed fill. It must not be re-audited against the
// current registry: a strategy renamed since (sig-voo-buy-tecl) would raise
// unknown_strategy on it for good.
func TestAuditStrategyContract_SkipsSubmittedStagedOrders(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	strats := teclStrats(t)

	id, err := stage_orders.InsertStagedOrder(db, &stage_orders.StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "renamed-away-strategy",
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        1,
		EntryPrice:      10,
		TakeProfitPrice: 10.5,
		StopLossPrice:   9,
		MaxTradingDays:  1,
		Status:          stage_orders.StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertStagedOrder error: %v", err)
	}
	if err := stage_orders.MarkStagedOrderSubmitted(db, id, "9001", time.Now().UTC()); err != nil {
		t.Fatalf("MarkStagedOrderSubmitted error: %v", err)
	}

	report := &Result{AccountNumber: "74234603", LiquidationValue: 100000}
	auditStrategyContract(report, db, &trader.ResolvedAccount{AccountNumber: "74234603"}, strats, nil, time.Now().UTC())

	if len(report.Findings) != 0 {
		t.Fatalf("expected a SUBMITTED staged order to be excluded from the contract audit, got %+v", report.Findings)
	}
}
