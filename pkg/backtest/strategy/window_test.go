package strategy

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

func TestWindowForUsesStrategyNotHardcoded(t *testing.T) {
	strat, ok := Get("sig-voo-buy-tecl")
	if !ok {
		t.Skip("sig-voo-buy-tecl not registered in this build")
	}
	hold, _, _ := WindowFor(strat, models.Signal{})
	if hold <= 0 {
		t.Fatal("expected strategy DefaultConfig holding window")
	}
	over := models.Signal{HoldDaysOverride: 2, TakeProfit: 12.5}
	h2, tp, _ := WindowFor(strat, over)
	if h2 != 2 {
		t.Fatalf("HoldDaysOverride should win, got %d", h2)
	}
	if tp != 12.5 {
		t.Fatalf("signal take-profit should win, got %v", tp)
	}
}

func TestWindowForWithoutStrategyKeepsSignalPrices(t *testing.T) {
	hold, tp, sl := WindowFor(nil, models.Signal{TakeProfit: 11, StopLoss: 9, BuyLimit: 10})
	if hold != 0 || tp != 11 || sl != 9 {
		t.Fatalf("got hold=%d tp=%v sl=%v, want 0, 11, 9", hold, tp, sl)
	}
}
