package runner

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func TestSplitByDataSeparatesStrategiesWithoutBars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	for _, sym := range []string{"SPY", "TECL"} {
		if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe) VALUES (?, '2026-01-02', '1d')`, sym); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	strats := []strategy.Strategy{strategy.NewPark("TECL"), strategy.NewPark("ZZZ-PA"), strategy.NewPark("SPY")}
	have, lacking, missing, err := SplitByData(path, "backtest_start", strats, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(have) != 2 || have[0].ID() != "park-tecl" || have[1].ID() != "park-spy" {
		t.Fatalf("have = %v", have)
	}
	if len(lacking) != 1 || lacking[0].ID() != "park-zzz-pa" {
		t.Fatalf("lacking = %v", lacking)
	}
	if len(missing) != 1 || missing[0] != "ZZZ-PA" {
		t.Fatalf("missing = %v", missing)
	}
}
