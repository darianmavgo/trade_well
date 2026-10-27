package trigger_limits

import (
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
)

// failed ends the placing at MaxFailures refused or failed orders, and never when
// MaxFailures is zero (the cron behaviour).
func TestFailedStopsAtMaxFailures(t *testing.T) {
	o := stage_orders.StagedOrder{Symbol: "SSO"}
	acc := &trader.ResolvedAccount{AccountNumber: "1"}
	s := &seatState{acc: acc, res: &SeatResult{}, opts: Options{MaxFailures: 2}}
	s.failed(o, "REJECTED", "first")
	if s.res.Stopped || s.res.Rejected != 1 {
		t.Fatalf("after one failure: stopped=%v rejected=%d", s.res.Stopped, s.res.Rejected)
	}
	s.failed(o, "BUYING_POWER_FULL", "second")
	if !s.res.Stopped || s.res.Rejected != 2 {
		t.Fatalf("after two failures: stopped=%v rejected=%d", s.res.Stopped, s.res.Rejected)
	}

	c := &seatState{acc: acc, res: &SeatResult{}}
	for i := 0; i < 5; i++ {
		c.failed(o, "REJECTED", "x")
	}
	if c.res.Stopped {
		t.Fatal("MaxFailures 0 must never stop the placing")
	}
}
