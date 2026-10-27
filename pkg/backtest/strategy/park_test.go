package strategy

import "testing"

func TestGetResolvesParkForAnySymbol(t *testing.T) {
	s, ok := Get("park-googl")
	if !ok {
		t.Fatal("park-googl not resolved")
	}
	rp, isPark := s.(ResidualProvider)
	if !isPark || rp.ParkSymbol() != "GOOGL" {
		t.Fatalf("got %T %v", s, s)
	}
	if s.ID() != "park-googl" || len(s.GenerateSignals(nil)) != 0 {
		t.Fatalf("park must emit no signals, id %q", s.ID())
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParkIDDoesNotShadowBuyHold(t *testing.T) {
	if s, ok := Get("park-buy-hold"); ok {
		if _, isPark := s.(ResidualProvider); isPark {
			t.Fatal("park-buy-hold is the PARK ticker's buy-and-hold, not a park")
		}
	}
	if _, ok := Get("park-"); ok {
		t.Fatal("empty symbol resolved")
	}
}

func TestSplitResidual(t *testing.T) {
	sleeve, ok := Get("price-action-reclaim")
	if !ok {
		t.Fatal("price-action-reclaim not registered")
	}
	googl := NewPark("GOOGL")
	if _, _, err := SplitResidual([]Strategy{googl}); err == nil {
		t.Fatal("park as primary must fail")
	}
	if _, _, err := SplitResidual([]Strategy{sleeve, googl, NewPark("SPY")}); err == nil {
		t.Fatal("two parks must fail")
	}
	sleeves, sym, err := SplitResidual([]Strategy{sleeve, googl})
	if err != nil || sym != "GOOGL" || len(sleeves) != 1 || sleeves[0].ID() != sleeve.ID() {
		t.Fatalf("sleeves=%d sym=%q err=%v", len(sleeves), sym, err)
	}
	sleeves, sym, err = SplitResidual([]Strategy{sleeve})
	if err != nil || sym != "" || len(sleeves) != 1 {
		t.Fatalf("no park: sleeves=%d sym=%q err=%v", len(sleeves), sym, err)
	}
}
