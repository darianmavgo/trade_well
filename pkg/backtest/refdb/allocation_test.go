package refdb

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestSetAllocationSizesEveryRowAndRecordsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tree_strategy (id, name, signal_symbol, trade_symbol, direction, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, next_day_limit, coil_range_max, sma_bounce_min, sma_bounce_max)
		VALUES ('t1','t1','AMD','AMD','long',5,0.05,0.05,0.65,0,0,0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, ok, err := BundleAllocation(path); err != nil || ok {
		t.Fatalf("a bundle with no allocation reported one: ok=%v err=%v", ok, err)
	}
	if err := SetAllocation(path, 0.20); err != nil {
		t.Fatal(err)
	}
	got, ok, err := BundleAllocation(path)
	if err != nil || !ok || got != 0.20 {
		t.Fatalf("BundleAllocation = %v, %v, %v", got, ok, err)
	}
	chk, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer chk.Close()
	var alloc float64
	if err := chk.Get(&alloc, `SELECT allocation_pct FROM tree_strategy WHERE id = 't1'`); err != nil || alloc != 0.20 {
		t.Fatalf("row allocation = %v, %v", alloc, err)
	}
	for _, bad := range []float64{0, -1, 1.5} {
		if err := SetAllocation(path, bad); err == nil {
			t.Errorf("SetAllocation(%v) should fail", bad)
		}
	}
}
