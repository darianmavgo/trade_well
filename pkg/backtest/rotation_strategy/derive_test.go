package rotation_strategy

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

func TestParseParamsFindsTheLabelInASweepLine(t *testing.T) {
	line := "  #1  Period-1d/Limit-95%/Hold-2d/TP+3%/SL-3%             Profit=+$258505.67  CAGR=37.73%  Idle=563 (+425% vs base)"
	got, err := ParseParams(line)
	want := Params{Period: "1d", EntryLimit: 0.95, HoldDays: 2, TakeProfit: 0.03, StopLoss: 0.03}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	if got.Label() != "Period-1d/Limit-95%/Hold-2d/TP+3%/SL-3%" {
		t.Fatalf("label round trip: %q", got.Label())
	}
	if p, err := ParseParams("Period-1w/Limit-100%/Hold-0d/TP+8%/SL-0%"); err != nil || p.EntryLimit != 1 || p.HoldDays != 0 || p.StopLoss != 0 {
		t.Fatalf("no hold and no stop: %+v, %v", p, err)
	}
	for _, bad := range []string{"", "Hold-2d", "Period-2y/Limit-95%/Hold-2d/TP+3%/SL-3%"} {
		if _, err := ParseParams(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestDefaultDerivedID(t *testing.T) {
	p := Params{Period: "1d", EntryLimit: 0.95, HoldDays: 2, TakeProfit: 0.03, StopLoss: 0.03}
	if got := DefaultDerivedID("rotation-2x-sector-pairs-daily-limit90", p); got != "rotation-2x-sector-pairs-daily-limit95-tp3-sl3-hold2" {
		t.Errorf("got %q", got)
	}
	p.Period = "1w"
	if got := DefaultDerivedID("biggest-loser-etf-1w", p); got != "biggest-loser-etf-1w-limit95-tp3-sl3-hold2-1w" {
		t.Errorf("got %q", got)
	}
}

func TestDeriveCopiesTheRowWithTheParameterSet(t *testing.T) {
	db, err := refdb.Open(filepath.Join(t.TempDir(), "strategies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO symbol_lists (symbol_list_id, list) VALUES ('my-list', 'AAA,BBB')`); err != nil {
		t.Fatal(err)
	}
	src := allRow()
	src.Symbols = "my-list"
	src.TopK, src.UniverseSize = 2, 2
	if _, err := db.NamedExec(`INSERT INTO rotation_strategy
		(id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct,
		 period, side, pick, entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct, cooldown)
		VALUES (:id, :name, :symbols, :universe_size, :top_k, :exit_buffer, :max_weight_pct, :regime_symbol, :regime_sma, :allocation_pct, :cash_yield, :slippage_pct,
		 :period, :side, :pick, :entry_limit_pct, :take_profit_pct, :hold_days, :stop_loss_pct, :cooldown)`, src); err != nil {
		t.Fatal(err)
	}

	p, _ := ParseParams("Period-1d/Limit-95%/Hold-2d/TP+3%/SL-3%")
	row, err := Derive(db, src.ID, "copy", "", p)
	if err != nil {
		t.Fatal(err)
	}
	if row.EntryLimitPct != 0.95 || row.HoldDays != 2 || row.TakeProfitPct != 0.03 || row.StopLossPct != 0.03 || row.Period != "1d" {
		t.Fatalf("parameters not applied: %+v", row)
	}
	if row.Pick != "all" || row.Cooldown != "month" || row.AllocationPct != src.AllocationPct || row.MaxWeightPct != src.MaxWeightPct {
		t.Fatalf("the rest of the row must be copied: %+v", row)
	}
	if !strings.Contains(row.Name, "Period-1d") {
		t.Errorf("default name %q should name the parameters", row.Name)
	}
	var raw string
	if err := db.Get(&raw, `SELECT symbols FROM rotation_strategy WHERE id = 'copy'`); err != nil || raw != "my-list" {
		t.Fatalf("the copy must keep the list id, got %q (%v)", raw, err)
	}
	if got, ok, _ := refdb.RotationStrategyByID(db, src.ID); !ok || got.EntryLimitPct != 0.9 {
		t.Fatalf("the source row must not change: %+v", got)
	}

	// Only the position size: the parameters of the source are kept.
	sized, err := DeriveWith(db, "copy", "", "", nil, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if sized.ID != "copy-pos20" || sized.AllocationPct != 0.2 || sized.MaxWeightPct != 0.2 || sized.EntryLimitPct != 0.95 || sized.HoldDays != 2 || sized.Cooldown != "month" {
		t.Fatalf("position-only copy: %+v", sized)
	}
	if got, ok, _ := refdb.RotationStrategyByID(db, "copy"); !ok || got.AllocationPct != src.AllocationPct {
		t.Fatalf("the source of a position-only copy must not change: %+v", got)
	}
	both, err := DeriveWith(db, src.ID, "", "", &p, 0.2)
	if err != nil || both.ID != "rot-all-limit95-tp3-sl3-hold2-pos20" || both.AllocationPct != 0.2 || both.StopLossPct != 0.03 {
		t.Fatalf("parameters and position size together: %+v %v", both, err)
	}
	if _, err := DeriveWith(db, src.ID, "x", "", nil, 0); err == nil {
		t.Error("a derive that changes nothing must be refused")
	}
	if _, err := DeriveWith(db, src.ID, "x", "", nil, 1.5); err == nil {
		t.Error("a position of 150% of the portfolio must be refused")
	}
	if _, err := Derive(db, src.ID, "copy", "", p); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("an existing id must be refused, got %v", err)
	}
	if _, err := Derive(db, "no-such", "x", "", p); err == nil {
		t.Error("an unknown source must be refused")
	}
	// A parameter set the row cannot run (a stop of 100%) writes nothing.
	bad := p
	bad.StopLoss = 1
	if _, err := Derive(db, src.ID, "bad", "", bad); err == nil {
		t.Error("a stop of 100% is not a runnable strategy")
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM rotation_strategy WHERE id = 'bad'`); err != nil || n != 0 {
		t.Errorf("a refused derive left %d rows", n)
	}
}
