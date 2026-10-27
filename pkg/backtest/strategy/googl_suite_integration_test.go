package strategy_test

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// The real GOOGL strategies run on real GOOGL bars (2021-01-01 to 2025-12-31).
func TestGooglStrategiesOnRealBars(t *testing.T) {
	refdb.DefaultPath = realbars.StrategiesDB(t)
	stratreg.RegisterFamilies()
	market := realbars.Copy(t, "GOOGL", "VOO")
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bars, _, err := storage.FetchBars(db, "backtest_start", []string{"GOOGL", "VOO"}, "2021-01-01", "2025-12-31")
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	for _, id := range []string{"googl-buy-hold", "streak-googl-down3-googl"} {
		s, ok := strategy.Get(id)
		if !ok {
			t.Fatalf("strategy %q not found", id)
		}
		s.SetDatabases(market, filepath.Join(t.TempDir(), id+".db"))
		counts[id] = len(s.GenerateSignals(bars))
		t.Logf("%s: %d signals", id, counts[id])
	}
	if counts["googl-buy-hold"] != 1 {
		t.Errorf("googl-buy-hold: %d signals, want 1", counts["googl-buy-hold"])
	}
}
