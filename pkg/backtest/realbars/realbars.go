// Package realbars gives tests real market data. Copy writes the daily bars of
// the named symbols from the real market database (data/market_history_hourly.db) into
// a temp database, so a test runs on actual prices and never writes to the real
// file. The test is skipped, with the reason, when that database is absent.
package realbars

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// marketPath finds data/market_history_hourly.db by walking up from the test's
// working directory to the module root.
func marketPath(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			p := filepath.Join(dir, "data", "market_history_hourly.db")
			if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
				t.Skipf("real market data %s not present", p)
			}
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("no go.mod above the test directory")
		}
		dir = parent
	}
}

// StrategiesDB returns the path of the real refdata/strategies.db, or skips.
func StrategiesDB(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			p := filepath.Join(dir, "refdata", "strategies.db")
			if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
				t.Skipf("real strategies database %s not present", p)
			}
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("no go.mod above the test directory")
		}
		dir = parent
	}
}

// MinSessions is the history Copy requires of each symbol it copies.
const MinSessions = 1000

// Copy returns the path of a new market database whose backtest_start holds
// every daily (timeframe 1d) row of symbols, copied unchanged.
func Copy(t testing.TB, symbols ...string) string {
	t.Helper()
	real := marketPath(t)
	out := filepath.Join(t.TempDir(), "market.db")
	db, err := sqlx.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // ATTACH is per connection
	if _, err := db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS real", strings.ReplaceAll(real, "'", "''"))); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(symbols))
	for i, s := range symbols {
		quoted[i] = "'" + strings.ReplaceAll(strings.ToUpper(s), "'", "") + "'"
	}
	if _, err := db.Exec(`CREATE TABLE backtest_start AS SELECT * FROM real.backtest_start
		WHERE timeframe = '1d' AND symbol IN (` + strings.Join(quoted, ",") + `)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM backtest_start"); err != nil || n == 0 {
		t.Skipf("real market data has no daily bars for %v", symbols)
	}
	// Tests on real bars were written for years of history. Sessions are derived
	// from the hourly bars, which most symbols hold for two years only.
	for _, s := range symbols {
		var have int
		if err := db.Get(&have, "SELECT COUNT(*) FROM backtest_start WHERE symbol = ?", strings.ToUpper(s)); err != nil {
			t.Fatal(err)
		}
		if have < MinSessions {
			t.Skipf("real market data holds %d sessions of %s, the test needs %d", have, strings.ToUpper(s), MinSessions)
		}
	}
	if _, err := db.Exec("DETACH DATABASE real"); err != nil {
		t.Fatal(err)
	}
	return out
}

// Closes returns the real daily closes of symbol in date order, read from a
// database made by Copy.
func Closes(t testing.TB, marketDB, symbol string) []float64 {
	t.Helper()
	db, err := sqlx.Open("sqlite", marketDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var out []float64
	if err := db.Select(&out, "SELECT close FROM backtest_start WHERE symbol = ? ORDER BY Date", symbol); err != nil {
		t.Fatal(err)
	}
	return out
}

// Dates returns the real dates (YYYY-MM-DD) of symbol in order.
func Dates(t testing.TB, marketDB, symbol string) []string {
	t.Helper()
	db, err := sqlx.Open("sqlite", marketDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var out []string
	if err := db.Select(&out, "SELECT substr(Date, 1, 10) FROM backtest_start WHERE symbol = ? ORDER BY Date", symbol); err != nil {
		t.Fatal(err)
	}
	return out
}

// ShortSymbol returns a real symbol with between min and max daily bars (a
// recent listing), or skips the test if the market database has none.
func ShortSymbol(t testing.TB, min, max int) string {
	t.Helper()
	db, err := sqlx.Open("sqlite", marketPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sym string
	err = db.Get(&sym, `SELECT symbol FROM backtest_start WHERE timeframe = '1d'
		GROUP BY symbol HAVING COUNT(*) BETWEEN ? AND ? ORDER BY symbol LIMIT 1`, min, max)
	if err != nil {
		t.Skipf("no real symbol with %d-%d daily bars", min, max)
	}
	return sym
}
