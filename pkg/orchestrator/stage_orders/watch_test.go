package stage_orders

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// bookStrategy is a limit-entry strategy that puts a day limit on many names and takes a
// name out of the rotation after a fill, as the sector-pairs rotation does.
type bookStrategy struct{ cfg strategy.StrategyConfig }

func (s bookStrategy) ID() string                             { return "book" }
func (s bookStrategy) Name() string                           { return "book" }
func (s bookStrategy) Description() string                    { return "" }
func (s bookStrategy) DefaultConfig() strategy.StrategyConfig { return s.cfg }
func (s bookStrategy) Validate() error                        { return nil }
func (s bookStrategy) SetDatabases(string, string)            {}
func (s bookStrategy) GenerateSignals(map[string][]backtestmodels.Bar) []backtestmodels.Signal {
	return nil
}

func bookConfig() strategy.StrategyConfig {
	return strategy.StrategyConfig{
		EntryLimitPct: 0.95, TakeProfitPct: 0.03, StopLossPct: 0.95, HoldingWindow: 2,
		AllocationPct: 0.20, OneFillPerMonth: true, PositionCap: 48,
	}
}

func bookSignals(session string, symbols ...string) []scan_market.Signal {
	var out []scan_market.Signal
	for _, sym := range symbols {
		out = append(out, scan_market.Signal{
			StrategyID: "book", AllocPct: 0.20, Symbol: sym, Price: 95, NextSession: session,
			Raw: backtestmodels.Signal{Symbol: sym, Date: "2026-10-01", Close: 100, BuyLimit: 95, Direction: "LONG"},
		})
	}
	return out
}

// Resting buy orders reserve cash at the broker, so a book of 20% orders cannot all be
// placed. WatchEntries claims no cash: every name is a candidate, unlike SizeStackEntries.
func TestWatchEntriesClaimNoCash(t *testing.T) {
	var syms []string
	for i := 0; i < 47; i++ {
		syms = append(syms, fmt.Sprintf("S%02d", i))
	}
	cands := bookSignals("2026-10-02", syms...)
	sized := SizeStackEntries(100000, 100000, nil, cands)
	funded := 0
	for _, e := range sized {
		if e.Quantity >= 1 {
			funded++
		}
	}
	if funded > 10 {
		t.Fatalf("test premise: cash-sizing funds only the first few of 47 names, got %d", funded)
	}
	watched := WatchEntries(100000, map[string]bool{"S05": true}, cands)
	got := 0
	for _, e := range watched {
		if e.Quantity > 0 {
			got++
			if e.Quantity != 210 { // 20% of 100k at $95
				t.Fatalf("%s full-size quantity %v, want 210", e.Symbol, e.Quantity)
			}
		}
		if e.Symbol == "S05" && (e.Quantity != 0 || e.Reason == "") {
			t.Errorf("a held name is skipped with a reason: %+v", e)
		}
	}
	if got != 46 {
		t.Fatalf("%d of 47 names watched, want all but the held one (46)", got)
	}
}

func TestRunStagesLimitEntriesAsWatchingDayOrdersAndHonorsTheMonthlyCooldown(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	insertFill := func(sym string, filled time.Time) {
		if _, err := db.Exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_price, target_profit_price, quantity, entry_time, entry_filled_at, status)
			VALUES ('A1', ?, 'book', 'book', 95, 98, 10, ?, ?, 'CLOSED')`, sym, filled, filled); err != nil {
			t.Fatal(err)
		}
	}
	// ROM filled earlier this (October) month; SSO filled last month; both are signalled again.
	insertFill("ROM", time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC))
	insertFill("SSO", time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC))

	acc := &trader.ResolvedAccount{AccountNumber: "A1"}
	acc.Details.CurrentBalances.LiquidationValue = 100000
	acc.Details.CurrentBalances.CashAvailableForTrading = 100000
	res, err := Run(context.Background(), db, acc, "book", bookSignals("2026-10-05", "QLD", "ROM", "SSO"), []strategy.Strategy{bookStrategy{bookConfig()}}, 0, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := GetStagedOrders(db, "A1")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]StagedOrder{}
	for _, o := range rows {
		by[o.Symbol] = o
	}
	if _, staged := by["ROM"]; staged {
		t.Errorf("ROM filled earlier this month and must be out of the rotation: %+v", by["ROM"])
	}
	for _, sym := range []string{"QLD", "SSO"} {
		o, ok := by[sym]
		if !ok {
			t.Fatalf("%s should be staged (rows %v)", sym, by)
		}
		if o.Status != StagedOrderWatching || o.EntryDuration != EntryDurationDay || o.SessionDate != "2026-10-05" || o.AllocPct != 0.20 {
			t.Errorf("%s staged as %+v, want WATCHING, a DAY entry for 2026-10-05 at 20%%", sym, o)
		}
		if o.EntryPrice != 95 || o.MaxTradingDays != 2 || o.TakeProfitPrice < 97.84 || o.TakeProfitPrice > 97.86 || o.StopLossPrice < 90.24 || o.StopLossPrice > 90.26 {
			t.Errorf("%s order prices: %+v", sym, o)
		}
	}
	skipped := false
	for _, r := range res.Rows {
		if r.Symbol == "ROM" && r.Status == "SKIPPED" && strings.Contains(r.Detail, "until next month") {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("ROM should be reported skipped for the monthly cooldown: %+v", res.Rows)
	}

	// A session in November is a new month: ROM is back in the rotation.
	res2, err := Run(context.Background(), db, acc, "book", bookSignals("2026-11-02", "ROM"), []strategy.Strategy{bookStrategy{bookConfig()}}, 0, false, 0)
	if err != nil || res2.OrdersStaged != 1 {
		t.Fatalf("ROM in November: staged %d, err %v", res2.OrdersStaged, err)
	}
}
