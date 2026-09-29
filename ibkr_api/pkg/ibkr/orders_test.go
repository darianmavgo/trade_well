package ibkr

import (
	"context"
	"encoding/json"
	"testing"

	schwabtrader "github.com/darianmavgo/schwaber/pkg/trader"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	// conid lookups are cached per Client: seeding the cache is the real code path.
	c.conids["TSLL"] = 111222
	return c
}

// schwaber's own bracket builder, translated to IBKR tickets: one parent carrying our
// cOID and two children that reference it (and carry no cOID of their own).
func TestBracketBecomesAnIBKRBracket(t *testing.T) {
	c := testClient(t)
	order, err := schwabtrader.BuildBracketOrderSpec("TSLL", 1, 10.00, 10.50, 9.00, schwabtrader.OrderTypeLimit)
	if err != nil {
		t.Fatal(err)
	}
	EnsureClientOrderID("U1", order)
	w, err := toWire(context.Background(), "U1", order, c.ResolveConid)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 3 {
		t.Fatalf("want parent + 2 children, got %d: %+v", len(w), w)
	}
	p := w[0]
	if p.COID != order.ClientOrderID || p.ParentID != "" || p.Side != "BUY" || p.OrderType != "LMT" || *p.Price != 10.00 || p.Conid != 111222 || p.SecType != "111222:STK" || p.TIF != "GTC" {
		t.Errorf("parent = %+v", p)
	}
	tp, sl := w[1], w[2]
	if tp.ParentID != p.COID || tp.COID != "" || tp.Side != "SELL" || tp.OrderType != "LMT" || *tp.Price != 10.50 || tp.TIF != "GTC" {
		t.Errorf("take-profit = %+v", tp)
	}
	if sl.ParentID != p.COID || sl.COID != "" || sl.OrderType != "STP" || sl.AuxPrice == nil || *sl.AuxPrice != 9.00 {
		t.Errorf("stop = %+v", sl)
	}
}

func TestMarketOrderAndClientOrderIDStability(t *testing.T) {
	c := testClient(t)
	o, _ := schwabtrader.BuildOrderSpec(schwabtrader.OrderTypeMarket, schwabtrader.InstructionSell, "tsll", 5, nil, "", "")
	id1 := EnsureClientOrderID("U1", o)
	o2, _ := schwabtrader.BuildOrderSpec(schwabtrader.OrderTypeMarket, schwabtrader.InstructionSell, "TSLL", 5, nil, "", "")
	if id2 := EnsureClientOrderID("U1", o2); id1 != id2 || len(id1) > 64 {
		t.Errorf("ids %q %q (must be equal and <= 64 chars)", id1, id2)
	}
	w, err := toWire(context.Background(), "U1", o, c.ResolveConid)
	if err != nil || len(w) != 1 || w[0].OrderType != "MKT" || w[0].Price != nil || w[0].Side != "SELL" || w[0].TIF != "DAY" || w[0].Quantity != 5 {
		t.Errorf("wire = %+v err=%v", w, err)
	}
}

func TestLiveOrderMapsToSchwabShape(t *testing.T) {
	raw := `{"acct":"U1","orderId":1234,"ticker":"TSLL","side":"SELL","orderType":"Stop","price":"9.00","timeInForce":"GTC",
	  "status":"Submitted","totalSize":1.0,"order_ref":"co_abc","lastExecutionTime_r":1790284466000}`
	var l liveOrder
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatal(err)
	}
	o := l.toOrder()
	if o.Status != "WORKING" || o.OrderType != "STOP" || o.StopPrice == nil || *o.StopPrice != 9 || o.OrderID == nil || *o.OrderID != 1234 ||
		o.ClientOrderID != "co_abc" || o.Duration != "GOOD_TILL_CANCEL" || o.Cancelable == nil || !*o.Cancelable {
		t.Errorf("order = %+v", o)
	}
	legs, err := schwabtrader.ParseOrderLegs(&o)
	if err != nil || legs[0].Instruction != "SELL" || legs[0].Instrument.Symbol != "TSLL" || legs[0].Quantity != 1 {
		t.Errorf("legs = %+v err=%v", legs, err)
	}
	for in, want := range map[string]string{"Filled": "FILLED", "Cancelled": "CANCELED", "Inactive": "REJECTED", "PreSubmitted": "WORKING", "PendingSubmit": "PENDING_ACTIVATION"} {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseNumberHandlesIBKRDecoration(t *testing.T) {
	for in, want := range map[string]float64{"1,234.5": 1234.5, "C182.50": 182.5, "": 0, "12%": 12, "H10": 10} {
		if got := ParseNumber(in); got != want {
			t.Errorf("ParseNumber(%q) = %v, want %v", in, got, want)
		}
	}
}
