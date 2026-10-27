package trader

import (
	"encoding/json"
	"testing"
)

func TestBuildOrderSpecMarketBuy(t *testing.T) {
	order, err := BuildOrderSpec(OrderTypeMarket, InstructionBuy, "VOO", 10, nil, DurationDay, SessionNormal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if order.OrderType != OrderTypeMarket {
		t.Errorf("expected OrderType %s, got %s", OrderTypeMarket, order.OrderType)
	}
	if order.Duration != DurationDay {
		t.Errorf("expected Duration %s, got %s", DurationDay, order.Duration)
	}

	legs, err := ParseOrderLegs(order)
	if err != nil {
		t.Fatalf("failed to parse legs: %v", err)
	}
	if len(legs) != 1 {
		t.Fatalf("expected 1 leg, got %d", len(legs))
	}
	if legs[0].Instruction != InstructionBuy {
		t.Errorf("expected Instruction %s, got %s", InstructionBuy, legs[0].Instruction)
	}
	if legs[0].Instrument.Symbol != "VOO" {
		t.Errorf("expected Symbol VOO, got %s", legs[0].Instrument.Symbol)
	}
	if legs[0].Quantity != 10 {
		t.Errorf("expected Quantity 10, got %f", legs[0].Quantity)
	}
}

func TestBuildOrderSpecLimitBuy(t *testing.T) {
	price := 450.50
	order, err := BuildOrderSpec(OrderTypeLimit, InstructionBuy, "AAPL", 5, &price, DurationGTC, SessionNormal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if order.OrderType != OrderTypeLimit {
		t.Errorf("expected OrderType %s, got %s", OrderTypeLimit, order.OrderType)
	}
	if order.Price == nil || *order.Price != 450.50 {
		t.Errorf("expected Price 450.50, got %v", order.Price)
	}
	if order.Duration != DurationGTC {
		t.Errorf("expected Duration %s, got %s", DurationGTC, order.Duration)
	}
}

func TestBuildBracketOrderSpec(t *testing.T) {
	// Bracket order for SOFI: 1 share Buy Limit @ $10.00, Take Profit @ $11.50, Stop Loss @ $9.00
	order, err := BuildBracketOrderSpec("SOFI", 1, 10.00, 11.50, 9.00, OrderTypeLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if order.OrderStrategyType != StrategyTrigger {
		t.Errorf("expected OrderStrategyType %s, got %s", StrategyTrigger, order.OrderStrategyType)
	}
	if order.OrderType != OrderTypeLimit {
		t.Errorf("expected OrderType %s, got %s", OrderTypeLimit, order.OrderType)
	}
	if order.Price == nil || *order.Price != 10.00 {
		t.Errorf("expected Entry Price 10.00, got %v", order.Price)
	}

	// Verify child OCO strategies are present
	if len(order.ChildOrderStrategies) == 0 {
		t.Fatalf("expected childOrderStrategies to be present")
	}

	var ocoStrategies []ChildOrder
	if err := json.Unmarshal(order.ChildOrderStrategies, &ocoStrategies); err != nil {
		t.Fatalf("failed to unmarshal childOrderStrategies: %v", err)
	}

	if len(ocoStrategies) != 1 || ocoStrategies[0].OrderStrategyType != StrategyOCO {
		t.Fatalf("expected 1 OCO child strategy")
	}

	children := ocoStrategies[0].ChildOrderStrategies
	if len(children) != 2 {
		t.Fatalf("expected 2 OCO child orders (profit + stop), got %d", len(children))
	}

	// Child 1: Take Profit Limit
	if children[0].OrderType != OrderTypeLimit || children[0].Price == nil || *children[0].Price != 11.50 {
		t.Errorf("expected Take Profit limit at 11.50, got %v", children[0])
	}

	// Child 2: Stop Loss Stop
	if children[1].OrderType != OrderTypeStop || children[1].StopPrice == nil || *children[1].StopPrice != 9.00 {
		t.Errorf("expected Stop Loss stop at 9.00, got %v", children[1])
	}
}

func TestBuildBracketOrderInvalidPrices(t *testing.T) {
	// Profit price <= entry price
	_, err := BuildBracketOrderSpec("SOFI", 1, 10.00, 9.50, 9.00, OrderTypeLimit)
	if err == nil {
		t.Errorf("expected error for profit price <= entry price, got nil")
	}

	// Stop price >= entry price
	_, err = BuildBracketOrderSpec("SOFI", 1, 10.00, 11.50, 10.50, OrderTypeLimit)
	if err == nil {
		t.Errorf("expected error for stop price >= entry price, got nil")
	}
}

func TestBuildBracketOrderSpecCrisisGuard(t *testing.T) {
	order, err := BuildBracketOrderSpec("TECL", 10, 100.00, 105.00, 0, OrderTypeLimit)
	if err != nil {
		t.Fatalf("expected crisis-guard bracket for SL=0, got: %v", err)
	}
	if order == nil {
		t.Fatal("nil order")
	}
	if len(order.ChildOrderStrategies) == 0 {
		t.Fatal("expected child OCO strategies")
	}
}

func TestBuildBracketOrderSpecDurationDayEntry(t *testing.T) {
	// The default LIMIT entry is GOOD_TILL_CANCEL; a DAY entry expires at the close.
	def, err := BuildBracketOrderSpec("ROM", 10, 95.00, 97.85, 90.25, OrderTypeLimit)
	if err != nil || def.Duration != DurationGTC {
		t.Fatalf("default limit entry: duration %q err %v, want GOOD_TILL_CANCEL", def.Duration, err)
	}
	day, err := BuildBracketOrderSpecDuration("ROM", 10, 95.00, 97.85, 90.25, OrderTypeLimit, DurationDay)
	if err != nil {
		t.Fatal(err)
	}
	if day.Duration != DurationDay || day.Price == nil || *day.Price != 95.00 {
		t.Fatalf("day entry: duration %q price %v", day.Duration, day.Price)
	}
	// The exit legs only exist once the entry fills, so they stay GOOD_TILL_CANCEL.
	var oco []ChildOrder
	if err := json.Unmarshal(day.ChildOrderStrategies, &oco); err != nil || len(oco) != 1 {
		t.Fatalf("children: %v %v", oco, err)
	}
	for _, c := range oco[0].ChildOrderStrategies {
		if c.Duration != DurationGTC {
			t.Errorf("exit leg duration %q, want GOOD_TILL_CANCEL", c.Duration)
		}
	}
	if _, err := BuildBracketOrderSpecDuration("ROM", 10, 95.00, 97.85, 90.25, OrderTypeLimit, "FILL_OR_KILL"); err == nil {
		t.Error("an unknown entry duration must be refused")
	}
}

func TestBuildBracketOrderSpecNoTakeProfit(t *testing.T) {
	order, err := BuildBracketOrderSpec("SOXL", 67, 83.56, 0, 73.53, OrderTypeLimit)
	if err != nil {
		t.Fatalf("expected stop-only bracket for TP=0, got: %v", err)
	}
	var kids []ChildOrder
	if err := json.Unmarshal(order.ChildOrderStrategies, &kids); err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].OrderType != OrderTypeStop || kids[0].StopPrice == nil || *kids[0].StopPrice != 73.53 {
		t.Errorf("expected a single stop child at 73.53, got %+v", kids)
	}
}
