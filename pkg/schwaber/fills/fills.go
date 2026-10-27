// Package fills reads what Schwab actually executed from an order: the average price and the
// quantity of its execution legs. The order list carries them in orderActivityCollection, not in
// the order's own price or stopPrice (those are the limit and the trigger, never the fill).
package fills

import "strings"

// FillPrice is the quantity-weighted average execution price on a Schwab order.
// side is "BUY" or "SELL"; an empty side uses this order's own execution legs
// and does not walk children. A non-empty side walks this order and its
// childOrderStrategies and returns the first order whose legs match that side
// and that actually executed. ok is false when no execution leg had a price.
func FillPrice(order map[string]interface{}, side string) (price, qty float64, ok bool) {
	if order == nil {
		return 0, 0, false
	}
	side = strings.ToUpper(strings.TrimSpace(side))
	if side == "" || orderSide(order) == side {
		if px, q, hit := ownExecution(order); hit {
			return px, q, true
		}
	}
	if side == "" {
		return 0, 0, false
	}
	kids, _ := order["childOrderStrategies"].([]interface{})
	for _, k := range kids {
		km, ok := k.(map[string]interface{})
		if !ok {
			continue
		}
		if px, q, hit := FillPrice(km, side); hit {
			return px, q, true
		}
	}
	return 0, 0, false
}

func orderSide(order map[string]interface{}) string {
	legs, _ := order["orderLegCollection"].([]interface{})
	for _, l := range legs {
		lm, ok := l.(map[string]interface{})
		if !ok {
			continue
		}
		inst, _ := lm["instruction"].(string)
		switch strings.ToUpper(strings.TrimSpace(inst)) {
		case "BUY":
			return "BUY"
		case "SELL":
			return "SELL"
		}
	}
	return ""
}

func ownExecution(order map[string]interface{}) (price, qty float64, ok bool) {
	acts, _ := order["orderActivityCollection"].([]interface{})
	var notional float64
	for _, a := range acts {
		am, ok := a.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := am["activityType"].(string); t != "" && !strings.EqualFold(t, "EXECUTION") {
			continue
		}
		legs, _ := am["executionLegs"].([]interface{})
		for _, l := range legs {
			lm, ok := l.(map[string]interface{})
			if !ok {
				continue
			}
			px, _ := lm["price"].(float64)
			q, _ := lm["quantity"].(float64)
			if px <= 0 || q <= 0 {
				continue
			}
			notional += px * q
			qty += q
		}
	}
	if qty <= 0 {
		return 0, 0, false
	}
	return notional / qty, qty, true
}
