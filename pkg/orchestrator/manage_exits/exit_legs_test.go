package manage_exits

import "testing"

// Two TSLL entries in one account: 1001 (09-24) exited on its own take-profit leg;
// 2002 (10-08) is filled with its bracket still resting. Schwab's bracket shape:
// TRIGGER parent -> OCO -> [take-profit, stop].
const twoTSLLBrackets = `[
 {"orderId":1001,"status":"FILLED","closeTime":"2026-09-24T13:30:00+0000","orderStrategyType":"TRIGGER",
  "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
  "childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
   {"orderId":1002,"status":"FILLED","orderType":"LIMIT","price":10.69,"closeTime":"2026-09-25T13:30:05+0000",
    "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
   {"orderId":1003,"status":"CANCELED","orderType":"STOP","stopPrice":8.2,
    "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]},
 {"orderId":2002,"status":"FILLED","closeTime":"2026-10-08T13:30:00+0000","orderStrategyType":"TRIGGER",
  "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
  "childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
   {"orderId":2003,"status":"WORKING","orderType":"LIMIT","price":10.66,
    "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
   {"orderId":2004,"status":"WORKING","orderType":"STOP","stopPrice":8.12,
    "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]}]`

// The old row's exit is its own filled leg; the new row must not see it, so the
// earlier trade's sale can never close or trigger a sale of today's share.
func TestExitLegsAreScopedToTheirEntryOrder(t *testing.T) {
	orders := mustOrders(t, twoTSLLBrackets)

	old := FilledExitLeg(ExitLegs(orders, "1001"))
	if old == nil || *old.OrderID != 1002 {
		t.Fatalf("old entry's filled leg = %+v, want order 1002", old)
	}
	newLegs := ExitLegs(orders, "2002")
	if leg := FilledExitLeg(newLegs); leg != nil {
		t.Fatalf("new entry has a filled exit leg %d it does not own", *leg.OrderID)
	}
	if got := workingExitLegID(newLegs); got != "2003" {
		t.Errorf("new entry's resting take-profit = %q, want 2003", got)
	}
	if got := ExitLegs(orders, ""); got != nil {
		t.Errorf("empty entry id matched legs: %v", got)
	}
	if got := ExitLegs(orders, "9999"); got != nil {
		t.Errorf("unknown entry id matched legs: %v", got)
	}
}

func TestCannotSell(t *testing.T) {
	orders := mustOrders(t, twoTSLLBrackets)
	tr := Trade{Symbol: "TSLL", EntryOrderID: "2002", Quantity: 1}
	if why := cannotSell(orders, tr, 1); why != "" {
		t.Errorf("filled entry with the share held: blocked (%s)", why)
	}
	if why := cannotSell(orders, tr, 0); why == "" {
		t.Error("sold with no shares held")
	}
	tr.Quantity = 2
	if why := cannotSell(orders, tr, 1); why == "" {
		t.Error("sold more shares than the broker holds")
	}
}
