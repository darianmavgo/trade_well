package orders

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

// AMUU as Schwab listed it on 2026-10-15: the entry (limit 271.76) filled at 256.69, and the stop
// sell (trigger 217.41) filled lower. Another account holds a bracket whose children still rest.
const amuuAccount = `[
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
  "orderLegCollection":[{"instruction":"BUY","quantity":45,"instrument":{"symbol":"AMUU"}}]},
 {"orderId":2001,"status":"FILLED","orderType":"LIMIT","price":10.30,"quantity":1,"orderStrategyType":"TRIGGER",
  "enteredTime":"2026-10-13T20:00:00+0000","closeTime":"2026-10-14T13:31:02+0000",
  "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
  "orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":10.21,"quantity":1}]}],
  "childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
    {"orderId":2002,"status":"WORKING","orderType":"LIMIT","price":10.82,"quantity":1,"enteredTime":"2026-10-14T13:31:03+0000",
     "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
    {"orderId":2003,"status":"WORKING","orderType":"STOP","stopPrice":8.24,"quantity":1,"enteredTime":"2026-10-14T13:31:03+0000",
     "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]}
]`

func live(t *testing.T) map[string][]schwab.Order {
	t.Helper()
	var o []schwab.Order
	if err := json.Unmarshal([]byte(amuuAccount), &o); err != nil {
		t.Fatal(err)
	}
	return map[string][]schwab.Order{"75124906": o}
}

func byID(rows []Row) map[string]Row {
	m := map[string]Row{}
	for _, r := range rows {
		m[r.OrderID] = r
	}
	return m
}

func TestRowsReportFillsApartFromLimitAndStop(t *testing.T) {
	rows := Rows(live(t), Options{Symbol: "amuu"})
	m := byID(rows)
	if len(rows) != 3 {
		t.Fatalf("AMUU orders = %d, want 3 (entry, stop, rejected)", len(rows))
	}
	entry := m["1008284115171"]
	if entry.Side != "BUY" || *entry.Limit != 271.76 || entry.Fill == nil || *entry.Fill != 256.69 || entry.FillQty != 45 {
		t.Errorf("entry = %+v: want limit 271.76 and fill 256.69", entry)
	}
	stop := m["1008290001"]
	wantFill := (214.10*30 + 211.60*15) / 45
	if stop.Side != "SELL" || stop.Type != "STOP" || *stop.Stop != 217.41 || stop.Fill == nil || *stop.Fill < wantFill-1e-9 || *stop.Fill > wantFill+1e-9 {
		t.Errorf("stop = %+v: want trigger 217.41, fill %.4f", stop, wantFill)
	}
	if rej := m["1008285228891"]; rej.Status != "REJECTED" || rej.Fill != nil {
		t.Errorf("rejected = %+v: an order that never executed has no fill", rej)
	}
	if rows[0].OrderID != "1008290001" {
		t.Errorf("newest first: got %s first", rows[0].OrderID)
	}
}

func TestRowsUnfoldBracketsAndTagTheirParent(t *testing.T) {
	m := byID(Rows(live(t), Options{Symbol: "TSLL"}))
	if len(m) != 3 {
		t.Fatalf("TSLL orders = %d, want the entry and its two children", len(m))
	}
	if tp := m["2002"]; tp.ParentID != "2001" || tp.Status != "WORKING" || *tp.Limit != 10.82 || tp.Fill != nil {
		t.Errorf("take-profit = %+v", tp)
	}
	if st := m["2003"]; st.ParentID != "2001" || *st.Stop != 8.24 {
		t.Errorf("stop = %+v", st)
	}
	if e := m["2001"]; *e.Fill != 10.21 || *e.Limit != 10.30 {
		t.Errorf("entry = %+v: fill 10.21 under limit 10.30", e)
	}
}

func TestRowsFilters(t *testing.T) {
	if n := len(Rows(live(t), Options{Status: "working"})); n != 2 {
		t.Errorf("WORKING = %d, want 2", n)
	}
	if n := len(Rows(live(t), Options{Since: time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)})); n != 4 {
		t.Errorf("since 10-14 13:00Z = %d, want 4 (the AMUU and TSLL entries were entered earlier)", n)
	}
	if n := len(Rows(live(t), Options{Symbol: "NOPE"})); n != 0 {
		t.Errorf("unknown symbol = %d rows", n)
	}
	if rows := Rows(nil, Options{}); len(rows) != 0 {
		t.Errorf("no accounts = %v", rows)
	}
}

func TestFormatLabelsLimitStopAndFill(t *testing.T) {
	var b bytes.Buffer
	Format(&b, Rows(live(t), Options{Symbol: "AMUU"}))
	out := b.String()
	for _, want := range []string{"LIMIT", "STOP", "FILL", "...4906", "271.76", "217.41", "256.69 x 45", "REJECTED"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}
