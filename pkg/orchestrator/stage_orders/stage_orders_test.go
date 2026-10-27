package stage_orders

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/schwaber/orderintent"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}
	return db
}

// TestAddPendingStagedSymbols confirms a symbol with an existing PENDING
// stage_order row is claimed just like an open position -- so a second scan
// run before submit-orders processes the queue (a retried job, a re-run
// during troubleshooting) skips it instead of staging a duplicate.
func TestAddPendingStagedSymbols(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := InsertStagedOrder(db, &StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "mara_tree",
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        100,
		EntryPrice:      10,
		TakeProfitPrice: 11,
		StopLossPrice:   9,
		MaxTradingDays:  1,
		Status:          StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertStagedOrder error: %v", err)
	}

	held := map[string]bool{}
	addPendingStagedSymbols(db, "74234603", held)
	if !held["MARA"] {
		t.Fatal("expected MARA to be claimed by its PENDING staged order")
	}

	// Scoped per account -- a different account's pending order must not leak in.
	held2 := map[string]bool{}
	addPendingStagedSymbols(db, "99999999", held2)
	if held2["MARA"] {
		t.Fatal("pending staged orders must be scoped per account")
	}
}

// TestSizeStackEntries_StackOfOne confirms a plain, non-"+" strategy ID sizes
// exactly like a multi-member stack's primary: AllocationPct of equity,
// capped by 95% of cash. There is no separate "single strategy" sizing path.
func TestSizeStackEntries_StackOfOne(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "sig-voo-buy-tecl", Priority: 0, AllocPct: 0.65, Symbol: "TECL", Price: 100},
	}
	got := SizeStackEntries(100000, 100000, nil, cands)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Quantity != 650 {
		t.Errorf("stack-of-1 qty=%v, want 650 (65%% of 100k @ $100), same math as a multi-member stack's primary", got[0].Quantity)
	}
	if got[0].Notional != 65000 {
		t.Errorf("stack-of-1 notional=%v, want 65000", got[0].Notional)
	}
}

// TestSizeStackEntries_StackOfOneSkipsWithNoCapital confirms that with no
// live account balance (accountCapital's no-fallback zero), a stack of 1 is
// skipped with a clear reason rather than sized against a guess.
func TestSizeStackEntries_StackOfOneSkipsWithNoCapital(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "sig-voo-buy-tecl", Priority: 0, AllocPct: 0.65, Symbol: "TECL", Price: 100},
	}
	got := SizeStackEntries(0, 0, nil, cands)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].Quantity != 0 || got[0].Reason == "" {
		t.Errorf("expected a skip reason with no capital, got %+v", got[0])
	}
}

func TestSizeStackEntries_PrimaryThenLeftover(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "sig-voo-buy-tecl", Priority: 0, AllocPct: 0.65, Symbol: "TECL", Price: 100},
		{StrategyID: "mara_tree", Priority: 1, AllocPct: 0.65, Symbol: "MARA", Price: 10},
		{StrategyID: "pdd_tree", Priority: 2, AllocPct: 0.65, Symbol: "PDD", Price: 50},
	}
	got := SizeStackEntries(100000, 100000, nil, cands)
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Quantity != 650 {
		t.Errorf("primary qty=%v, want 650 (65%% of 100k @ $100)", got[0].Quantity)
	}
	// leftover cash after primary = 35000; 95% = 33250; MARA $10 → 3325 shares
	if got[1].Quantity != 3325 {
		t.Errorf("mara qty=%v, want 3325 leftover", got[1].Quantity)
	}
	// leftover after mara ≈ $1,750; 95% / $50 → 33 PDD shares
	if got[2].Quantity != 33 {
		t.Errorf("pdd leftover qty=%v, want 33", got[2].Quantity)
	}
}

func TestSizeStackEntries_SkipsHeldAndDoesNotPreempt(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "sig-voo-buy-tecl", Priority: 0, AllocPct: 0.65, Symbol: "TECL", Price: 100},
		{StrategyID: "mara_tree", Priority: 1, AllocPct: 0.65, Symbol: "MARA", Price: 10},
	}
	held := map[string]bool{"MARA": true}
	got := SizeStackEntries(100000, 35000, held, cands)
	if got[0].Quantity != 332 {
		t.Errorf("primary sized to leftover cash, qty=%v want 332 (95%% of 35k / 100)", got[0].Quantity)
	}
	if got[1].Quantity != 0 || got[1].Reason == "" {
		t.Errorf("open MARA must not be preempted: %+v", got[1])
	}
}

