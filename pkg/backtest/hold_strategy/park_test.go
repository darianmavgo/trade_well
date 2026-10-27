package hold_strategy

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func TestFamilyResolvesParkForAnySymbol(t *testing.T) {
	RegisterFrom(t.TempDir() + "/none.db")
	s, ok := strategy.Get("park-googl")
	if !ok {
		t.Fatal("park-googl not resolved")
	}
	rp, isPark := s.(strategy.ResidualProvider)
	if !isPark || rp.ParkSymbol() != "GOOGL" {
		t.Fatalf("got %T %v", s, s)
	}
	if s.ID() != "park-googl" || len(s.GenerateSignals(nil)) != 0 {
		t.Fatalf("park must emit no signals, id %q", s.ID())
	}
	if strategy.FamilyOf(s) != "hold" {
		t.Fatalf("park family %q, want hold", strategy.FamilyOf(s))
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParkIDDoesNotShadowBuyHold(t *testing.T) {
	RegisterFrom(t.TempDir() + "/none.db")
	if s, ok := strategy.Get("park-buy-hold"); ok {
		if _, isPark := s.(strategy.ResidualProvider); isPark {
			t.Fatal("park-buy-hold is the PARK ticker's buy-and-hold, not a park")
		}
	}
	if _, ok := strategy.Get("park-"); ok {
		t.Fatal("empty symbol resolved")
	}
}

func TestSplitResidual(t *testing.T) {
	sleeve := strategy.NewSQLPipeline("sleeve", "Sleeve", "a stack member that is not a park", "sql/strategies/streak_strategy", strategy.StrategyConfig{AllocationPct: 0.1})
	googl := NewPark("GOOGL")
	if _, _, err := strategy.SplitResidual([]strategy.Strategy{googl}); err == nil {
		t.Fatal("park as primary must fail")
	}
	if _, _, err := strategy.SplitResidual([]strategy.Strategy{sleeve, googl, NewPark("SPY")}); err == nil {
		t.Fatal("two parks must fail")
	}
	sleeves, sym, err := strategy.SplitResidual([]strategy.Strategy{sleeve, googl})
	if err != nil || sym != "GOOGL" || len(sleeves) != 1 || sleeves[0].ID() != sleeve.ID() {
		t.Fatalf("sleeves=%d sym=%q err=%v", len(sleeves), sym, err)
	}
	sleeves, sym, err = strategy.SplitResidual([]strategy.Strategy{sleeve})
	if err != nil || sym != "" || len(sleeves) != 1 {
		t.Fatalf("no park: sleeves=%d sym=%q err=%v", len(sleeves), sym, err)
	}
}
