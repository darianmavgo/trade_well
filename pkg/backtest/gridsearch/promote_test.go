package gridsearch

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func TestPromoteWritesQualifyingRowsOnce(t *testing.T) {
	parent, ok := strategy.Get("voo-up3")
	if !ok {
		t.Fatal("voo-up3 is not registered")
	}
	gridPath := filepath.Join(t.TempDir(), "grid.db")
	gdb, err := sqlx.Open("sqlite", gridPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureGridSearchSchema(gdb); err != nil {
		t.Fatal(err)
	}
	inserts := []struct {
		days, hold, trades int
		tp, sl, wr         float64
		regime             string
	}{
		{5, 15, 46, 0.03, 0.08, 0.76, "All Regimes"},
		{3, 15, 148, 0.03, 0.08, 0.73, "All Regimes"},
		{4, 10, 40, 0.05, 0.08, 0.90, "QQQ>=SMA50"},
		{2, 8, 100, 0.05, 0.08, 0.40, "All Regimes"},
	}
	for _, r := range inserts {
		if _, err := gdb.Exec(`
			INSERT INTO gridsearch_results (
				strategy_id, label, symbol, signal_days, hold_days, take_profit_pct, stop_loss_pct, regime, win_rate, total_trades
			) VALUES (?, ?, 'TQQQ', ?, ?, ?, ?, ?, ?, ?)`,
			parent.ID(), "label", r.days, r.hold, r.tp, r.sl, r.regime, r.wr, r.trades); err != nil {
			t.Fatal(err)
		}
	}
	gdb.Close()

	refPath := filepath.Join(t.TempDir(), "settings.db")
	cfg := PromoteConfig{
		Parents:    []strategy.Strategy{parent},
		GridDB:     gridPath,
		RefDB:      refPath,
		MinWinRate: 0.6,
		MinTrades:  30,
		Top:        5,
	}
	rep, err := Promote(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Written) != 2 {
		t.Fatalf("written %d skipped %v notes %v", len(rep.Written), rep.Skipped, rep.Notes)
	}
	ids := map[string]bool{}
	for _, row := range rep.Written {
		ids[row.ID] = true
		if row.SignalSymbol != "VOO" || row.TradeSymbol != "TQQQ" || row.AllocationPct != 0.65 {
			t.Fatalf("row %+v", row)
		}
	}
	if !ids["streak-voo-up5-tqqq"] || !ids["streak-voo-up3-tqqq"] {
		t.Fatalf("ids %v", ids)
	}
	if len(rep.Notes) == 0 {
		t.Fatal("expected a note that signal_symbol fell back to the parent strategy")
	}

	rep, err = Promote(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Written) != 2 {
		t.Fatalf("second promote wrote %d", len(rep.Written))
	}
	db, err := refdb.Open(refPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := refdb.StreakStrategies(db)
	if err != nil || len(rows) != 2 {
		t.Fatalf("table has %+v (%v)", rows, err)
	}
}
