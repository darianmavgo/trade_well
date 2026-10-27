package refdb

import (
	"path/filepath"
	"testing"
)

func TestStreakStrategyRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "ref.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wr := 0.76
	trades := 46
	row := StreakStrategy{
		ID: "streak-voo-up5-tqqq", Name: "VOO up5 → TQQQ",
		SignalSymbol: "VOO", TradeSymbol: "TQQQ", Direction: "rally",
		SignalDays: 5, HoldDays: 15, TakeProfitPct: 0.03, StopLossPct: 0.08,
		Regime: "All Regimes", AllocationPct: 0.65, CashYield: 0.045,
		WinRate: &wr, TotalTrades: &trades,
	}
	if err := UpsertStreakStrategies(db, []StreakStrategy{row}); err != nil {
		t.Fatal(err)
	}
	row.HoldDays = 12
	if err := UpsertStreakStrategies(db, []StreakStrategy{row}); err != nil {
		t.Fatal(err)
	}
	gotRows, err := StreakStrategies(db)
	if err != nil || len(gotRows) != 1 || gotRows[0].HoldDays != 12 || gotRows[0].WinRate == nil || *gotRows[0].WinRate != wr {
		t.Fatalf("StreakStrategies = %+v, %v", gotRows, err)
	}
}
