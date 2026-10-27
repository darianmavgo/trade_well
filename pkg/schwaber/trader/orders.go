package trader

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

// Order Constants
const (
	// CrisisStopFraction is applied when a strategy intentionally has no stop
	// (stopLossPrice == 0): live Schwab brackets still get a -20% crisis guard.
	CrisisStopFraction = 0.80

	OrderTypeMarket    = "MARKET"
	OrderTypeLimit     = "LIMIT"
	OrderTypeStop      = "STOP"
	OrderTypeStopLimit = "STOP_LIMIT"

	StrategySingle  = "SINGLE"
	StrategyTrigger = "TRIGGER"
	StrategyOCO     = "OCO"

	InstructionBuy  = "BUY"
	InstructionSell = "SELL"

	DurationDay = "DAY"
	DurationGTC = "GOOD_TILL_CANCEL"

	SessionNormal   = "NORMAL"
	SessionAM       = "AM"
	SessionPM       = "PM"
	SessionSeamless = "SEAMLESS"

	AssetTypeEquity = "EQUITY"
)

type Instrument struct {
	Symbol    string `json:"symbol"`
	AssetType string `json:"assetType"`
}

type OrderLeg struct {
	Instruction string     `json:"instruction"`
	Quantity    float64    `json:"quantity"`
	Instrument  Instrument `json:"instrument"`
}

// ChildOrder represents a sub-order in an OCO or Trigger strategy.
type ChildOrder struct {
	OrderType            string       `json:"orderType,omitempty"`
	Session              string       `json:"session,omitempty"`
	Price                *float64     `json:"price,omitempty"`
	StopPrice            *float64     `json:"stopPrice,omitempty"`
	Duration             string       `json:"duration,omitempty"`
	OrderStrategyType    string       `json:"orderStrategyType,omitempty"`
	OrderLegCollection   []OrderLeg   `json:"orderLegCollection,omitempty"`
	ChildOrderStrategies []ChildOrder `json:"childOrderStrategies,omitempty"`
}

// RoundPrice rounds a USD equity price to cents (Schwab rejects over-precise limits).
func RoundPrice(p float64) float64 {
	return math.Round(p*100) / 100
}

// BuildOrderSpec constructs a basic single-leg schwab.Order.
func BuildOrderSpec(
	orderType string,
	instruction string,
	symbol string,
	quantity float64,
	price *float64,
	duration string,
	session string,
) (*schwab.Order, error) {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("symbol cannot be empty")
	}
	if quantity <= 0 {
		return nil, fmt.Errorf("quantity must be greater than zero")
	}
	if (orderType == OrderTypeLimit || orderType == OrderTypeStopLimit) && (price == nil || *price <= 0) {
		return nil, fmt.Errorf("price must be greater than zero for %s orders", orderType)
	}

	if duration == "" {
		duration = DurationDay
	}
	if session == "" {
		session = SessionNormal
	}

	legs := []OrderLeg{
		{
			Instruction: instruction,
			Quantity:    quantity,
			Instrument: Instrument{
				Symbol:    sym,
				AssetType: AssetTypeEquity,
			},
		},
	}

	legsJSON, err := json.Marshal(legs)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal order legs: %w", err)
	}

	order := &schwab.Order{
		OrderType:          orderType,
		Session:            session,
		Duration:           duration,
		OrderStrategyType:  StrategySingle,
		OrderLegCollection: legsJSON,
	}

	if price != nil && *price > 0 {
		p := *price
		order.Price = &p
	}

	return order, nil
}

// BuildBracketOrderSpec creates a 1st-triggers-OCO bracket order:
// 1. Entry Order (e.g. BUY 1 share at entryPrice)
// 2. Take-Profit Order (e.g. SELL 1 share at profitPrice Limit)
// 3. Stop-Loss Order (e.g. SELL 1 share at stopLossPrice Stop)
//
// If stopLossPrice <= 0 (strategy has no stop), a crisis guard of
// CrisisStopFraction (-20%) of entryPrice is used so Schwab still gets a valid OCO.
func BuildBracketOrderSpec(
	symbol string,
	quantity float64,
	entryPrice float64,
	profitPrice float64,
	stopLossPrice float64,
	entryType string, // LIMIT or MARKET
) (*schwab.Order, error) {
	return BuildBracketOrderSpecDuration(symbol, quantity, entryPrice, profitPrice, stopLossPrice, entryType, "")
}

