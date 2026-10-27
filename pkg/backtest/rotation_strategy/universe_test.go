package rotation_strategy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// universeMarket writes a market database of 260 daily bars for AAA, BBB and QQQ, 60 for SHORT
// (too little history) and 260 for TEST1 (a numbered series, not a ticker).
func universeMarket(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "market.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2021, 1, 4, 0, 0, 0, 0, time.UTC)
	for sym, n := range map[string]int{"AAA": 260, "BBB": 260, "QQQ": 260, "SHORT": 60, "TEST1": 260} {
		for i := 0; i < n; i++ {
			c := 100 + float64(i)*0.1
			if sym == "BBB" {
				c = 50 + float64(i)*0.2 + float64(i%7)
			}
			if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume)
				VALUES (?, ?, '1d', ?, ?, ?, ?, 1000)`, sym, start.AddDate(0, 0, i).Format("2006-01-02"), c, c+1, c-1, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	return path
}

func TestUniverseStoresTheCandidatesAsASymbolList(t *testing.T) {
	market, ref := universeMarket(t), filepath.Join(t.TempDir(), "strategies.db")
	calc := filepath.Join(t.TempDir(), "calc.db")
	listOf := func() (string, bool) {
		db, err := refdb.Open(ref)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var list string
		err = db.Get(&list, `SELECT list FROM symbol_lists WHERE symbol_list_id = ?`, CandidatesListID)
		return list, err == nil
	}

	dry, err := Universe(context.Background(), UniverseConfig{MarketDB: market, RefDB: ref, CalcDB: calc, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Written || dry.Candidates != 3 || dry.ListID != CandidatesListID {
		t.Fatalf("dry run = %+v, want 3 candidates and nothing written", dry)
	}
	if _, ok := listOf(); ok {
		t.Fatal("a dry run wrote the symbol list")
	}
	if _, err := os.Stat(calc); err == nil {
		t.Fatal("the scratch database was left behind")
	}

	live, err := Universe(context.Background(), UniverseConfig{MarketDB: market, RefDB: ref, CalcDB: calc})
	if err != nil {
		t.Fatal(err)
	}
	if !live.Written {
		t.Fatalf("live run = %+v", live)
	}
	if list, ok := listOf(); !ok || list != "AAA,BBB,QQQ" {
		t.Fatalf("symbol list = %q, %v; want AAA,BBB,QQQ (no SHORT, no TEST1)", list, ok)
	}
	curves := map[string]bool{}
	for _, y := range live.Yearly {
		curves[y.Curve] = true
		if y.Sessions <= 0 {
			t.Fatalf("curve %s year %s has %d sessions", y.Curve, y.Year, y.Sessions)
		}
	}
	for _, want := range []string{"bench", "top3", "top10", "top10_gated", "qqq"} {
		if !curves[want] {
			t.Fatalf("no yearly return for curve %q: %v", want, curves)
		}
	}
}

func TestUniverseFailsWithoutAMarketDatabase(t *testing.T) {
	_, err := Universe(context.Background(), UniverseConfig{MarketDB: filepath.Join(t.TempDir(), "nope.db"), RefDB: filepath.Join(t.TempDir(), "r.db"), DryRun: true})
	if err == nil {
		t.Fatal("a missing market database must be an error")
	}
}