func TestSizeStackEntries_SameSymbolOnce(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "a", Priority: 0, AllocPct: 0.5, Symbol: "TECL", Price: 10,
			Raw: backtestmodels.Signal{Symbol: "TECL"}},
		{StrategyID: "b", Priority: 1, AllocPct: 0.5, Symbol: "TECL", Price: 10},
	}
	got := SizeStackEntries(10000, 10000, nil, cands)
	if got[0].Quantity <= 0 {
		t.Fatal("first fill should take TECL")
	}
	if got[1].Quantity != 0 {
		t.Errorf("second TECL should skip, qty=%v", got[1].Quantity)
	}
}

func TestAccountCapitalNoFallback(t *testing.T) {
	eq, cash := accountCapital(nil)
	if eq != 0 || cash != 0 {
		t.Errorf("no resolved account should yield zero equity/cash, not a guess: got %v/%v", eq, cash)
	}

	// A resolved account with no reported balance also gets zero, not a
	// hardcoded guess -- sizing must be driven only by real data.
	zeroAcc := &trader.ResolvedAccount{}
	eq, cash = accountCapital(zeroAcc)
	if eq != 0 || cash != 0 {
		t.Errorf("zero-balance account should not fall back to a guessed equity, got %v/%v", eq, cash)
	}

	acc := &trader.ResolvedAccount{}
	acc.Details.CurrentBalances.LiquidationValue = 80000
	acc.Details.CurrentBalances.CashAvailableForTrading = 20000
	eq, cash = accountCapital(acc)
	if eq != 80000 || cash != 20000 {
		t.Errorf("live snapshot equity/cash = %v/%v", eq, cash)
	}

	// A fully invested account reports no cash; it must not be sized as if cash equalled equity.
	invested := &trader.ResolvedAccount{}
	invested.Details.CurrentBalances.LiquidationValue = 80000
	eq, cash = accountCapital(invested)
	if eq != 80000 || cash != 0 {
		t.Errorf("fully invested account equity/cash = %v/%v, want 80000/0", eq, cash)
	}
}

func TestSizeStackEntries_SortsByPriority(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "pdd_tree", Priority: 2, AllocPct: 0.65, Symbol: "PDD", Price: 50},
		{StrategyID: "sig-voo-buy-tecl", Priority: 0, AllocPct: 0.65, Symbol: "TECL", Price: 100},
		{StrategyID: "mara_tree", Priority: 1, AllocPct: 0.65, Symbol: "MARA", Price: 10},
	}
	got := SizeStackEntries(100000, 100000, nil, cands)
	if got[0].StrategyID != "sig-voo-buy-tecl" || got[0].Quantity != 650 {
		t.Errorf("primary should fill first: %+v", got[0])
	}
	if got[1].StrategyID != "mara_tree" || got[1].Quantity != 3325 {
		t.Errorf("mara leftover second: %+v", got[1])
	}
}

func TestSizeStackEntries_InsufficientCash(t *testing.T) {
	cands := []scan_market.Signal{
		{StrategyID: "sig-voo-buy-tecl", Priority: 0, AllocPct: 0.65, Symbol: "TECL", Price: 100},
	}
	got := SizeStackEntries(100000, 50, nil, cands)
	if got[0].Quantity != 0 || got[0].Reason == "" {
		t.Errorf("expected skip on leftover cash, got %+v", got[0])
	}
}

// TestHeldSymbolsBrokerIsTruth confirms heldSymbols follows the broker
// snapshot and ignores a stale OPEN manage_exit row, falling back to
// manage_exit rows only when there is no broker snapshot.
func TestHeldSymbolsBrokerIsTruth(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := manage_exits.Open(db, manage_exits.OpenTradeParams{
		AccountNumber:  "74234603",
		Symbol:         "PDD",
		StrategyName:   "pdd_tree",
		EntryPrice:     10,
		Quantity:       1,
		EntryTime:      time.Now().UTC(),
		MaxTradingDays: 1,
	}); err != nil {
		t.Fatal(err)
	}

	acc := &trader.ResolvedAccount{}
	acc.Details.Positions = []trader.Position{
		{LongQuantity: 10, Instrument: trader.PositionInstrument{Symbol: "mara"}},
		{LongQuantity: 0, ShortQuantity: 0, Instrument: trader.PositionInstrument{Symbol: "SKIP"}},
	}
	held := heldSymbols(db, acc, "74234603")
	if !held["MARA"] {
		t.Fatalf("held=%v, want MARA from broker", held)
	}
	if held["PDD"] {
		t.Fatalf("held=%v: stale OPEN manage_exit row must not count when the broker snapshot shows no PDD", held)
	}
	if fb := heldSymbols(db, nil, "74234603"); !fb["PDD"] {
		t.Fatalf("fallback held=%v, want PDD from manage_exit when no broker snapshot", fb)
	}
	if held["SKIP"] {
		t.Fatal("zero-qty position must not count as held")
	}
}

