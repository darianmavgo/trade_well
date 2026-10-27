package account_state

import "testing"

// A filled entry with its OCO exits resting, a canceled order, and a working standalone
// limit buy. Only the live orders with legs are pending: the two exit legs and the buy.
const pendingFixture = `[
 {"orderId":1,"status":"FILLED","orderStrategyType":"TRIGGER","enteredTime":"2026-10-08T13:28:00+0000",
  "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
  "childOrderStrategies":[{"orderStrategyType":"OCO","status":"WORKING","childOrderStrategies":[
   {"orderId":2,"status":"WORKING","orderType":"LIMIT","price":10.66,"enteredTime":"2026-10-08T13:28:00+0000",
    "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
   {"orderId":3,"status":"WORKING","orderType":"STOP","stopPrice":8.12,
    "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]},
 {"orderId":4,"status":"CANCELED","orderType":"LIMIT","price":5,
  "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"MARA"}}]},
 {"orderId":5,"status":"QUEUED","orderType":"LIMIT","price":9.5,
  "orderLegCollection":[{"instruction":"BUY","quantity":2,"instrument":{"symbol":"PDD"}}]}]`

func TestAddPendingOrdersIncludesBracketChildrenOnly(t *testing.T) {
	tbl := newPendingOrdersTable()
	addPendingOrders(tbl, "25474194", []byte(pendingFixture))

	got := map[string][]string{}
	for _, r := range tbl.Rows {
		got[r[3]] = r
	}
	if len(tbl.Rows) != 3 || got["2"] == nil || got["3"] == nil || got["5"] == nil {
		t.Fatalf("pending rows = %v, want orders 2, 3, 5", tbl.Rows)
	}
	if r := got["2"]; r[1] != "TSLL" || r[2] != "SELL" || r[6] != "10.66" {
		t.Errorf("take-profit row = %v", r)
	}
	if r := got["3"]; r[7] != "8.12" {
		t.Errorf("stop row = %v", r)
	}
	if len(tbl.Columns) != len(tbl.Rows[0]) {
		t.Errorf("%d columns vs %d cells", len(tbl.Columns), len(tbl.Rows[0]))
	}
	addPendingOrders(tbl, "x", []byte("not json"))
}
