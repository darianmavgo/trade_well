package runner

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// A stack run stores a summary row per sleeve under the sleeve's own id. Those
// rows must not stand in for the sleeve's standalone result.
func TestScanKeepsStandaloneResultWhenAStackRunRepeatsTheID(t *testing.T) {
	dir := t.TempDir()
	results, err := storage.OpenResults(dir, "builtin")
	if err != nil {
		t.Fatal(err)
	}
	defer results.Close()

	flat := models.DailyEquityPoint{Date: "2024-01-02", Cash: 100, PositionsValue: 0, TotalEquity: 100}
	held := models.DailyEquityPoint{Date: "2024-01-03", Cash: 40, PositionsValue: 60, TotalEquity: 100}
	if _, err := results.WriteRun(storage.RunMeta{StrategyID: "sig-x", Kind: "single"}, storage.RunPayload{
		Equity:  []models.DailyEquityPoint{{Date: "2024-01-01", Cash: 100, TotalEquity: 100}, flat, held},
		Reports: []storage.NamedReport{{StrategyID: "sig-x", Report: models.PerformanceReport{CAGR: 0.10, TotalTrades: 3}}},
	}); err != nil {
		t.Fatal(err)
	}
	// Stack run: its own row, plus a sleeve row for sig-x with a different CAGR.
	if _, err := results.WriteRun(storage.RunMeta{StrategyID: "sig-x+other", Kind: "stack"}, storage.RunPayload{
		Equity: []models.DailyEquityPoint{flat, held},
		Reports: []storage.NamedReport{
			{StrategyID: "sig-x+other", Report: models.PerformanceReport{CAGR: 0.50}},
			{StrategyID: "sig-x", Report: models.PerformanceReport{CAGR: 0.90}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	by, files, groups, _, bad := ScanAndValidate(dir, 1)
	if files != 1 || bad != 0 {
		t.Fatalf("files=%d compromised=%d", files, bad)
	}
	got, ok := by["sig-x"]
	if !ok {
		t.Fatal("sig-x missing")
	}
	if got.Report.CAGR != 0.10 {
		t.Fatalf("cagr %v, want standalone 0.10 (a stack's sleeve row must not replace it)", got.Report.CAGR)
	}
	if !got.Report.IdleKnown || got.Report.IdleDays != 2 {
		t.Fatalf("idle days %d known %v, want 2 from the standalone curve", got.Report.IdleDays, got.Report.IdleKnown)
	}
	if _, ok := by["sig-x+other"]; !ok || groups != 2 {
		t.Fatalf("the stack run is its own strategy id; groups=%d", groups)
	}
}

func TestScanReturnsNewestRunPerStrategy(t *testing.T) {
	dir := t.TempDir()
	results, err := storage.OpenResults(dir, "builtin")
	if err != nil {
		t.Fatal(err)
	}
	defer results.Close()
	for _, cagr := range []float64{0.1, 0.2, 0.3} {
		if _, err := results.WriteRun(storage.RunMeta{StrategyID: "a"}, storage.RunPayload{
			Reports: []storage.NamedReport{{StrategyID: "a", Report: models.PerformanceReport{CAGR: cagr}}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	by, _, _, _, _ := ScanAndValidate(dir, 1)
	if got := by["a"]; got.Report.CAGR != 0.3 || got.Increment != 3 {
		t.Fatalf("got cagr %v run %d, want the third run", got.Report.CAGR, got.Increment)
	}
}

func TestScanWithNoResultsFile(t *testing.T) {
	by, files, _, _, _ := ScanAndValidate(t.TempDir(), 1)
	if len(by) != 0 || files != 0 {
		t.Fatalf("empty dir: %d results, %d files", len(by), files)
	}
}