func TestStagedOrderLifecycle(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id, err := InsertStagedOrder(db, &StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "sig-voo-buy-tecl",
		Symbol:          "VOO",
		Instruction:     trader.InstructionBuy,
		Quantity:        3,
		EntryPrice:      500.0,
		TakeProfitPrice: 525.0,
		StopLossPrice:   475.0,
		MaxTradingDays:  8,
		Status:          StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("failed to insert staged order: %v", err)
	}

	pending, err := GetPendingStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get pending staged orders: %v", err)
	}
	if len(pending) != 1 || pending[0].Symbol != "VOO" {
		t.Fatalf("expected 1 pending staged order for VOO, got %+v", pending)
	}

	if err := MarkStagedOrderRejected(db, id, "insufficient buying power"); err != nil {
		t.Fatalf("failed to mark staged order rejected: %v", err)
	}

	pendingAfter, err := GetPendingStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get pending staged orders after reject: %v", err)
	}
	if len(pendingAfter) != 0 {
		t.Errorf("expected 0 pending staged orders after reject, got %d", len(pendingAfter))
	}

	all, err := GetStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get all staged orders: %v", err)
	}
	if len(all) != 1 || all[0].Status != StagedOrderRejected || all[0].RejectReason != "insufficient buying power" {
		t.Errorf("expected 1 rejected staged order with reason recorded, got %+v", all)
	}
}

