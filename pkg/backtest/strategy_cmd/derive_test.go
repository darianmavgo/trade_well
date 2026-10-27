package strategy_cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

func TestDeriveCommandCopiesARowFromASweepLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO rotation_strategy
		(id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct, period, side, pick)
		VALUES ('base', 'Base', 'AAA,BBB', 2, 2, 0, 0.1, 'QQQ', 0, 0.1, 0, 0.0005, '1d', 'long', 'all')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var out bytes.Buffer
	line := "#1  Period-1d/Limit-95%/Hold-2d/TP+3%/SL-3%   Profit=+$258505.67  CAGR=37.73%"
	if err := Derive(&out, path, "base", "base-95", "Base 95", line); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "./bin/backtest -strategy base-95") {
		t.Errorf("output should say how to run it:\n%s", out.String())
	}
	db, err = refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, ok, err := refdb.RotationStrategyByID(db, "base-95")
	if err != nil || !ok || row.Name != "Base 95" || row.EntryLimitPct != 0.95 || row.HoldDays != 2 || row.Symbols != "AAA,BBB" {
		t.Fatalf("row %+v ok=%v err=%v", row, ok, err)
	}

	if err := Derive(&out, path, "", "", "", line); err == nil {
		t.Error("-from is required")
	}
	if err := Derive(&out, path, "base", "", "", "no parameters here"); err == nil {
		t.Error("text with no parameter set must be refused")
	}
}

func TestDeriveCommandPositionSizeAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO rotation_strategy
		(id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct, period, side, pick, entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct)
		VALUES ('base', 'Base', 'AAA,BBB', 2, 2, 0, 0.1, 'QQQ', 0, 0.1, 0, 0.0005, '1d', 'long', 'all', 0.95, 0.03, 2, 0.05),
		       ('ranked', 'Ranked', 'AAA,BBB', 2, 1, 0, 1, 'QQQ', 0, 1, 0, 0.0005, '1d', 'long', 'winner', 0, 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var out bytes.Buffer
	if err := DeriveWith(&out, path, "base", "base-20", "", "", 0.2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "20% of portfolio value") {
		t.Errorf("output should name the position size:\n%s", out.String())
	}
	db, err = refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	row, ok, err := refdb.RotationStrategyByID(db, "base-20")
	if err != nil || !ok || row.AllocationPct != 0.2 || row.MaxWeightPct != 0.2 || row.StopLossPct != 0.05 || row.EntryLimitPct != 0.95 {
		t.Fatalf("row %+v ok=%v err=%v", row, ok, err)
	}
	// A ranked row is sized by top_k, so a position size does not apply to it.
	if err := DeriveWith(&out, path, "ranked", "r20", "", "", 0.2); err == nil {
		t.Error("a position size on a row that ranks must be refused")
	}
	if err := DeriveWith(&out, path, "base", "x", "", "", 0); err == nil {
		t.Error("with neither parameters nor a position size there is nothing to derive")
	}
}
