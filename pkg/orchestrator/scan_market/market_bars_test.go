package scan_market

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func writeLocalBars(t *testing.T, path string, bars []models.Bar) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.UpsertBars(db, "backtest_start", bars); err != nil {
		t.Fatal(err)
	}
}

func bar(sym, date string, close float64) models.Bar {
	return models.Bar{Symbol: sym, Date: date, Timeframe: "1d", Open: close, High: close + 1, Low: close - 1, Close: close, Volume: 100}
}

func TestPersistThenHydrateKeepsOnlyRequiredSymbolsAndNewSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 21, 0, 0, 0, time.UTC)
	db := setupTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	local := filepath.Join(dir, "market_history.db")
	writeLocalBars(t, local, []models.Bar{
		bar("VOO", "2026-10-14", 500), bar("VOO", "2026-10-15", 501),
		bar("TECL", "2026-10-15", 50),
		bar("ZZZZ", "2026-10-15", 9),  // not required: must never reach the database
		bar("VOO", "2020-01-02", 300), // older than the window
	})
	n, err := SaveMarketWorkingCopyToTurso(ctx, db, local, []string{"voo", "TECL"}, now)
	if err != nil || n != 3 {
		t.Fatalf("first persist = %d, %v; want 3 (VOO x2, TECL x1)", n, err)
	}
	var zzzz int
	_ = db.QueryRow(`SELECT count(*) FROM market_bars WHERE symbol IN ('ZZZZ') OR date < '2024-10-16'`).Scan(&zzzz)
	if zzzz != 0 {
		t.Errorf("%d unrequired or out-of-window row(s) stored", zzzz)
	}

	// A new session arrives: only it and the newest stored one (possibly provisional) are rewritten.
	writeLocalBars(t, local, []models.Bar{bar("VOO", "2026-10-16", 502)})
	n, err = SaveMarketWorkingCopyToTurso(ctx, db, local, []string{"VOO", "TECL"}, now)
	if err != nil || n != 3 { // VOO 10-15 and 10-16, TECL 10-15
		t.Fatalf("second persist = %d, %v; want 3", n, err)
	}
	var total int
	_ = db.QueryRow(`SELECT count(*) FROM market_bars`).Scan(&total)
	if total != 4 {
		t.Errorf("stored rows = %d, want 4", total)
	}

	// A cold instance: an empty local file is rebuilt from the database.
	fresh := filepath.Join(t.TempDir(), "market_history.db")
	n, err = BuildMarketWorkingCopyFromTurso(ctx, db, fresh, []string{"VOO", "TECL"}, now)
	if err != nil || n != 4 {
		t.Fatalf("hydrate = %d, %v; want 4", n, err)
	}
	last, err := storage.SymbolLastDate(fresh, "VOO")
	if err != nil || last != "2026-10-16" {
		t.Errorf("hydrated VOO tip = %q, %v", last, err)
	}
}

func TestPersistWithoutALocalFileIsANoOp(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	n, err := SaveMarketWorkingCopyToTurso(context.Background(), db, filepath.Join(t.TempDir(), "missing.db"), []string{"VOO"}, time.Now())
	if n != 0 || err != nil {
		t.Errorf("= %d, %v", n, err)
	}
}
