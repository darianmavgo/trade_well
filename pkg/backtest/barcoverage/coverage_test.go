package barcoverage

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// hourly writes one regular-session hourly bar per (symbol, day); January is
// EST, so 14:00 UTC is the 9:00 bar.
func hourly(t *testing.T, path string, rows [][2]string) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume) VALUES (?, ?, '1h', 1, 1, 1, 1, 1)`, r[0], r[1]+" 14:00:00"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sessions.Derive(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageCountsSessionsAnyOfTheSymbolsTraded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "market_history_hourly.db")
	days := []string{"2021-01-04", "2021-01-05", "2021-01-06", "2021-01-07", "2021-01-08"}
	var rows [][2]string
	for _, d := range days {
		rows = append(rows, [2]string{"FULL", d})
	}
	// GAPPY misses the 5th and 6th; LATE lists on the 6th.
	for _, d := range []string{"2021-01-04", "2021-01-07", "2021-01-08"} {
		rows = append(rows, [2]string{"GAPPY", d})
	}
	for _, d := range days[2:] {
		rows = append(rows, [2]string{"LATE", d})
	}
	hourly(t, path, rows)

	got, err := Coverage(CoverageOptions{Symbols: []string{"FULL", "GAPPY", "LATE", "NONE"}, DB: path, Start: "2021-01-01"})
	if err != nil {
		t.Fatal(err)
	}
	g, l, n := got[1], got[2], got[3]
	if g.Expected != 5 || g.Have != 3 || g.Missing != 2 || g.LongestGap != 2 || g.BarsPerDay != 1 {
		t.Errorf("GAPPY = %+v", g)
	}
	// LATE is only expected from its first session.
	if l.Expected != 3 || l.Missing != 0 || l.Pct() != 100 {
		t.Errorf("LATE = %+v", l)
	}
	if n.Note != "no sessions in window" {
		t.Errorf("NONE = %+v", n)
	}
}
