package runner

import (
	"fmt"
	"sync"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/hold_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

// TopDetail keeps the trades and curve of the k best strategies by CAGR and
// hands back slim results, from many workers at once.
func TestTopDetailKeepsTheBestK(t *testing.T) {
	top := NewTopDetail(3)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := &hold_strategy.Strategy{Row: refdb.HoldStrategy{ID: fmt.Sprintf("h%02d", i), Name: "x", Symbol: "VOO", AllocationPct: 1}}
			slim := top.Offer(RunResult{
				Strat: s, Report: models.PerformanceReport{CAGR: float64(i) / 100},
				Trades: []models.Trade{{Symbol: "VOO"}}, EquityCurve: []models.DailyEquityPoint{{Date: "2024-01-02"}},
			})
			if slim.Trades != nil || slim.EquityCurve != nil || slim.Report.CAGR != float64(i)/100 {
				t.Errorf("result %d was not returned slim with its report: %+v", i, slim)
			}
		}(i)
	}
	wg.Wait()
	for _, id := range []string{"h49", "h48", "h47"} {
		if r, ok := top.Detail(id); !ok || len(r.Trades) != 1 || len(r.EquityCurve) != 1 {
			t.Errorf("%s should keep its detail: %v %+v", id, ok, r)
		}
	}
	if _, ok := top.Detail("h46"); ok {
		t.Error("h46 is not in the best 3")
	}
	if len(top.kept) != 3 {
		t.Errorf("kept %d results, want 3", len(top.kept))
	}
}
