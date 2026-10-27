package trigger_limits

import (
	"math"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
)

func order(sym string, limit float64) stage_orders.StagedOrder {
	return stage_orders.StagedOrder{Symbol: sym, EntryPrice: limit}
}

func TestDistance(t *testing.T) {
	if got := Distance(98, 100); math.Abs(got-0.02) > 1e-9 {
		t.Errorf("Distance(98,100) = %v, want 0.02", got)
	}
	if got := Distance(101, 100); got >= 0 {
		t.Errorf("a limit above the price is already reached, got %v", got)
	}
	if !math.IsInf(Distance(98, 0), 1) {
		t.Error("no price must be infinitely far")
	}
}

func TestQuotePricePrefersAsk(t *testing.T) {
	if p := (Quote{Ask: 10, Last: 9}).Price(); p != 10 {
		t.Errorf("ask present: %v", p)
	}
	if p := (Quote{Last: 9}).Price(); p != 9 {
		t.Errorf("pre-open with no ask falls back to last: %v", p)
	}
}

func TestRankByDistanceNearestFirstThenSymbol(t *testing.T) {
	orders := []stage_orders.StagedOrder{order("ZZZ", 98), order("AAA", 98), order("MID", 99), order("NOQ", 50)}
	quotes := map[string]Quote{
		"ZZZ": {Ask: 100}, "AAA": {Ask: 100}, "MID": {Ask: 100},
	}
	got := RankByDistance(orders, quotes)
	want := []string{"MID", "AAA", "ZZZ", "NOQ"}
	for i, w := range want {
		if got[i].Order.Symbol != w {
			t.Fatalf("rank %d = %s, want %s (all: %v)", i, got[i].Order.Symbol, w, got)
		}
	}
}

func TestPlanSwap(t *testing.T) {
	r := func(sym string, d float64) Ranked { return Ranked{Order: order(sym, 1), Distance: d} }
	resting := []Ranked{r("A", 0.01), r("B", 0.05), r("C", 0.02)}

	out, in, ok := PlanSwap(resting, []Ranked{r("N", 0.015), r("M", 0.03)})
	if !ok || out.Order.Symbol != "B" || in.Order.Symbol != "N" {
		t.Errorf("want B swapped for N, got ok=%v out=%s in=%s", ok, out.Order.Symbol, in.Order.Symbol)
	}
	// Nearer than the farthest resting order, but not by more than the hysteresis.
	if _, _, ok := PlanSwap(resting, []Ranked{r("N", 0.049)}); ok {
		t.Error("a gain inside the hysteresis must not swap")
	}
	if _, _, ok := PlanSwap(resting, []Ranked{r("N", math.Inf(1))}); ok {
		t.Error("a name with no price must never replace a resting order")
	}
	if _, _, ok := PlanSwap(nil, []Ranked{r("N", 0.001)}); ok {
		t.Error("nothing resting, nothing to swap")
	}
	if _, _, ok := PlanSwap(resting, nil); ok {
		t.Error("nothing watched, nothing to swap in")
	}
}

func TestIsBuyingPowerRefusal(t *testing.T) {
	yes := []string{
		"submission failed: API error: status 400, body: Insufficient buying power for this order",
		"insufficient buying power: order cost ($20000.00) exceeds available cash ($5000.00) in this Roth IRA",
	}
	no := []string{
		"API error: status 400, body: validation error",
		"symbol not found",
		"",
	}
	for _, s := range yes {
		if !IsBuyingPowerRefusal(s) {
			t.Errorf("should be a buying-power refusal: %q", s)
		}
	}
	for _, s := range no {
		if IsBuyingPowerRefusal(s) {
			t.Errorf("should not be a buying-power refusal: %q", s)
		}
	}
}

func TestRestingAndReopenStore(t *testing.T) {
	db, _ := setup(t)
	id := watch(t, db, "AAA", 98, session)
	if _, err := stage_orders.PromoteWatchingOrder(db, id, 5); err != nil {
		t.Fatal(err)
	}
	if err := stage_orders.MarkStagedOrderRejected(db, id, "Insufficient buying power"); err != nil {
		t.Fatal(err)
	}
	if ok, err := stage_orders.ReopenRejectedWatch(db, id); err != nil || !ok {
		t.Fatalf("reopen: ok=%v err=%v", ok, err)
	}
	got, _ := stage_orders.GetStagedOrder(db, id)
	if got.Status != stage_orders.StagedOrderWatching || got.RejectReason != "" {
		t.Errorf("reopened row = %s %q", got.Status, got.RejectReason)
	}

	if err := stage_orders.MarkStagedOrderPendingFill(db, id, "123", et(2026, 10, 8, 9, 20)); err != nil {
		t.Fatal(err)
	}
	rest, err := stage_orders.GetRestingDayEntries(db, "A1", session)
	if err != nil || len(rest) != 1 || rest[0].OrderID != "123" {
		t.Fatalf("resting = %v err=%v", rest, err)
	}
	if other, _ := stage_orders.GetRestingDayEntries(db, "A1", "2026-10-09"); len(other) != 0 {
		t.Error("a resting order belongs to its own session only")
	}
	if ok, err := stage_orders.CancelRestingOrder(db, id, "swapped"); err != nil || !ok {
		t.Fatalf("cancel resting: ok=%v err=%v", ok, err)
	}
	if rest, _ := stage_orders.GetRestingDayEntries(db, "A1", session); len(rest) != 0 {
		t.Error("a cancelled order is no longer resting")
	}
}
