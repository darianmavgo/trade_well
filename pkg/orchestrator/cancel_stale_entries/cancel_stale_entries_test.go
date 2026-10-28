package cancel_stale_entries

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/schemas"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

var now = time.Date(2026, 10, 28, 13, 0, 0, 0, time.UTC)

func order(id int64, status, instruction string, age time.Duration) schwab.Order {
	o := schwab.Order{OrderID: &id, Status: status}
	if instruction != "" {
		o.OrderLegCollection = json.RawMessage(`[{"instruction":"` + instruction + `","quantity":1,"instrument":{"symbol":"AAA"}}]`)
	}
	if age != 0 {
		o.EnteredTime = &schwab.FlexTime{Time: now.Add(-age)}
	}
	return o
}

func ids(orders []schwab.Order) []int64 {
	var out []int64
	for _, o := range orders {
		out = append(out, *o.OrderID)
	}
	return out
}

func TestStaleEntries(t *testing.T) {
	// A filled entry's working exit is a child of the top-level order, not a top-level order.
	filled := order(7, "FILLED", "BUY", 48*time.Hour)
	filled.ChildOrderStrategies = json.RawMessage(`[{"orderId":70,"status":"WORKING","orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"AAA"}}],"enteredTime":"2026-10-20T14:00:00+0000"}]`)
	// A working entry with its not-yet-active exit legs: cancelled as one order.
	bracket := order(8, "WORKING", "BUY", 30*time.Hour)
	bracket.ChildOrderStrategies = json.RawMessage(`[{"orderId":80,"status":"AWAITING_PARENT_ORDER"}]`)

	cases := []struct {
		name   string
		orders []schwab.Order
		want   []int64
	}{
		{"older than 24h", []schwab.Order{order(1, "WORKING", "BUY", 25*time.Hour)}, []int64{1}},
		{"under 24h stays", []schwab.Order{order(2, "WORKING", "BUY", 23*time.Hour)}, nil},
		{"exactly 24h stays", []schwab.Order{order(3, "WORKING", "BUY", 24*time.Hour)}, nil},
		{"sell is not an entry", []schwab.Order{order(4, "WORKING", "SELL", 72*time.Hour)}, nil},
		{"OCO parent without legs is not an entry", []schwab.Order{order(5, "WORKING", "", 72*time.Hour)}, nil},
		{"buy to cover is not an entry", []schwab.Order{order(6, "WORKING", "BUY_TO_COVER", 72*time.Hour)}, nil},
		{"filled entry and its working exit stay", []schwab.Order{filled}, nil},
		{"working entry with legs is one order", []schwab.Order{bracket}, []int64{8}},
		{"canceled and expired stay", []schwab.Order{order(9, "CANCELED", "BUY", 72*time.Hour), order(10, "EXPIRED", "BUY", 72*time.Hour)}, nil},
		{"unknown entered time stays", []schwab.Order{order(11, "WORKING", "BUY", 0)}, nil},
		{"mixed", []schwab.Order{order(12, "WORKING", "BUY", 50*time.Hour), order(13, "WORKING", "BUY", time.Hour), order(14, "QUEUED", "buy", 49*time.Hour)}, []int64{12, 14}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ids(StaleEntries(c.orders, now))
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

func TestSettleUpdatesOnlyTheCancelledOrder(t *testing.T) {
	db, err := appconfig.OpenDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := schemas.EnsureAll(db); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][]any{{"STALE", "SUBMITTED_PENDING_FILL", "555"}, {"OTHER", "SUBMITTED_PENDING_FILL", "556"}, {"DONE", "REJECTED", "555"}} {
		if _, err := db.Exec(`INSERT INTO stage_order (account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price, status, order_id, staged_at)
			VALUES ('111', 's', ?, 1, 10, 11, 9, ?, ?, ?)`, r[0], r[1], r[2], now); err != nil {
			t.Fatal(err)
		}
	}
	if note := settle(db, "111", "555"); note == "" {
		t.Fatal("settle changed nothing")
	}
	status := func(sym string) string {
		var s string
		if err := db.QueryRow(`SELECT status FROM stage_order WHERE symbol = ?`, sym).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for sym, want := range map[string]string{"STALE": "CANCELLED", "OTHER": "SUBMITTED_PENDING_FILL", "DONE": "REJECTED"} {
		if got := status(sym); got != want {
			t.Errorf("%s: status %s, want %s", sym, got, want)
		}
	}
}
