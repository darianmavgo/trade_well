package refdb

import (
	"os"
	"path/filepath"
	"testing"

	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// The markov migration seed makes one calm_filter hmm row per distinct trade symbol (a duplicate
// symbol is skipped), copies the markov rows to markov_strategy_retired, and can be run twice.
func TestHMMFromMarkovSeed(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "strategies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO markov_strategy
		(id, name, signal_symbol, trade_symbol, direction, target_state, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		VALUES
		('markov_model_aa',   'a', 'AA',   'AA',   'long', 'bull', 15, 0.05, 0, 0.25, 0,     0,     0),
		('markov_model_spxl', 'b', 'SPXL', 'SPXL', 'long', 'bull', 15, 0.05, 0, 0.20, 0.045, 0.0005, 0),
		('markov_model_spxl_2', 'c', 'SPXL', 'SPXL', 'long', 'bull', 15, 0.05, 0, 0.25, 0,   0,     0)`); err != nil {
		t.Fatal(err)
	}
	path, err := sqlfiles.Resolve("sql/seed/hmm_from_markov.sql")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(string(script)); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	var n, symbols, retired int
	if err := db.QueryRow(`SELECT count(*), count(DISTINCT trade_symbol) FROM hmm_strategy`).Scan(&n, &symbols); err != nil {
		t.Fatal(err)
	}
	if n != 2 || symbols != 2 {
		t.Fatalf("hmm rows %d, symbols %d, want 2 and 2", n, symbols)
	}
	if err := db.QueryRow(`SELECT count(*) FROM markov_strategy_retired`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if retired != 3 {
		t.Fatalf("retired rows %d, want 3", retired)
	}
	row, ok, err := HMMStrategyByID(db, "hmm-calm-spxl")
	if err != nil || !ok {
		t.Fatalf("hmm-calm-spxl: ok=%v err=%v", ok, err)
	}
	if row.HMMSymbol != "QQQ" || row.Rule != "calm_filter" || row.HoldDays != 3 || row.TakeProfitPct != 0 ||
		row.StopLossPct != 0.08 || row.AllocationPct != 0.20 || row.CashYield != 0.045 || row.SlippagePct != 0.0005 {
		t.Fatalf("row %+v", row)
	}
}
