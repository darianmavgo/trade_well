package strategy_test

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/hold_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func TestRowFamilyLooksUpWithoutRegisteringRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS hold_strategy (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, symbol TEXT NOT NULL,
		total_return INTEGER NOT NULL DEFAULT 0, allocation_pct REAL NOT NULL,
		cash_yield REAL NOT NULL, slippage_pct REAL NOT NULL,
		trailing_stop_pct REAL NOT NULL DEFAULT 0, sma_reentry_period INTEGER NOT NULL DEFAULT 0);
		INSERT INTO hold_strategy (id, name, symbol, total_return, allocation_pct, cash_yield, slippage_pct)
		VALUES ('hold-zzfam-a','A','VOO',0,1,0,0), ('hold-zzfam-b','B','QQQ',0,1,0,0)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	hold_strategy.RegisterFrom(path)

	for _, id := range []string{"hold-zzfam-a", "HOLD_ZZFAM_A", "holdzzfama"} {
		s, ok := strategy.Get(id)
		if !ok || s.ID() != "hold-zzfam-a" {
			t.Fatalf("Get(%q) = %v, %v", id, s, ok)
		}
	}
	if _, ok := strategy.Get("hold-zzfam-nope"); ok {
		t.Fatal("unknown id resolved")
	}
	for _, s := range strategy.List() {
		if s.ID() == "hold-zzfam-a" {
			t.Fatal("row was registered individually")
		}
	}
	got := map[string]bool{}
	for _, s := range strategy.ListAll() {
		got[s.ID()] = true
	}
	if !got["hold-zzfam-a"] || !got["hold-zzfam-b"] {
		t.Fatalf("ListAll missing rows")
	}
	// A strategy registered in Go has no row in strategies.db: it is found by its id and listed by
	// List, but "all" does not run it.
	strategy.NewSQLPipelineStrategy("zz-go-only", "Go only", "registered in Go, no row", "sql/strategies/streak_strategy", strategy.StrategyConfig{AllocationPct: 0.1})
	if _, ok := strategy.Get("zz-go-only"); !ok {
		t.Fatal("a Go-registered strategy must still be found by its id")
	}
	for _, s := range strategy.ListAll() {
		if s.ID() == "zz-go-only" {
			t.Fatal("ListAll includes a strategy that is not a row of strategies.db")
		}
	}
	found := false
	for _, fc := range strategy.FamilyCounts() {
		if fc.Name == "hold" && fc.Count == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("FamilyCounts = %+v", strategy.FamilyCounts())
	}
}
