package schwab

import (
	"encoding/json"
	"testing"
)

const bracket = `[{"orderId":1,"status":"FILLED","childOrderStrategies":[{"orderId":2,"status":"WORKING","childOrderStrategies":[
 {"orderId":3,"status":"WORKING"},{"orderId":4,"status":"CANCELED"}]}]},{"orderId":5,"status":"FILLED"}]`

func TestFlattenOrdersIsDepthFirstParentFirst(t *testing.T) {
	var orders []Order
	if err := json.Unmarshal([]byte(bracket), &orders); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, o := range FlattenOrders(orders) {
		ids = append(ids, *o.OrderID)
	}
	want := []int64{1, 2, 3, 4, 5}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
	if kids := (Order{}).Children(); kids != nil {
		t.Errorf("order with no children: %v", kids)
	}
}

func TestIsWorkingStatus(t *testing.T) {
	for _, s := range []string{"WORKING", "queued", "AWAITING_PARENT_ORDER", "PENDING_ACTIVATION"} {
		if !IsWorkingStatus(s) {
			t.Errorf("%s should be working", s)
		}
	}
	for _, s := range []string{"FILLED", "CANCELED", "REJECTED", "EXPIRED", ""} {
		if IsWorkingStatus(s) {
			t.Errorf("%s should not be working", s)
		}
	}
}
