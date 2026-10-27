package export_views

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestViewsMatchStreaksAndSMA(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "market.db")
	db, err := storage.OpenSQLite(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, Date DATETIME, timeframe TEXT, asset_class TEXT,
		open FLOAT, high FLOAT, low FLOAT, close FLOAT, "Adj Close" FLOAT, volume BIGINT, symbol TEXT)`); err != nil {
		t.Fatal(err)
	}
	closes := []float64{10, 9, 8, 9, 10, 11, 5} // down, down, up, up, up, split-like drop
	for i, c := range closes {
		if _, err := db.Exec(`INSERT INTO backtest_start (idx, Date, timeframe, open, high, low, close, volume, symbol)
			VALUES (?, ?, '1d', ?, ?, ?, ?, 100, 'AAA')`, i, fmt.Sprintf("2024-01-%02d", i+1), c, c, c, c); err != nil {
			t.Fatal(err)
		}
	}
	// an hourly bar must not appear in the daily views
	if _, err := db.Exec(`INSERT INTO backtest_start (idx, Date, timeframe, close, volume, symbol)
		VALUES (99, '2024-01-01 10:00:00', '1h', 1, 1, 'AAA')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	out := filepath.Join(dir, "views.db")
	views, err := Run(context.Background(), Options{MarketDB: src, DBOut: out, SQLOut: filepath.Join(dir, "v.sql")})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) < 10 {
		t.Fatalf("only %d views", len(views))
	}
	vdb, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer vdb.Close()

	var n int
	if err := vdb.QueryRow(`SELECT count(*) FROM v_daily_bars`).Scan(&n); err != nil || n != 7 {
		t.Fatalf("v_daily_bars rows = %d, %v", n, err)
	}
	var down, up int
	if err := vdb.QueryRow(`SELECT down_streak, up_streak FROM v_streak_slice WHERE date = '2024-01-03'`).Scan(&down, &up); err != nil || down != 2 || up != 0 {
		t.Fatalf("2024-01-03 streaks = %d/%d, %v", down, up, err)
	}
	if err := vdb.QueryRow(`SELECT down_streak, up_streak FROM v_streak_slice WHERE date = '2024-01-06'`).Scan(&down, &up); err != nil || down != 0 || up != 3 {
		t.Fatalf("2024-01-06 streaks = %d/%d, %v", down, up, err)
	}
	var sma float64
	if err := vdb.QueryRow(`SELECT sma50 FROM v_bar_sma WHERE date = '2024-01-03'`).Scan(&sma); err != nil || sma != 9 {
		t.Fatalf("sma50 = %v, %v", sma, err)
	}
	var jump string
	if err := vdb.QueryRow(`SELECT date FROM v_split_jumps`).Scan(&jump); err != nil || jump != "2024-01-07" {
		t.Fatalf("split jump = %q, %v", jump, err)
	}
	var vol float64
	if err := vdb.QueryRow(`SELECT avg_volume FROM v_avg_volume WHERE window_days = 20`).Scan(&vol); err != nil || vol != 100 {
		t.Fatalf("avg volume = %v, %v", vol, err)
	}
	if err := vdb.QueryRow(`SELECT count(*) FROM v_tree_features`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("v_tree_features needs 200 bars, got %d rows, %v", n, err)
	}
}

func TestRefusesMarketDBAsOutput(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	db, _ := storage.OpenSQLite(p)
	db.Close()
	if _, err := Run(context.Background(), Options{MarketDB: p, DBOut: p}); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestDBHoldsOnlyRelevantDailyBars(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "market.db")
	db, err := storage.OpenSQLite(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, Date DATETIME, timeframe TEXT, asset_class TEXT,
		open FLOAT, high FLOAT, low FLOAT, close FLOAT, "Adj Close" FLOAT, volume BIGINT, symbol TEXT);
		CREATE UNIQUE INDEX idx_u ON backtest_start(symbol, Date, timeframe)`); err != nil {
		t.Fatal(err)
	}
	for _, sym := range []string{"AAA", "BBB"} {
		for i := 0; i < 3; i++ {
			if _, err := db.Exec(`INSERT INTO backtest_start (idx, Date, timeframe, close, volume, symbol) VALUES (?, ?, '1d', 5, 1, ?)`,
				i, fmt.Sprintf("2024-01-%02d", i+1), sym); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO backtest_start (idx, Date, timeframe, close, volume, symbol) VALUES (?, ?, '1h', 5, 1, ?)`,
				i, fmt.Sprintf("2024-01-%02d 10:00:00", i+1), sym); err != nil {
				t.Fatal(err)
			}
		}
	}
	db.Close()

	out := filepath.Join(dir, "out.db")
	if _, err := Run(context.Background(), Options{MarketDB: src, DBOut: out, Symbols: []string{"aaa"}}); err != nil {
		t.Fatal(err)
	}
	vdb, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer vdb.Close()
	var n, syms, hourly int
	if err := vdb.QueryRow(`SELECT count(*), count(DISTINCT symbol), sum(length(Date) > 10) FROM backtest_start`).Scan(&n, &syms, &hourly); err != nil {
		t.Fatal(err)
	}
	if n != 3 || syms != 1 || hourly != 0 {
		t.Fatalf("rows=%d symbols=%d hourly=%d, want 3/1/0", n, syms, hourly)
	}
	var idx int
	if err := vdb.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_u'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("index not copied: %d, %v", idx, err)
	}
	if err := vdb.QueryRow(`SELECT count(*) FROM v_bar_sma`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("v_bar_sma rows = %d, %v", n, err)
	}
	if _, err := Run(context.Background(), Options{MarketDB: src, DBOut: out, Symbols: []string{"A'; DROP"}}); err == nil {
		t.Fatal("expected a bad symbol to be refused")
	}
}
