package storage

import (
	"path/filepath"
	"testing"
)

func TestInSampleEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE backtest_start (symbol TEXT, Date TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"2021-01-04", "2026-06-30", "2026-06-30 09:31"} {
		if _, err := db.Exec(`INSERT INTO backtest_start VALUES ('VOO', ?)`, d); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	end, ok, err := InSampleEnd(path, "backtest_start", "2021-01-01", "", 12)
	if err != nil || !ok || end != "2025-06-30" {
		t.Fatalf("got %q %v %v, want 2025-06-30", end, ok, err)
	}
	if _, ok, _ := InSampleEnd(path, "backtest_start", "2021-01-01", "", 0); ok {
		t.Fatal("0 months must not hold out")
	}
	if _, ok, _ := InSampleEnd(path, "backtest_start", "2026-01-01", "", 12); ok {
		t.Fatal("history shorter than a year must not split")
	}
}

func TestEnsureBarTableCreatesSymbolStatsView(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2; i++ { // second call must be a no-op
		if err := EnsureBarTable(db, "backtest_start"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO backtest_start (Date, symbol) VALUES ('2026-01-02', 'VOO'), ('2026-01-05', 'VOO')`); err != nil {
		t.Fatal(err)
	}
	var sym string
	var n int
	if err := db.QueryRow(`SELECT symbol, "count(*)" FROM v_symbol_stats`).Scan(&sym, &n); err != nil || sym != "VOO" || n != 2 {
		t.Fatalf("v_symbol_stats = %q %d, %v", sym, n, err)
	}
}
