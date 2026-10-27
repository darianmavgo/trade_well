package refdb

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestExportCopiesOnlyTheNamedRows(t *testing.T) {
	src := filepath.Join(t.TempDir(), "full.db")
	db, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO streak_strategy (id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days, take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		 VALUES ('streak-a','x','VOO','TECL','drop',3,8,0.05,0,'All Regimes',0.65,0,0,0), ('streak-b','x','AAA','AAA','drop',3,8,0,0,'All Regimes',0.1,0,0,0)`,
		`INSERT INTO markov_strategy (id, name, signal_symbol, trade_symbol, direction, target_state, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		 VALUES ('markov_a','x','AMD','AMD','long','bull',15,0.05,0,0.25,0,0,1), ('markov_b','x','BBB','BBB','long','bull',15,0.05,0,0.25,0,0,1)`,
		`CREATE TABLE IF NOT EXISTS hold_strategy (id TEXT PRIMARY KEY, name TEXT, symbol TEXT, total_return INTEGER NOT NULL DEFAULT 0,
		   allocation_pct REAL, cash_yield REAL, slippage_pct REAL, trailing_stop_pct REAL NOT NULL DEFAULT 0, sma_reentry_period INTEGER NOT NULL DEFAULT 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	dst := filepath.Join(t.TempDir(), "bundle.db")
	found, err := Export(src, dst, []string{"streak-a", "markov_a"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(found["streak_strategy"], []string{"streak-a"}) || !reflect.DeepEqual(found["markov_strategy"], []string{"markov_a"}) {
		t.Fatalf("found %v", found)
	}

	// The bundle opens as a reference database and reads back through the normal accessors.
	b, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	row, ok, err := MarkovStrategyByID(b, "markov_a")
	if err != nil || !ok || row.SignalSymbol != "AMD" {
		t.Fatalf("markov_a: %+v %v %v", row, ok, err)
	}
	if _, ok, _ := MarkovStrategyByID(b, "markov_b"); ok {
		t.Fatal("a row that was not named was exported")
	}

	if _, err := Export(src, dst, []string{"streak-a", "no_such_row"}); err == nil || !strings.Contains(err.Error(), "no_such_row") {
		t.Fatalf("a missing id must be an error naming it, got %v", err)
	}
	if _, err := Export(src, dst, []string{"x'); DROP TABLE streak_strategy;--"}); err == nil {
		t.Fatal("an id with quotes must be refused")
	}
}

func TestExportModelsCopiesOnlyTheBundledSymbols(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle.db")
	b, err := Open(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Exec(`INSERT INTO markov_strategy (id, name, signal_symbol, trade_symbol, direction, target_state, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		 VALUES ('markov_a','x','amd','AMD','long','bull',15,0.05,0,0.25,0,0,1)`); err != nil {
		t.Fatal(err)
	}
	b.Close()

	full := filepath.Join(dir, "markov_models.db")
	db, err := storage.OpenSQLite(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.RunStage(db, "markov_model_schema", nil); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO markov_model_meta (symbol, bars, first_date, last_date, trained_at, lookback_days, state_threshold) VALUES ('AMD',100,'2020-01-01','2020-06-01','t',250,0.5), ('ZZZ',100,'2020-01-01','2020-06-01','t',250,0.5)`,
		`INSERT INTO markov_prediction VALUES ('AMD','2020-06-01',1,0.6,0.2,0.4), ('ZZZ','2020-06-01',1,0.6,0.2,0.4)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	dst := filepath.Join(dir, "out.db")
	syms, err := ExportModels("markov", full, bundle, dst)
	if err != nil || !reflect.DeepEqual(syms, []string{"AMD"}) {
		t.Fatalf("exported %v, %v", syms, err)
	}
	out, err := storage.OpenSQLiteReadOnly(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	var n int
	if err := out.Get(&n, `SELECT COUNT(*) FROM markov_prediction WHERE symbol <> 'AMD'`); err != nil || n != 0 {
		t.Fatalf("a symbol that is not bundled was exported: %d %v", n, err)
	}

	if _, err := b2(bundle); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportModels("markov", full, bundle, dst); err == nil || !strings.Contains(err.Error(), "QQQ") {
		t.Fatalf("a signal symbol with no model must be an error naming it, got %v", err)
	}
	if _, err := ExportModels("tree", filepath.Join(dir, "none.db"), bundle, dst); err == nil {
		t.Fatal("a missing source database must be an error")
	}
}

// b2 adds a markov row whose signal symbol has no trained model.
func b2(bundle string) (int64, error) {
	b, err := Open(bundle)
	if err != nil {
		return 0, err
	}
	defer b.Close()
	_, err = b.Exec(`INSERT INTO markov_strategy (id, name, signal_symbol, trade_symbol, direction, target_state, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, next_day_limit)
		 VALUES ('markov_b','x','QQQ','QQQ','long','bull',15,0.05,0,0.25,0,0,1)`)
	return 0, err
}

// A rotation row that names a symbol list is exported with that list, so the
// bundle resolves it the same way the full database does.
func TestExportCarriesTheSymbolListARowNames(t *testing.T) {
	src := filepath.Join(t.TempDir(), "full.db")
	db, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS hold_strategy (id TEXT PRIMARY KEY, name TEXT, symbol TEXT, total_return INTEGER NOT NULL DEFAULT 0,
		   allocation_pct REAL, cash_yield REAL, slippage_pct REAL, trailing_stop_pct REAL NOT NULL DEFAULT 0, sma_reentry_period INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO symbol_lists (symbol_list_id, list) VALUES ('etfs', 'SPY,QQQ'), ('unused', 'AAA')`,
		`INSERT INTO rotation_strategy (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, allocation_pct, cash_yield, slippage_pct)
		 VALUES ('rot-a', 'n', 'etfs', 1, 1, 0, 1, 1, 0, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	dst := filepath.Join(t.TempDir(), "bundle.db")
	if _, err := Export(src, dst, []string{"rot-a"}); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	row, ok, err := RotationStrategyByID(b, "rot-a")
	if err != nil || !ok || row.Symbols != "SPY,QQQ" {
		t.Fatalf("rot-a: %+v ok=%v err=%v", row, ok, err)
	}
	var n int
	if err := b.Get(&n, `SELECT COUNT(*) FROM symbol_lists`); err != nil || n != 1 {
		t.Fatalf("bundle should carry only the named list, has %d (err %v)", n, err)
	}
}
