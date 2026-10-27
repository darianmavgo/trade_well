package gridsearch

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

func res(limit float64, hold int, cagr, dd float64) gridResult {
	return gridResult{Period: "1w", EntryLimit: limit, HoldDays: hold, TakeProfit: 0.05,
		Report: models.PerformanceReport{CAGR: cagr, MaxDrawdownPct: dd, MaxDrawdownDuration: 10, TotalTrades: 40}}
}

// A lone spike next to weak neighbours loses to a plateau of good settings.
func TestSmoothedBestPrefersPlateau(t *testing.T) {
	results := []gridResult{
		res(0.90, 2, 0.30, 0.10), // spike
		res(0.95, 2, 0.01, 0.10),
		res(0.97, 2, 0.20, 0.10), // plateau
		res(1.00, 2, 0.21, 0.10),
	}
	axes := map[string][]float64{"limit": {0.90, 0.95, 0.97, 1.00}, "tp": {0.05}, "sl": {0}}
	best, ok := smoothedBest(results, axes, []int{2})
	if !ok || best.EntryLimit < 0.97 {
		t.Fatalf("picked limit %v, want the 97-100%% plateau", best.EntryLimit)
	}
}

func TestPassesNeedsHeldOutEvidence(t *testing.T) {
	good := res(1, 2, 0.1, 0.1)
	good.Report.TotalTrades = 4
	if !(symbolOutcome{OOS: &good, MinOOS: 4}).passes() {
		t.Error("profitable held-out result with enough trades should pass")
	}
	good.Report.TotalTrades = 3
	if (symbolOutcome{OOS: &good, MinOOS: 4}).passes() {
		t.Error("too few held-out trades should not pass")
	}
}

func TestTradesFor(t *testing.T) {
	for years, want := range map[float64]int{1: 4, 4.77: 20, 0.25: 1} {
		if got := tradesFor(years); got != want {
			t.Errorf("tradesFor(%v) = %d, want %d", years, got, want)
		}
	}
}
