package schwab

import (
	"encoding/json"
	"testing"
)

// The AMUU stop order of 2026-10-15 as Schwab lists it: a STOP sell whose stopPrice is the
// trigger, with the execution below it. The fill is read from the activity, not the stop price.
const filledStop = `{
	"orderId": 1008290001, "orderType": "STOP", "status": "FILLED", "stopPrice": 217.41, "quantity": 45,
	"closeTime": "2026-10-15T13:30:04+0000",
	"orderLegCollection": [{"instruction": "SELL", "instrument": {"symbol": "AMUU"}}],
	"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [
		{"price": 214.10, "quantity": 30}, {"price": 211.60, "quantity": 15}]}]
}`

func TestOrderKeepsItsExecutionsAndReportsTheFill(t *testing.T) {
	var o Order
	if err := json.Unmarshal([]byte(filledStop), &o); err != nil {
		t.Fatal(err)
	}
	if len(o.OrderActivityCollection) == 0 {
		t.Fatal("orderActivityCollection was dropped when decoding the order")
	}
	px, qty, ok := o.FillPrice("")
	want := (214.10*30 + 211.60*15) / 45
	if !ok || qty != 45 || px < want-1e-9 || px > want+1e-9 {
		t.Fatalf("fill = %v x %v ok %v, want %v x 45", px, qty, ok, want)
	}
	if *o.StopPrice != 217.41 || px == *o.StopPrice {
		t.Errorf("fill %v must not be the stop price %v", px, *o.StopPrice)
	}
}

func TestOrderWithNoExecutionHasNoFill(t *testing.T) {
	var o Order
	if err := json.Unmarshal([]byte(`{"orderId": 7, "orderType": "STOP", "status": "WORKING", "stopPrice": 217.41,
		"orderLegCollection": [{"instruction": "SELL"}]}`), &o); err != nil {
		t.Fatal(err)
	}
	if px, _, ok := o.FillPrice(""); ok {
		t.Fatalf("a working order reports a fill of %v", px)
	}
}

// A bracket's entry carries the stop and the take-profit below it; the entry's own fill is its own.
func TestFillPriceWalksChildOrders(t *testing.T) {
	var o Order
	if err := json.Unmarshal([]byte(`{"orderId": 1, "status": "FILLED",
		"orderLegCollection": [{"instruction": "BUY"}],
		"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [{"price": 256.69, "quantity": 45}]}],
		"childOrderStrategies": [`+filledStop+`]}`), &o); err != nil {
		t.Fatal(err)
	}
	if px, _, ok := o.FillPrice(""); !ok || px != 256.69 {
		t.Errorf("entry fill = %v %v", px, ok)
	}
	kids := FlattenOrders([]Order{o})
	if len(kids) != 2 {
		t.Fatalf("flattened = %d orders", len(kids))
	}
	if px, _, ok := kids[1].FillPrice(""); !ok || px > 214 || px < 213 {
		t.Errorf("child stop fill = %v %v, want about 213.4", px, ok)
	}
	if px, _, ok := o.FillPrice("SELL"); !ok || px > 214 {
		t.Errorf("sell fill through the entry = %v %v", px, ok)
	}
}
