package refdb

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSymbolList(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "strategies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO symbol_lists (symbol_list_id, list) VALUES ('mine', 'SPY, qqq,,IWM')`); err != nil {
		t.Fatal(err)
	}
	got, ok, err := SymbolList(db, "mine")
	if err != nil || !ok || !reflect.DeepEqual(got, []string{"SPY", "QQQ", "IWM"}) {
		t.Fatalf("got %v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := SymbolList(db, "missing"); ok || err != nil {
		t.Fatalf("missing list: ok=%v err=%v", ok, err)
	}
}

// A rotation row whose symbols equals a symbol_list_id gets that list; any other
// value stays a literal ticker list.
func TestRotationSymbolsResolveToSymbolList(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "strategies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO symbol_lists (symbol_list_id, list) VALUES ('etf-pre-2021', 'SPY,QQQ,IWM')`,
		`INSERT INTO rotation_strategy (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, allocation_pct, cash_yield, slippage_pct)
		 VALUES ('by-list', 'n', ' ETF-pre-2021 ', 1, 1, 0, 1, 1, 0, 0),
		        ('literal', 'n', 'TQQQ,SOXL', 1, 1, 0, 1, 1, 0, 0),
		        ('market',  'n', '',           1, 1, 0, 1, 1, 0, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]string{}
	rows, err := RotationStrategies(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		got[r.ID] = r.Symbols
	}
	want := map[string]string{"by-list": "SPY,QQQ,IWM", "literal": "TQQQ,SOXL", "market": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	one, ok, err := RotationStrategyByID(db, "by-list")
	if err != nil || !ok || one.Symbols != "SPY,QQQ,IWM" {
		t.Fatalf("by id: %+v ok=%v err=%v", one, ok, err)
	}
}
