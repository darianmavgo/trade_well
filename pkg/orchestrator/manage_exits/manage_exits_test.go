package manage_exits

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
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

func TestTradeTrackerLifecycle(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id, err := Open(db, OpenTradeParams{
		AccountNumber:     "74234603",
		Symbol:            "AAPL",
		StrategyName:      "Momentum5Pct",
		EntryPrice:        100.00,
		TargetProfitPrice: 105.00,
		StopLossPrice:     95.00,
		Quantity:          5,
		EntryTime:         time.Now().UTC().AddDate(0, 0, -5),
		MaxTradingDays:    8,
	})
	if err != nil {
		t.Fatalf("failed to open trade: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive trade id, got %d", id)
	}

	openTrades, err := GetOpenTrades(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get open trades: %v", err)
	}
	if len(openTrades) != 1 {
		t.Fatalf("expected 1 open trade, got %d", len(openTrades))
	}
	if openTrades[0].Symbol != "AAPL" || openTrades[0].TargetProfitPrice != 105.00 {
		t.Errorf("mismatch in trade details: %+v", openTrades[0])
	}

	// Close trade
	err = CloseActiveTrade(db, id, TradeStatusClosedProfit, "PROFIT_HIT", 105.00, time.Now().UTC())
	if err != nil {
		t.Fatalf("failed to close trade: %v", err)
	}

	openAfter, err := GetOpenTrades(db, "74234603")
	if err != nil {
		t.Fatalf("failed to get open trades after close: %v", err)
	}
	if len(openAfter) != 0 {
		t.Errorf("expected 0 open trades after close, got %d", len(openAfter))
	}
}

func TestWindowForTradeFallsBackToStrategy(t *testing.T) {
	strat, ok := strategy.Get("sig-voo-buy-tecl")
	if !ok {
		t.Skip("sig-voo-buy-tecl not registered in this backtestgosqlite build")
	}
	hold, target := windowForTrade(Trade{
		StrategyName:      "sig-voo-buy-tecl",
		Symbol:            "TECL",
		EntryPrice:        100,
		MaxTradingDays:    0,
		TargetProfitPrice: 0,
	}, map[string]strategy.Strategy{"sig-voo-buy-tecl": strat})
	if hold <= 0 {
		t.Fatalf("expected strategy holding window, got %d", hold)
	}
	if target <= 100 {
		t.Fatalf("expected strategy take-profit above entry, got %v", target)
	}
}

// A 1-day position filled Thursday is due at Friday's 09:35 run, which is its
// exit date, and not yet due on Thursday afternoon.
func TestExitsOnTheExitDate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	fill := et(2026, 9, 24, 12, 6) // Thursday, the session TSLL actually filled
	if _, err := Open(db, OpenTradeParams{
		AccountNumber: "25474194", Symbol: "TSLL", StrategyName: "tsll-daily-one-share",
		EntryOrderID: "1008052088386", EntryPrice: 10.19, TargetProfitPrice: 10.69,
		Quantity: 1, EntryTime: fill, FilledAt: fill, MaxTradingDays: 1,
	}); err != nil {
		t.Fatal(err)
	}
	acc := &trader.ResolvedAccount{AccountNumber: "25474194"}
	acc.Details.Positions = []trader.Position{
		{LongQuantity: 1, Instrument: trader.PositionInstrument{Symbol: "TSLL"}},
	}

	thursday, err := Run(context.Background(), db, nil, acc, nil, et(2026, 9, 24, 15, 50), true)
	if err != nil {
		t.Fatal(err)
	}
	if thursday.ClosedTimeExpiry != 0 || thursday.StillActive != 1 {
		t.Fatalf("Thursday afternoon is before the exit date: %+v", thursday)
	}

	friday, err := Run(context.Background(), db, nil, acc, nil, et(2026, 9, 25, 9, 35), true)
	if err != nil {
		t.Fatal(err)
	}
	if friday.ClosedTimeExpiry != 1 || len(friday.Rows) != 1 || friday.Rows[0].Signal != "EXIT_TIME_EXPIRY" {
		t.Fatalf("Friday 09:35 is the exit date and must be a time exit, got %+v", friday)
	}
}

