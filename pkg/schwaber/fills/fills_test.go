package fills

import (
	"encoding/json"
	"testing"
)

func TestFillPriceWalksBracketChildren(t *testing.T) {
	raw := `{
		"orderId": 1001,
		"status": "FILLED",
		"orderLegCollection": [{"instruction": "BUY", "instrument": {"symbol": "TSLL"}}],
		"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [
			{"price": 10.25, "quantity": 1},
			{"price": 10.35, "quantity": 1}
		]}],
		"childOrderStrategies": [{
			"orderId": 1002,
			"status": "FILLED",
			"orderLegCollection": [{"instruction": "SELL", "instrument": {"symbol": "TSLL"}}],
			"orderActivityCollection": [{"activityType": "EXECUTION", "executionLegs": [
				{"price": 10.80, "quantity": 2}
			]}]
		}]
	}`
	var order map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &order); err != nil {
		t.Fatal(err)
	}
	buy, qty, ok := FillPrice(order, "BUY")
	if !ok || qty != 2 || buy != 10.30 {
		t.Fatalf("buy fill = %v qty %v ok %v, want 10.30 x 2", buy, qty, ok)
	}
	sell, _, ok := FillPrice(order, "SELL")
	if !ok || sell != 10.80 {
		t.Fatalf("sell fill = %v ok %v", sell, ok)
	}
	own, _, ok := FillPrice(order, "")
	if !ok || own != 10.30 {
		t.Fatalf("own fill = %v (children must not be mixed in)", own)
	}
}
