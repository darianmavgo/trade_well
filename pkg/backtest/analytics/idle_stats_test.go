package analytics

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

func TestCalculateIdleStats_Empty(t *testing.T) {
	stats := CalculateIdleStats(nil)
	if stats.TradingDays != 0 || stats.AvgCashPct != 0 {
		t.Fatalf("empty curve: got %+v", stats)
	}
}

func TestCalculateIdleStats_Mixed(t *testing.T) {
	curve := []models.DailyEquityPoint{
		{Date: "2026-01-01", Cash: 100000, PositionsValue: 0, TotalEquity: 100000, OpenPositions: 0},
		{Date: "2026-01-02", Cash: 35000, PositionsValue: 65000, TotalEquity: 100000, OpenPositions: 1},
		{Date: "2026-01-03", Cash: 35000, PositionsValue: 65000, TotalEquity: 100000, OpenPositions: 1},
		{Date: "2026-01-04", Cash: 100000, PositionsValue: 0, TotalEquity: 100000, OpenPositions: 0},
	}
	stats := CalculateIdleStats(curve)
	if stats.TradingDays != 4 {
		t.Errorf("TradingDays = %d, want 4", stats.TradingDays)
	}
	if stats.DaysFullyIdle != 2 {
		t.Errorf("DaysFullyIdle = %d, want 2", stats.DaysFullyIdle)
	}
	if stats.DaysDeployed != 2 {
		t.Errorf("DaysDeployed = %d, want 2", stats.DaysDeployed)
	}
	if stats.FullyIdlePct != 0.5 {
		t.Errorf("FullyIdlePct = %v, want 0.5", stats.FullyIdlePct)
	}
	wantAvgCash := (1.0 + 0.35 + 0.35 + 1.0) / 4.0
	if stats.AvgCashPct < wantAvgCash-1e-9 || stats.AvgCashPct > wantAvgCash+1e-9 {
		t.Errorf("AvgCashPct = %v, want %v", stats.AvgCashPct, wantAvgCash)
	}
	if stats.MinCashPct != 0.35 {
		t.Errorf("MinCashPct = %v, want 0.35", stats.MinCashPct)
	}
	if stats.MaxCashPct != 1.0 {
		t.Errorf("MaxCashPct = %v, want 1.0", stats.MaxCashPct)
	}
}