func TestExitManagerAging(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Trade entered 12 calendar days ago (~8-9 trading days ago)
	entryTime := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 3, 16, 0, 0, 0, time.UTC) // 10 trading days later

	_, err := Open(db, OpenTradeParams{
		AccountNumber:     "74234603",
		Symbol:            "MSFT",
		StrategyName:      "Momentum5Pct",
		EntryPrice:        400.00,
		TargetProfitPrice: 420.00,
		Quantity:          2,
		EntryTime:         entryTime,
		MaxTradingDays:    8,
	})
	if err != nil {
		t.Fatalf("failed to open trade: %v", err)
	}

	acc := &trader.ResolvedAccount{
		AccountNumber: "74234603",
		HashValue:     "dummyHash",
	}
	acc.Details.Positions = []trader.Position{
		{LongQuantity: 2, Instrument: trader.PositionInstrument{Symbol: "MSFT"}},
	}

	res, err := Run(context.Background(), db, nil, acc, nil, now, true)
	if err != nil {
		t.Fatalf("ExitManager error: %v", err)
	}

	if res.TotalChecked != 1 {
		t.Errorf("expected 1 checked trade, got %d", res.TotalChecked)
	}
	if res.ClosedTimeExpiry != 1 {
		t.Errorf("expected 1 expired trade, got %d", res.ClosedTimeExpiry)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 email row, got %d: %+v", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	if row.Account != "74234603" || row.Strategy != "Momentum5Pct" || row.Symbol != "MSFT" {
		t.Errorf("expected row to identify account/strategy/symbol, got %+v", row)
	}
	if row.Signal != "EXIT_TIME_EXPIRY" || row.Status != "SIMULATED" {
		t.Errorf("expected a simulated time-expiry exit row, got %+v", row)
	}
}

// TestExitManagerSkipsPhantomPosition reproduces the production incident: a
// recorded as an open manage_exit row, but the limit order never actually
// filled (price gapped past the limit). manage_exits must never compute an
// exit -- let alone place a live market sell -- against a position that
// manage_exit believes exists but the broker never actually opened.
func TestExitManagerSkipsPhantomPosition(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := Open(db, OpenTradeParams{
		AccountNumber:     "74234603",
		Symbol:            "MARA",
		StrategyName:      "mara_tree",
		EntryPrice:        11.24,
		TargetProfitPrice: 11.80,
		StopLossPrice:     10.34,
		Quantity:          133,
		EntryTime:         time.Now().UTC(),
		MaxTradingDays:    1,
	})
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}

	// The broker reports NO MARA position -- the entry limit order never
	// filled (e.g. price gapped up well past the limit).
	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}

	res, err := Run(context.Background(), db, nil, acc, nil, time.Now().UTC(), false)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.ClosedProfit != 0 || res.ClosedTimeExpiry != 0 {
		t.Fatalf("expected no exit action against a phantom position, got ClosedProfit=%d ClosedTimeExpiry=%d", res.ClosedProfit, res.ClosedTimeExpiry)
	}
	if res.SkippedNoPosition != 1 {
		t.Fatalf("expected the phantom position to be counted as skipped, got %d", res.SkippedNoPosition)
	}
	if len(res.Rows) != 1 || res.Rows[0].Signal != "NO_POSITION" || res.Rows[0].Status != "SKIPPED" {
		t.Fatalf("expected a single NO_POSITION/SKIPPED row, got %+v", res.Rows)
	}

	// The manage_exit row must be left untouched (still OPEN) -- not
	// closed as if a real exit happened.
	open, err := GetOpenTrades(db, "74234603")
	if err != nil {
		t.Fatalf("GetOpenTrades error: %v", err)
	}
	if len(open) != 1 || open[0].Status != TradeStatusOpen {
		t.Fatalf("expected the trade to remain OPEN (unresolved, not phantom-closed), got %+v", open)
	}
}
