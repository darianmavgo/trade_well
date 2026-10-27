package accountstore

import (
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/fills"
)

// FillPrice is fills.FillPrice, kept here for the callers that read the account mirror.
func FillPrice(order map[string]interface{}, side string) (price, qty float64, ok bool) {
	return fills.FillPrice(order, side)
}

// FeeAndSymbol reads Schwab transferItems. Fees are the absolute amounts of
// items that carry a feeType (commission, SEC, TAF). Symbol is the first
// instrument symbol on any item.
func FeeAndSymbol(tx map[string]interface{}) (fees float64, symbol string) {
	if tx == nil {
		return 0, ""
	}
	items, _ := tx["transferItems"].([]interface{})
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if ft, _ := m["feeType"].(string); strings.TrimSpace(ft) != "" {
			if amt, ok := m["amount"].(float64); ok {
				if amt < 0 {
					amt = -amt
				}
				fees += amt
			}
		}
		if symbol == "" {
			if inst, ok := m["instrument"].(map[string]interface{}); ok {
				symbol, _ = inst["symbol"].(string)
			}
		}
	}
	return fees, symbol
}
