package trader

import (
	"testing"
)

func createSampleAccount() *SecuritiesAccount {
	return &SecuritiesAccount{
		Type:          "CASH",
		AccountNumber: "12345678",
		Nickname:      "Roth IRA",
		CurrentBalances: Balances{
			CashBalance:             5000.0,
			CashAvailableForTrading: 5000.0,
			LiquidationValue:        25000.0,
		},
		Positions: []Position{
			{
				Instrument: PositionInstrument{
					Symbol:    "VOO",
					AssetType: "EQUITY",
				},
				LongQuantity: 20.0,
				AveragePrice: 450.0,
				MarketValue:  9000.0,
			},
		},
	}
}

func TestSafetyInsufficientBuyingPower(t *testing.T) {
	guard := NewSafetyGuard()
	price := 400.0
	// Available cash is $5,000. Trying to buy 15 shares @ $400 = $6,000.
	order, _ := BuildOrderSpec(OrderTypeLimit, InstructionBuy, "SPY", 15, &price, DurationDay, SessionNormal)

	acc := createSampleAccount()
	_, err := guard.ValidateOrder(order, acc, price)
	if err == nil {
		t.Fatalf("expected insufficient buying power error, got nil")
	}
}

func TestSafetyValidBuyOrder(t *testing.T) {
	guard := NewSafetyGuard()
	price := 400.0
	// Buying 5 shares @ $400 = $2,000 <= $5,000 cash
	order, _ := BuildOrderSpec(OrderTypeLimit, InstructionBuy, "SPY", 5, &price, DurationDay, SessionNormal)

	acc := createSampleAccount()
	res, err := guard.ValidateOrder(order, acc, price)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if !res.Valid {
		t.Errorf("expected valid to be true")
	}
	if res.EstimatedValue != 2000.0 {
		t.Errorf("expected estimated value 2000.0, got %f", res.EstimatedValue)
	}
}

func TestSafetySellShortPrevention(t *testing.T) {
	guard := NewSafetyGuard()
	price := 500.0
	// Holds 20 shares of VOO. Trying to sell 30 shares.
	order, _ := BuildOrderSpec(OrderTypeLimit, InstructionSell, "VOO", 30, &price, DurationDay, SessionNormal)

	acc := createSampleAccount()
	_, err := guard.ValidateOrder(order, acc, price)
	if err == nil {
		t.Fatalf("expected short-selling error, got nil")
	}
}

func TestSafetySellValidShares(t *testing.T) {
	guard := NewSafetyGuard()
	price := 500.0
	// Holds 20 shares of VOO. Selling 10 shares.
	order, _ := BuildOrderSpec(OrderTypeLimit, InstructionSell, "VOO", 10, &price, DurationDay, SessionNormal)

	acc := createSampleAccount()
	res, err := guard.ValidateOrder(order, acc, price)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Valid {
		t.Errorf("expected valid to be true")
	}
}

func TestSafetyBuyRejectedWhenNoBuyingPower(t *testing.T) {
	guard := NewSafetyGuard()
	price := 400.0
	order, _ := BuildOrderSpec(OrderTypeLimit, InstructionBuy, "SPY", 1, &price, DurationDay, SessionNormal)

	acc := &SecuritiesAccount{} // every balance field is zero: unreadable or empty account
	if _, err := guard.ValidateOrder(order, acc, price); err == nil {
		t.Fatalf("expected a buy to be rejected when the account reports no buying power")
	}
}

func TestSafetyPriceCollar(t *testing.T) {
	guard := &SafetyGuard{WarnPct: DefaultCollarWarnPct, RejectPct: DefaultCollarRejectPct}
	market := 100.0
	cases := []struct {
		name         string
		limit        float64
		wantReject   bool
		wantWarnings int
	}{
		{"near the market", 101, false, 0},
		{"20% away is fine", 80, false, 0},
		{"60% away warns", 160, false, 1},
		{"exactly 100% away warns", 200, false, 1},
		{"90% below warns, can never be refused", 10, false, 1},
		{"over 100% away is refused", 250, true, 0},
	}
	for _, c := range cases {
		price := c.limit
		order, _ := BuildOrderSpec(OrderTypeLimit, InstructionBuy, "SPY", 1, &price, DurationDay, SessionNormal)
		res, err := guard.ValidateOrder(order, createSampleAccount(), market)
		if c.wantReject {
			if err == nil {
				t.Errorf("%s: expected a collar rejection", c.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if len(res.Warnings) != c.wantWarnings {
			t.Errorf("%s: %d warnings, want %d", c.name, len(res.Warnings), c.wantWarnings)
		}
	}
	off := &SafetyGuard{}
	price := 1000.0
	order, _ := BuildOrderSpec(OrderTypeLimit, InstructionSell, "VOO", 1, &price, DurationDay, SessionNormal)
	if _, err := off.ValidateOrder(order, createSampleAccount(), market); err != nil {
		t.Errorf("a zero collar must limit nothing: %v", err)
	}
}

func TestEnvFraction(t *testing.T) {
	t.Setenv("PRICE_COLLAR_WARN_PCT", "0.3")
	if got := envFraction("PRICE_COLLAR_WARN_PCT", 0.5); got != 0.3 {
		t.Errorf("got %v, want 0.3", got)
	}
	t.Setenv("PRICE_COLLAR_WARN_PCT", "abc")
	if got := envFraction("PRICE_COLLAR_WARN_PCT", 0.5); got != 0.5 {
		t.Errorf("a bad value must give the default, got %v", got)
	}
}
