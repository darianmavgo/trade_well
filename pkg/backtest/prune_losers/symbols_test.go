package prune_losers

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// trades builds n trades of a symbol: wins of +10 and the rest of -pnlLoss.
func trades(sym string, n, wins int, loss float64) []models.Trade {
	var out []models.Trade
	for i := 0; i < n; i++ {
		pnl := -loss
		if i < wins {
			pnl = 10
		}
		out = append(out, models.Trade{Symbol: sym, EntryDate: "2024-01-02", ExitDate: "2024-01-03", NetPnL: pnl})
	}
	return out
}

func writeRun(t *testing.T, dir, id string, all ...[]models.Trade) {
	t.Helper()
	r, err := storage.OpenResults(dir, "rotation")
	if err != nil {
		t.Fatal(err)
	}
	var tr []models.Trade
	for _, a := range all {
		tr = append(tr, a...)
	}
	if _, err := r.WriteRun(storage.RunMeta{StrategyID: id, Kind: "single"}, storage.RunPayload{Trades: tr}); err != nil {
		t.Fatal(err)
	}
	r.Close()
}

func symbolsFixture(t *testing.T) (strategiesDB, reports string) {
	t.Helper()
	root := t.TempDir()
	reports = filepath.Join(root, "reports")
	run := filepath.Join(reports, "7")
	// LOSER: down both windows, 3 wins of 10 trades (30%). HIGHWIN: down both, 6 of 10 (60%).
	// ISONLY: down in-sample, up held-out. OOSONLY: up in-sample, down held-out.
	// NOOOS: down in-sample, no held-out trades. GOOD: up in both.
	writeRun(t, run, "rot",
		trades("LOSER", 5, 1, 5), trades("HIGHWIN", 5, 3, 40), trades("ISONLY", 5, 1, 5), trades("OOSONLY", 5, 4, 1), trades("NOOOS", 5, 1, 5), trades("GOOD", 5, 4, 1))
	writeRun(t, filepath.Join(run, "oos"), "rot",
		trades("LOSER", 5, 1, 5), trades("HIGHWIN", 5, 3, 40), trades("ISONLY", 5, 4, 1), trades("OOSONLY", 5, 1, 5), trades("GOOD", 5, 4, 1))

	strategiesDB = filepath.Join(root, "strategies.db")
	db, err := refdb.Open(strategiesDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO symbol_lists (symbol_list_id, list) VALUES ('basket', 'GOOD,HIGHWIN,ISONLY,LOSER,NOOOS,OOSONLY,UNTRADED')`,
		`INSERT INTO rotation_strategy (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct, period, side, pick)
		 VALUES ('rot', 'Rot', 'basket', 7, 7, 0, 0.1, 'QQQ', 0, 0.1, 0, 0, '1d', 'long', 'all')`,
		`INSERT INTO rotation_strategy (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct, period, side, pick)
		 VALUES ('lit', 'Lit', 'GOOD,LOSER', 2, 2, 0, 0.1, 'QQQ', 0, 0.1, 0, 0, '1d', 'long', 'all')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return strategiesDB, reports
}

func TestPruneSymbolsNeedsAllThreeConditions(t *testing.T) {
	sdb, reports := symbolsFixture(t)
	var out bytes.Buffer
	res, err := PruneSymbols(SymbolsConfig{StrategiesDB: sdb, ReportsDir: reports, Strategy: "rot", MinWinRate: 0.4, DryRun: true, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Removed, []string{"LOSER"}) {
		t.Fatalf("removed %v, want only LOSER (HIGHWIN wins 60%%, ISONLY and OOSONLY win a window, NOOOS has no held-out trades)\n%s", res.Removed, out.String())
	}
	if res.RunID != 7 {
		t.Errorf("run %d, want the latest run with both windows (7)", res.RunID)
	}
	db, _ := refdb.Open(sdb)
	defer db.Close()
	var syms string
	db.Get(&syms, `SELECT symbols FROM rotation_strategy WHERE id = 'rot'`)
	if syms != "basket" {
		t.Fatalf("a dry run changed the row: %q", syms)
	}
}

func TestPruneSymbolsPointsTheStrategyAtACopyOfTheList(t *testing.T) {
	sdb, reports := symbolsFixture(t)
	cfg := SymbolsConfig{StrategiesDB: sdb, ReportsDir: reports, Strategy: "rot", MinWinRate: 0.4}
	if _, err := PruneSymbols(cfg); err != nil {
		t.Fatal(err)
	}
	db, _ := refdb.Open(sdb)
	defer db.Close()
	var row, pruned, original string
	db.Get(&row, `SELECT symbols FROM rotation_strategy WHERE id = 'rot'`)
	db.Get(&pruned, `SELECT list FROM symbol_lists WHERE symbol_list_id = 'basket-pruned'`)
	db.Get(&original, `SELECT list FROM symbol_lists WHERE symbol_list_id = 'basket'`)
	if row != "basket-pruned" || pruned != "GOOD,HIGHWIN,ISONLY,NOOOS,OOSONLY,UNTRADED" {
		t.Fatalf("row %q, pruned list %q", row, pruned)
	}
	if !strings.Contains(original, "LOSER") {
		t.Fatalf("the shared list must not change: %q", original)
	}
	// Run again: nothing left to remove, and the list name does not grow another suffix.
	res, err := PruneSymbols(cfg)
	if err != nil || len(res.Removed) != 0 {
		t.Fatalf("second run removed %v (%v)", res.Removed, err)
	}
	// The strategy is still a runnable row.
	if got, ok, err := refdb.RotationStrategyByID(db, "rot"); err != nil || !ok || strings.Contains(got.Symbols, "LOSER") || !strings.Contains(got.Symbols, "GOOD") {
		t.Fatalf("row after prune: %+v ok=%v err=%v", got, ok, err)
	}
}

func TestPruneSymbolsRewritesALiteralList(t *testing.T) {
	sdb, reports := symbolsFixture(t)
	// Give the literal strategy the same results.
	writeRun(t, filepath.Join(reports, "8"), "lit", trades("LOSER", 5, 1, 5), trades("GOOD", 5, 4, 1))
	writeRun(t, filepath.Join(reports, "8", "oos"), "lit", trades("LOSER", 5, 1, 5), trades("GOOD", 5, 4, 1))
	res, err := PruneSymbols(SymbolsConfig{StrategiesDB: sdb, ReportsDir: reports, Strategy: "lit", MinWinRate: 0.4})
	if err != nil || res.RunID != 8 || !reflect.DeepEqual(res.Removed, []string{"LOSER"}) {
		t.Fatalf("%+v %v", res, err)
	}
	db, _ := refdb.Open(sdb)
	defer db.Close()
	var syms string
	db.Get(&syms, `SELECT symbols FROM rotation_strategy WHERE id = 'lit'`)
	if syms != "GOOD" {
		t.Fatalf("literal list is %q, want GOOD", syms)
	}
}

func TestPruneSymbolsRefusals(t *testing.T) {
	sdb, reports := symbolsFixture(t)
	for name, cfg := range map[string]SymbolsConfig{
		"no strategy":      {StrategiesDB: sdb, ReportsDir: reports},
		"unknown strategy": {StrategiesDB: sdb, ReportsDir: reports, Strategy: "nope"},
		"no results":       {StrategiesDB: sdb, ReportsDir: t.TempDir(), Strategy: "rot"},
		"run without both": {StrategiesDB: sdb, ReportsDir: reports, Strategy: "lit", RunID: 7},
	} {
		if _, err := PruneSymbols(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
