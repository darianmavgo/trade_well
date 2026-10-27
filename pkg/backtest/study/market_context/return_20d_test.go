package market_context

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/study"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func TestReturn20dMatchesLagFormula(t *testing.T) {
	if _, ok := study.Get("market_context_20d"); !ok {
		t.Fatal("market_context_20d is not registered")
	}

	dir := t.TempDir()
	marketPath := filepath.Join(dir, "market.db")
	resultsPath := filepath.Join(dir, "return_20d.db")
	db, err := sqlx.Open("sqlite", marketPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE backtest_start (
		Date TEXT, timeframe TEXT, open REAL, high REAL, low REAL, close REAL, volume INTEGER, symbol TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		for _, sym := range Symbols {
			closePx := 100.0 + float64(i)
			if _, err := db.Exec(`INSERT INTO backtest_start (Date, timeframe, open, high, low, close, volume, symbol)
				VALUES (?, '1d', ?, ?, ?, ?, 1, ?)`, date, closePx, closePx, closePx, closePx, sym); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Intraday and a sixth symbol must not enter the 20-session window.
	if _, err := db.Exec(`INSERT INTO backtest_start (Date, timeframe, close, symbol) VALUES
		('2024-01-01T09:30', '1m', 1, 'VOO'),
		('2024-01-15', '1d', 999, 'SPY')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s := &Return20d{}
	s.SetDatabases(marketPath, resultsPath)
	if err := s.Run(); err != nil {
		t.Fatal(err)
	}

	out, err := sqlx.Open("sqlite", resultsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	var nSpy int
	if err := out.Get(&nSpy, `SELECT COUNT(*) FROM return_20d WHERE ticker = 'SPY'`); err != nil {
		t.Fatal(err)
	}
	if nSpy != 0 {
		t.Fatalf("SPY rows = %d, want 0", nSpy)
	}

	day := start.AddDate(0, 0, 20).Format("2006-01-02") // 21st session; lag is the first close
	var simple, logRet float64
	if err := out.QueryRow(`SELECT return_20d_simple, return_20d_log FROM return_20d WHERE ticker = 'VOO' AND date = ?`, day).Scan(&simple, &logRet); err != nil {
		t.Fatal(err)
	}
	wantSimple := (120.0 - 100.0) / 100.0
	wantLog := math.Log(120.0 / 100.0)
	if math.Abs(simple-wantSimple) > 1e-9 || math.Abs(logRet-wantLog) > 1e-9 {
		t.Fatalf("VOO %s simple=%v log=%v, want %v and %v", day, simple, logRet, wantSimple, wantLog)
	}

	var early interface{}
	if err := out.Get(&early, `SELECT return_20d_simple FROM return_20d WHERE ticker = 'IEF' AND date = ?`, start.Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if early != nil {
		t.Fatalf("first session should have a null 20d return, got %v", early)
	}
}
