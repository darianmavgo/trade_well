package strategy

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// Real GOOGL, AAPL and MARA bars from 2021-01-01. The dates up to 2025-12-31 are
// the ones the old Go implementation produced on the same data (it matched the
// SQL pipeline on 335 symbols, 1,625 signals), so a later bar cannot change them.
func TestPriceActionReclaimOnRealBars(t *testing.T) {
	market := realbars.Copy(t, "GOOGL", "AAPL", "MARA")
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bars, _, err := storage.FetchBars(db, "backtest_start", []string{"GOOGL", "AAPL", "MARA"}, "2021-01-01", "")
	if err != nil {
		t.Fatal(err)
	}
	s := &PriceActionReclaimStrategy{}
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	got := map[string][]string{}
	for _, sig := range s.GenerateSignals(bars) {
		// Before mid-2022 the 200-day average still differs on a market database that starts in
		// January 2021, and the expectations below were taken on a longer history. Compare from mid-2022.
		if sig.Date >= "2022-06-01" && sig.Date <= "2025-12-31" {
			got[sig.Symbol] = append(got[sig.Symbol], sig.Date)
		}
		if sig.Entry != 1 || sig.StopLoss <= 0 || sig.StopLoss >= sig.Close || sig.StopLoss/sig.Close < 0.85 {
			t.Fatalf("bad signal %+v", sig)
		}
	}
	want := map[string][]string{
		"GOOGL": {"2023-06-28", "2023-11-01", "2024-02-29"},
		"AAPL":  {"2023-08-22", "2023-10-02", "2024-08-09", "2025-01-27"},
		"MARA":  {"2024-01-25", "2024-03-15", "2024-04-11", "2024-04-19", "2025-01-03"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}