// BuildBracketOrderSpecDuration is BuildBracketOrderSpec with the entry order's
// duration named: DurationDay makes the entry limit expire at the close if it has not
// filled (the exit legs stay GOOD_TILL_CANCEL, they only exist once it fills). An empty
// entryDuration keeps the default: GOOD_TILL_CANCEL for a LIMIT entry, DAY for MARKET.
func BuildBracketOrderSpecDuration(
	symbol string,
	quantity float64,
	entryPrice float64,
	profitPrice float64,
	stopLossPrice float64,
	entryType string, // LIMIT or MARKET
	entryDuration string, // "", DAY or GOOD_TILL_CANCEL
) (*schwab.Order, error) {
	switch entryDuration {
	case "", DurationDay, DurationGTC:
	default:
		return nil, fmt.Errorf("entry duration %q is not DAY or GOOD_TILL_CANCEL", entryDuration)
	}
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if sym == "" {
		return nil, fmt.Errorf("symbol cannot be empty")
	}
	if quantity <= 0 {
		return nil, fmt.Errorf("quantity must be greater than zero")
	}
	if entryPrice <= 0 && entryType == OrderTypeLimit {
		return nil, fmt.Errorf("entry price must be greater than zero")
	}
	if profitPrice < 0 {
		return nil, fmt.Errorf("profit target price cannot be negative")
	}
	// stopLossPrice == 0 means "no strategy stop"; attach a -20% crisis guard for the broker.
	if stopLossPrice <= 0 && entryPrice > 0 {
		stopLossPrice = entryPrice * CrisisStopFraction
	}
	if stopLossPrice <= 0 {
		return nil, fmt.Errorf("stop loss price must be greater than zero (or provide entryPrice for crisis guard)")
	}
	if profitPrice > 0 && entryPrice > 0 && profitPrice <= entryPrice {
		return nil, fmt.Errorf("take-profit price ($%.2f) must be higher than entry price ($%.2f)", profitPrice, entryPrice)
	}
	if entryPrice > 0 && stopLossPrice >= entryPrice {
		return nil, fmt.Errorf("stop-loss price ($%.2f) must be lower than entry price ($%.2f)", stopLossPrice, entryPrice)
	}

	if entryType == "" {
		entryType = OrderTypeLimit
	}

	entryPrice = RoundPrice(entryPrice)
	profitPrice = RoundPrice(profitPrice)
	stopLossPrice = RoundPrice(stopLossPrice)

	// 1. Entry Order Legs
	entryLegs := []OrderLeg{
		{
			Instruction: InstructionBuy,
			Quantity:    quantity,
			Instrument:  Instrument{Symbol: sym, AssetType: AssetTypeEquity},
		},
	}
	entryLegsJSON, err := json.Marshal(entryLegs)
	if err != nil {
		return nil, err
	}

	// 2. Child OCO: Take-Profit (Limit Sell) + Stop-Loss (Stop Sell)
	sellLeg := []OrderLeg{
		{
			Instruction: InstructionSell,
			Quantity:    quantity,
			Instrument:  Instrument{Symbol: sym, AssetType: AssetTypeEquity},
		},
	}

	pPrice := profitPrice
	sPrice := stopLossPrice

	stopChild := ChildOrder{
		OrderStrategyType:  StrategySingle,
		OrderType:          OrderTypeStop,
		Session:            SessionNormal,
		Duration:           DurationGTC,
		StopPrice:          &sPrice,
		OrderLegCollection: sellLeg,
	}
	// A zero profit price means the strategy has no take-profit (it exits on hold days):
	// the entry carries only the stop. Otherwise the take-profit and stop are an OCO pair.
	ocoStrategy := []ChildOrder{stopChild}
	if profitPrice > 0 {
		ocoStrategy = []ChildOrder{
			{
				OrderStrategyType: StrategyOCO,
				ChildOrderStrategies: []ChildOrder{
					{
						OrderStrategyType:  StrategySingle,
						OrderType:          OrderTypeLimit,
						Session:            SessionNormal,
						Duration:           DurationGTC,
						Price:              &pPrice,
						OrderLegCollection: sellLeg,
					},
					stopChild,
				},
			},
		}
	}

	childJSON, err := json.Marshal(ocoStrategy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal child strategies: %w", err)
	}

	// LIMIT entries are GTC so a buy under the ask rests Working until fill
	// (DAY was fine intraday, but GTC matches "wait for pullback"). MARKET stays DAY.
	duration := DurationGTC
	if entryType == OrderTypeMarket {
		duration = DurationDay
	}
	if entryDuration != "" {
		duration = entryDuration
	}
	order := &schwab.Order{
		OrderType:            entryType,
		Session:              SessionNormal,
		Duration:             duration,
		OrderStrategyType:    StrategyTrigger,
		OrderLegCollection:   entryLegsJSON,
		ChildOrderStrategies: childJSON,
	}

	if entryType == OrderTypeLimit && entryPrice > 0 {
		ep := entryPrice
		order.Price = &ep
	}

	return order, nil
}

// ParseOrderLegs extracts the typed legs from a schwab.Order.
func ParseOrderLegs(order *schwab.Order) ([]OrderLeg, error) {
	if len(order.OrderLegCollection) == 0 {
		return nil, fmt.Errorf("order has no legs")
	}
	var legs []OrderLeg
	if err := json.Unmarshal(order.OrderLegCollection, &legs); err != nil {
		return nil, err
	}
	return legs, nil
}

// OrderSymbol is the upper-cased symbol of an order's first leg ("" if unreadable).
func OrderSymbol(order *schwab.Order) string {
	legs, err := ParseOrderLegs(order)
	if err != nil || len(legs) == 0 {
		return ""
	}
	return strings.ToUpper(legs[0].Instrument.Symbol)
}

// OrderIsSell reports whether an order's first leg is a sell.
func OrderIsSell(order *schwab.Order) bool {
	legs, err := ParseOrderLegs(order)
	if err != nil || len(legs) == 0 {
		return false
	}
	return strings.Contains(strings.ToUpper(legs[0].Instruction), "SELL")
}
