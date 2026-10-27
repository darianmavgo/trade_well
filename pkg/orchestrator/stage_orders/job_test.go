package stage_orders

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

type jobEnv struct {
	db      *sql.DB
	acc     *trader.ResolvedAccount
	sigID   int64
	members map[string]strategy.Strategy
}

func newJobEnv(t *testing.T, nextSession string) *jobEnv {
	t.Helper()
	db := setupTestDB(t)
	t.Cleanup(func() { db.Close() })
	if err := scan_market.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := job_runs.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_FOLDER", t.TempDir())
	resolved, err := strategies.ResolveGreenlit(nil, []string{"streak-voo-buy-tecl"})
	if err != nil {
		t.Fatal(err)
	}
	teclStrategy := resolved[0]
	acc := &trader.ResolvedAccount{AccountNumber: "111"}
	acc.Details.CurrentBalances.LiquidationValue = 100000
	acc.Details.CurrentBalances.CashAvailableForTrading = 100000
	id, err := scan_market.InsertBuySignal(db, &scan_market.BuySignal{
		ScanRunID: "20260923T220000Z", StackID: "streak-voo-buy-tecl", StrategyID: "streak-voo-buy-tecl", Symbol: "TECL", Direction: "LONG",
		AsOfDate: "2026-09-23", NextSession: nextSession, Price: 20, Close: 20, BuyLimit: 20,
		TakeProfit: 21, StopLoss: 18.4, AllocationPct: 0.65, HoldDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &jobEnv{db: db, acc: acc, sigID: id, members: map[string]strategy.Strategy{"streak-voo-buy-tecl": teclStrategy}}
}

func (e *jobEnv) signalStatus(t *testing.T) (status string, staged sql.NullInt64) {
	t.Helper()
	if err := e.db.QueryRow(`SELECT status, staged_order_id FROM scan_market_buy_signal WHERE id = ?`, e.sigID).Scan(&status, &staged); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRunJobStagesNewSignalOnceAndLinksIt(t *testing.T) {
	e := newJobEnv(t, "2026-09-24")
	opts := JobOptions{Members: e.members, Now: time.Date(2026, 9, 23, 23, 0, 0, 0, time.UTC)}

	res, err := RunJob(context.Background(), e.db, []*trader.ResolvedAccount{e.acc}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Signals != 1 || res.Staged != 1 {
		t.Fatalf("signals=%d staged=%d, want 1/1 (%v)", res.Signals, res.Staged, res.Details)
	}
	status, staged := e.signalStatus(t)
	if status != scan_market.BuySignalStaged || !staged.Valid {
		t.Fatalf("buy signal status=%q staged=%v", status, staged)
	}
	var qty float64
	if err := e.db.QueryRow(`SELECT quantity FROM stage_order WHERE id = ?`, staged.Int64).Scan(&qty); err != nil || qty != 3250 {
		t.Fatalf("stage_order qty=%v err=%v, want 65%% of $100k / $20 = 3250", qty, err)
	}

	// Rerun: the signal is no longer NEW, so nothing new is staged.
	res, err = RunJob(context.Background(), e.db, []*trader.ResolvedAccount{e.acc}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Signals != 0 || res.Staged != 0 {
		t.Fatalf("rerun signals=%d staged=%d, want 0/0", res.Signals, res.Staged)
	}
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM stage_order`).Scan(&n)
	if n != 1 {
		t.Fatalf("stage_order rows = %d, want 1", n)
	}
}

func TestRunJobDryRunWritesNothing(t *testing.T) {
	e := newJobEnv(t, "2026-09-24")
	res, err := RunJob(context.Background(), e.db, []*trader.ResolvedAccount{e.acc},
		JobOptions{DryRun: true, Members: e.members, Now: time.Date(2026, 9, 23, 23, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Signals != 1 {
		t.Fatalf("signals=%d", res.Signals)
	}
	if status, _ := e.signalStatus(t); status != scan_market.BuySignalNew {
		t.Fatalf("dry run changed signal status to %q", status)
	}
	var n int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM stage_order`).Scan(&n)
	if n != 0 {
		t.Fatalf("dry run wrote %d stage_order rows", n)
	}
}

func TestRunJobDismissesExpiredSignal(t *testing.T) {
	e := newJobEnv(t, "2026-09-24")
	// It is now Sep 28: the Sep 24 session is long gone.
	res, err := RunJob(context.Background(), e.db, []*trader.ResolvedAccount{e.acc},
		JobOptions{Members: e.members, Now: time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Staged != 0 || res.Dismissed != 1 {
		t.Fatalf("staged=%d dismissed=%d", res.Staged, res.Dismissed)
	}
	if status, _ := e.signalStatus(t); status != scan_market.BuySignalDismissed {
		t.Fatalf("status=%q, want DISMISSED", status)
	}
}

func TestRunJobNeedsAccounts(t *testing.T) {
	e := newJobEnv(t, "2026-09-24")
	if _, err := RunJob(context.Background(), e.db, nil, JobOptions{Members: e.members}); err == nil {
		t.Fatal("expected an error with no accounts")
	}
}

// A strategy that sizes by shares (the one-share TSLL smoke test) must stay at
// that size. Before this was honored it was sized to 100% of equity: 2599
// shares of TSLL in a $28k account.
func TestSizingHonorsFixedSharesAndFixedDollar(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "tsll", Symbol: "TSLL", Price: 10.30, AllocPct: 1.0, Sizing: "fixed_shares", FixedShares: 1},
		{StrategyID: "dollar", Symbol: "AAA", Price: 50, AllocPct: 1.0, Sizing: "fixed_dollar", FixedDollar: 500},
		{StrategyID: "pct", Symbol: "BBB", Price: 20, AllocPct: 0.5, Priority: 2},
	}
	got := SizeStackEntries(100000, 100000, nil, cands)
	qty := map[string]float64{}
	for _, e := range got {
		qty[e.StrategyID] = e.Quantity
	}
	if qty["tsll"] != 1 {
		t.Errorf("fixed_shares strategy sized to %v shares, want 1", qty["tsll"])
	}
	if qty["dollar"] != 10 {
		t.Errorf("fixed_dollar $500 at $50 = %v shares, want 10", qty["dollar"])
	}
	if qty["pct"] < 1000 {
		t.Errorf("percent-of-equity strategy should still size by allocation, got %v", qty["pct"])
	}
}

func TestRunJobSizesTSLLSmokeTestToOneShare(t *testing.T) {
	e := newJobEnv(t, "2026-09-24")
	tsll, ok := strategy.Get("tsll-daily-one-share")
	if !ok {
		t.Fatal("tsll-daily-one-share not registered")
	}
	if _, err := e.db.Exec(`DELETE FROM scan_market_buy_signal`); err != nil {
		t.Fatal(err)
	}
	if _, err := scan_market.InsertBuySignal(e.db, &scan_market.BuySignal{
		ScanRunID: "20260923T220000Z", StackID: "tsll-daily-one-share", StrategyID: "tsll-daily-one-share", Symbol: "TSLL",
		Direction: "LONG", AsOfDate: "2026-09-23", NextSession: "2026-09-24", Price: 10.30, Close: 10.30, BuyLimit: 10.30,
		TakeProfit: 10.815, StopLoss: 8.24, AllocationPct: 1.0, HoldDays: 1,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := RunJob(context.Background(), e.db, []*trader.ResolvedAccount{e.acc},
		JobOptions{Members: map[string]strategy.Strategy{"tsll-daily-one-share": tsll}, Now: time.Date(2026, 9, 23, 23, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Staged != 1 {
		t.Fatalf("staged=%d (%v)", res.Staged, res.Details)
	}
	var qty float64
	if err := e.db.QueryRow(`SELECT quantity FROM stage_order WHERE symbol='TSLL'`).Scan(&qty); err != nil || qty != 1 {
		t.Fatalf("TSLL stage_order quantity = %v (err %v), want exactly 1", qty, err)
	}
}
