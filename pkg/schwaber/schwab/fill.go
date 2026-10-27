package schwab

import (
	"encoding/json"

	"github.com/darianmavgo/trade_well/pkg/schwaber/fills"
)

// FillPrice is the quantity-weighted average price this order actually executed at, and the
// quantity, from its execution legs. side "" reads this order's own executions; "BUY" or "SELL"
// also walks the orders nested under it and returns the first that executed on that side. ok is
// false when nothing executed (a working or rejected order, or a list read that carried no
// activity): never substitute Price or StopPrice, which are the limit and the trigger.
func (o Order) FillPrice(side string) (price, qty float64, ok bool) {
	raw, err := json.Marshal(o)
	if err != nil {
		return 0, 0, false
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, 0, false
	}
	return fills.FillPrice(m, side)
}
