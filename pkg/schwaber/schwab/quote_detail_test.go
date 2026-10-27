package schwab

import (
	"encoding/json"
	"testing"
)

func TestQuoteDetailReadsThePriceFields(t *testing.T) {
	var resp map[string]Quote
	body := `{"ROM":{"assetMainType":"EQUITY","realtime":true,"quote":{"lastPrice":95.1,"bidPrice":95.0,"askPrice":95.2,"lowPrice":94.5,"highPrice":99.0,"openPrice":98.0,"closePrice":100.0,"totalVolume":1000}}}`
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	d, err := resp["ROM"].Detail()
	if err != nil {
		t.Fatal(err)
	}
	if d.LowPrice != 94.5 || d.AskPrice != 95.2 || d.ClosePrice != 100 || d.LastPrice != 95.1 {
		t.Fatalf("detail %+v", d)
	}
}
