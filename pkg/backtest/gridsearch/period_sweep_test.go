package gridsearch

import (
	"math"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// One weekly entry on Tuesday (the bar before closes at 100) and its Friday exit.
func periodCtx() *sweepContext {
	dates := []string{"2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05", "2026-03-06"}
	rows := [][4]float64{{100, 100, 100, 100}, {92, 93, 88, 93}, {93, 101, 92, 100}, {100, 100, 99, 99}, {99, 99, 98, 98}}
	var bars []models.Bar
	for i, r := range rows {
		bars = append(bars, models.Bar{Symbol: "AAA", Date: dates[i], Open: r[0], High: r[1], Low: r[2], Close: r[3], Volume: 1})
	}
	return &sweepContext{
		PeriodSignals: map[string][]models.Signal{"1w": {
			{Symbol: "AAA", Date: "2026-03-03", Close: 93, Entry: 1, Direction: "LONG"},
			{Symbol: "AAA", Date: "2026-03-06", Close: 98, Entry: -1, Direction: "LONG"},
		}},
		PeriodConfigs: map[string]strategy.StrategyConfig{"1w": {
			AllocationPct: 1, PositionCap: 1, HoldingWindow: 99999, TargetPct: 999, PositionSizing: "fixed_pct",
		}},
		AllBars: map[string][]models.Bar{"AAA": bars}, SortedDates: dates,
	}
}

func TestPeriodTaskPricesTheLimitAndTakeProfitFromTheFill(t *testing.T) {
	task := sweepTask{period: "1w", limit: 0.9, tp: 0.05, hold: 0, alloc: 1}
	res, ok := evalPeriodTask(periodCtx(), task, sweepOptions{Capital: 100000}, true)
	if !ok || len(res.Trades) != 1 {
		t.Fatalf("want one trade, got ok=%v %+v", ok, res.Trades)
	}
	tr := res.Trades[0]
	if tr.EntryDate != "2026-03-03" || math.Abs(tr.EntryPrice-90) > 1 {
		t.Fatalf("entry %+v: want a fill near the 90 limit on 03-03", tr)
	}
	if math.Abs(tr.TargetPrice-tr.EntryPrice*1.05) > 1e-9 {
		t.Fatalf("target %v is not 5%% over the booked entry %v", tr.TargetPrice, tr.EntryPrice)
	}
	if tr.ExitDate != "2026-03-04" || tr.ExitReason != models.ExitReasonProfitTarget {
		t.Fatalf("want the 94.5 target taken on 03-04 (high 101), got %+v", tr)
	}
}

func TestPeriodTaskSkipsTheWeekWhenTheLimitIsNeverReached(t *testing.T) {
	task := sweepTask{period: "1w", limit: 0.85, tp: 0.05, alloc: 1} // the low is 88, above 85
	if res, ok := evalPeriodTask(periodCtx(), task, sweepOptions{Capital: 100000}, true); ok && len(res.Trades) > 0 {
		t.Fatalf("an unmet limit must not trade: %+v", res.Trades)
	}
}

func TestPeriodTaskHoldWindowExitsAtTheClose(t *testing.T) {
	task := sweepTask{period: "1w", limit: 1.0, tp: 0.5, hold: 1, alloc: 1} // buy at 100, never hit +50%
	res, ok := evalPeriodTask(periodCtx(), task, sweepOptions{Capital: 100000}, true)
	if !ok || len(res.Trades) != 1 {
		t.Fatalf("want one trade, got ok=%v %+v", ok, res.Trades)
	}
	if tr := res.Trades[0]; tr.ExitReason != models.ExitReasonTimeUp || tr.HoldDays != 1 {
		t.Fatalf("a 1 day hold exits on the next close, got %+v", tr)
	}
}
