package accountstore

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFeeAndSymbol(t *testing.T) {
	raw := `{"transferItems": [
		{"instrument": {"symbol": "TSLL"}, "amount": -10.30},
		{"feeType": "SEC_FEE", "amount": -0.01},
		{"feeType": "TAF_FEE", "amount": 0.02}
	]}`
	var tx map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &tx); err != nil {
		t.Fatal(err)
	}
	fees, symbol := FeeAndSymbol(tx)
	if symbol != "TSLL" || fees < 0.029 || fees > 0.031 {
		t.Fatalf("fees=%v symbol=%s", fees, symbol)
	}
}

func TestParseOrdersStoresExecutionPrice(t *testing.T) {
	db := openTest(t)
	s := New(Default)
	if err := s.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	body := []byte(`[{"orderId":55,"status":"FILLED","price":10.50,"orderLegCollection":[{"instruction":"BUY","instrument":{"symbol":"TSLL"}}],
		"orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":10.20,"quantity":1}]}]}]`)
	s.ParseOrders(db, "111", body, time.Now().UTC())
	var px float64
	if err := db.QueryRow(`SELECT execution_price FROM orders WHERE order_id = '55'`).Scan(&px); err != nil {
		t.Fatal(err)
	}
	if px != 10.20 {
		t.Fatalf("execution_price = %v", px)
	}
}