func TestGetStagedOrdersHandlesNullSignalID(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// signal_id is a nullable column; rows written without one (e.g. staged
	// manually, or by any future caller that omits it) store SQL NULL, not 0.
	// Scanning must tolerate that rather than assuming an int64 column.
	_, err := db.Exec(`
		INSERT INTO stage_order (
			account_number, strategy_name, symbol, instruction,
			quantity, entry_price, take_profit_price, stop_loss_price,
			max_trading_days, status, staged_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"74234603", "sig-voo-buy-tecl", "VOO", trader.InstructionBuy,
		1.0, 500.0, 525.0, 475.0, 8, StagedOrderPending, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("failed to insert staged order with NULL signal_id: %v", err)
	}

	pending, err := GetPendingStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("GetPendingStagedOrders failed on NULL signal_id: %v", err)
	}
	if len(pending) != 1 || pending[0].SignalID != 0 {
		t.Errorf("expected 1 staged order with SignalID 0, got %+v", pending)
	}

	all, err := GetStagedOrders(db, "74234603")
	if err != nil {
		t.Fatalf("GetStagedOrders failed on NULL signal_id: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 staged order, got %d", len(all))
	}
}

func TestClaimStagedOrderForSubmit_OnlyOneWinner(t *testing.T) {
	// File DB + MaxOpenConns(1): :memory: gives each connection its own empty DB.
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "claim.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}

	id, err := InsertStagedOrder(db, &StagedOrder{
		AccountNumber:   "74234603",
		StrategyName:    "mara_tree",
		Symbol:          "MARA",
		Instruction:     trader.InstructionBuy,
		Quantity:        10,
		EntryPrice:      11.0,
		TakeProfitPrice: 12.0,
		StopLossPrice:   10.0,
		MaxTradingDays:  1,
		Status:          StagedOrderPending,
		StagedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("InsertStagedOrder: %v", err)
	}

	var wg sync.WaitGroup
	wins := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cid := orderintent.FromStagedKey("74234603", "MARA", "BUY", "2026-09-21", id)
			// All use same client_order_id — first claim wins; others get claimed=false
			// (or unique constraint if somehow double-set with different ids).
			ok, err := ClaimStagedOrderForSubmit(db, id, cid)
			if err != nil {
				t.Errorf("claim err: %v", err)
			}
			wins <- ok
		}(i)
	}
	wg.Wait()
	close(wins)
	var n int
	for w := range wins {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 claim winner, got %d", n)
	}
	all, _ := GetStagedOrders(db, "74234603")
	if len(all) != 1 || all[0].Status != StagedOrderSubmitting {
		t.Fatalf("expected SUBMITTING, got %+v", all)
	}
	if all[0].ClientOrderID == "" {
		t.Fatal("expected client_order_id set")
	}
}

func TestClientOrderIDUniqueRejectsDuplicate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now().UTC()
	id1, err := InsertStagedOrder(db, &StagedOrder{
		AccountNumber: "1", StrategyName: "s", Symbol: "A", Instruction: "BUY",
		Quantity: 1, EntryPrice: 1, TakeProfitPrice: 2, StopLossPrice: 0.5,
		MaxTradingDays: 1, Status: StagedOrderPending, StagedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := InsertStagedOrder(db, &StagedOrder{
		AccountNumber: "1", StrategyName: "s", Symbol: "B", Instruction: "BUY",
		Quantity: 1, EntryPrice: 1, TakeProfitPrice: 2, StopLossPrice: 0.5,
		MaxTradingDays: 1, Status: StagedOrderPending, StagedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := ClaimStagedOrderForSubmit(db, id1, "co_same")
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	_, err = ClaimStagedOrderForSubmit(db, id2, "co_same")
	if err == nil {
		t.Fatal("expected UNIQUE failure on duplicate client_order_id")
	}
}

func TestMigrateAddsClientOrderIDColumn(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	var name string
	err := db.QueryRow(`SELECT name FROM pragma_table_info('stage_order') WHERE name='client_order_id'`).Scan(&name)
	if err != nil || name != "client_order_id" {
		t.Fatalf("column missing: %v %q", err, name)
	}
}

func TestFromStagedKeyStableAcrossProcesses(t *testing.T) {
	a := orderintent.FromStagedKey("acc", "X", "BUY", "2026-09-21", 42)
	b := orderintent.FromStagedKey("acc", "X", "BUY", "2026-09-21", 42)
	if a != b {
		t.Fatalf("%s != %s", a, b)
	}
}

func TestOpenIntentStoreTemp(t *testing.T) {
	dir := t.TempDir()
	store, err := orderintent.Open(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
}

// An order claimed for submission whose credentials were refused never reached the broker, so
// releasing the claim returns it to PENDING (same client_order_id) for the next run, and only
// from SUBMITTING.
func TestReleaseStagedOrderClaim(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	id, err := InsertStagedOrder(db, &StagedOrder{
		AccountNumber: "74234603", StrategyName: "hmm-calm-soxl", Symbol: "SOXL", Instruction: trader.InstructionBuy,
		Quantity: 5, EntryPrice: 83.56, StopLossPrice: 73.53, MaxTradingDays: 3,
		Status: StagedOrderPending, StagedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := ClaimStagedOrderForSubmit(db, id, "co_test"); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := ReleaseStagedOrderClaim(db, id); err != nil {
		t.Fatal(err)
	}
	o, err := GetStagedOrder(db, id)
	if err != nil || o.Status != StagedOrderPending || o.ClientOrderID != "co_test" {
		t.Fatalf("after release: %+v %v", o, err)
	}
	// A rejected order is not reopened by a release.
	if err := MarkStagedOrderRejected(db, id, "no"); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseStagedOrderClaim(db, id); err != nil {
		t.Fatal(err)
	}
	if o, _ := GetStagedOrder(db, id); o.Status != StagedOrderRejected {
		t.Fatalf("a rejected order was reopened: %s", o.Status)
	}
}

func TestReopenRejectedOrder(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	mk := func(orderID string) int64 {
		id, err := InsertStagedOrder(db, &StagedOrder{
			AccountNumber: "74234603", StrategyName: "hmm-calm-soxl", Symbol: "SOXL", Instruction: trader.InstructionBuy,
			Quantity: 5, EntryPrice: 83.56, StopLossPrice: 73.53, MaxTradingDays: 3,
			Status: StagedOrderRejected, StagedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if orderID != "" {
			if _, err := db.Exec(`UPDATE stage_order SET order_id = ? WHERE id = ?`, orderID, id); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	never, placed := mk(""), mk("1008251668279")
	if ok, err := ReopenRejectedOrder(db, never); err != nil || !ok {
		t.Fatalf("a rejected order that never reached the broker should reopen: %v %v", ok, err)
	}
	if o, _ := GetStagedOrder(db, never); o.Status != StagedOrderPending {
		t.Fatalf("status %s, want PENDING", o.Status)
	}
	if ok, _ := ReopenRejectedOrder(db, placed); ok {
		t.Fatal("an order with a broker order id must not be reopened")
	}
	if ok, _ := ReopenRejectedOrder(db, never); ok {
		t.Fatal("a PENDING order is not reopened again")
	}
}
