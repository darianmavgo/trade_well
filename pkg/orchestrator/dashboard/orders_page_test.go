package dashboard

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

const ordersAMUU = `[
 {"orderId":1008284115171,"status":"FILLED","orderType":"LIMIT","price":271.76,"quantity":45,
  "enteredTime":"2026-10-14T01:10:39+0000","closeTime":"2026-10-14T13:30:13+0000",
  "orderLegCollection":[{"instruction":"BUY","quantity":45,"instrument":{"symbol":"AMUU"}}],
  "orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":256.69,"quantity":45}]}]},
 {"orderId":1008290001,"status":"FILLED","orderType":"STOP","stopPrice":217.41,"quantity":45,
  "enteredTime":"2026-10-14T13:31:00+0000","closeTime":"2026-10-15T13:30:04+0000",
  "orderLegCollection":[{"instruction":"SELL","quantity":45,"instrument":{"symbol":"AMUU"}}],
  "orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":214.10,"quantity":30},{"price":211.60,"quantity":15}]}]},
 {"orderId":1008285228891,"status":"REJECTED","orderType":"LIMIT","price":271.76,"quantity":45,
  "enteredTime":"2026-10-14T13:28:03+0000","closeTime":"2026-10-14T13:28:03+0000",
  "orderLegCollection":[{"instruction":"BUY","quantity":45,"instrument":{"symbol":"AMUU"}}]}
]`

// The page shows the limit and the stop that were sent beside the price actually executed, and a
// filled stop is flagged.
func TestOrdersPageShowsFillsBesideLimitAndStop(t *testing.T) {
	var o []schwab.Order
	if err := json.Unmarshal([]byte(ordersAMUU), &o); err != nil {
		t.Fatal(err)
	}
	rows := orders.Rows(map[string][]schwab.Order{"75124906": o}, orders.Options{Symbol: "AMUU"})
	var buf bytes.Buffer
	if err := ordersTmpl.Execute(&buf, newOrdersView(rows, nil, []string{"75124906"}, 7, orders.Options{Symbol: "AMUU"}, time.Now())); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	for _, want := range []string{"<title>orders</title>", "symbol AMUU", "...4906", "$271.76", "$217.41", "$256.69", "45 shares", "$213.27", "STOP FILLED", "REJECTED", " ET"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("a value was rejected by the template escaper")
	}
}

// With no Schwab credentials the page still renders and says why, rather than listing nothing
// silently; JSON has the same shape with an empty list, never null.
func TestOrdersPageReportsAFailedBrokerReadAndIsHTMLByDefault(t *testing.T) {
	t.Setenv("APP_FOLDER", "")
	_, h := checkHealthServer(t)
	w := getStatus(h, "/orders")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("default = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if body := w.Body.String(); !strings.Contains(body, "live read") || !strings.Contains(body, "Schwab could not be read") {
		t.Errorf("a failed broker read is not shown:\n%s", body)
	}
	j := getStatus(h, "/orders?format=json&symbol=AMUU&days=30")
	if !strings.HasPrefix(j.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("format=json content type %q", j.Header().Get("Content-Type"))
	}
	var got struct {
		Days    int      `json:"days"`
		Errors  []string `json:"errors"`
		Orders  []any    `json:"orders"`
		Account []string `json:"accounts_read"`
	}
	if err := json.Unmarshal(j.Body.Bytes(), &got); err != nil || got.Days != 30 || len(got.Errors) == 0 || got.Orders == nil {
		t.Errorf("json = %+v err %v", got, err)
	}
	for path, want := range map[string]int{"/orders?days=0": 400, "/orders?days=59": 400, "/orders?days=x": 400} {
		if w := getStatus(h, path); w.Code != want {
			t.Errorf("%s = %d, want %d", path, w.Code, want)
		}
	}
}
