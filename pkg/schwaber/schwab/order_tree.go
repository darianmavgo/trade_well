package schwab

import (
	"encoding/json"
	"strings"
)

// Children are the orders nested under o in childOrderStrategies: the OCO under a
// bracket's TRIGGER entry, then the take-profit and stop under the OCO. Nil when o has
// none or they do not decode.
func (o Order) Children() []Order {
	if len(o.ChildOrderStrategies) == 0 {
		return nil
	}
	var kids []Order
	if err := json.Unmarshal(o.ChildOrderStrategies, &kids); err != nil {
		return nil
	}
	return kids
}

// FlattenOrders returns orders and, depth-first, every order nested beneath them, each
// parent before its children.
func FlattenOrders(orders []Order) []Order {
	var out []Order
	for _, o := range orders {
		out = append(out, o)
		out = append(out, FlattenOrders(o.Children())...)
	}
	return out
}

// IsWorkingStatus reports whether a broker order status is one that can still be
// cancelled and that reserves shares or cash: resting, queued, or waiting on a parent
// order or condition.
func IsWorkingStatus(status string) bool {
	switch strings.ToUpper(status) {
	case "WORKING", "PENDING_ACTIVATION", "QUEUED", "ACCEPTED", "AWAITING_PARENT_ORDER",
		"AWAITING_STOP_CONDITION", "AWAITING_CONDITION", "PENDING_ACKNOWLEDGEMENT":
		return true
	}
	return false
}
