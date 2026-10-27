package strategy

import (
	"testing"
)

func TestAllStrategiesHaveAllocationPct(t *testing.T) {
	strats := List()
	if len(strats) == 0 {
		t.Fatalf("No strategies registered")
	}

	for _, s := range strats {
		cfg := s.DefaultConfig()
		maxAlloc := 1.0
		if cfg.UseMargin && cfg.MarginLeverage > 1.0 {
			maxAlloc = cfg.MarginLeverage
		}
		if cfg.AllocationPct <= 0 || cfg.AllocationPct > maxAlloc {
			t.Errorf("Strategy '%s' (%s) has invalid AllocationPct: %f (must be > 0 and <= %.1f)",
				s.ID(), s.Name(), cfg.AllocationPct, maxAlloc)
		} else {
			t.Logf("✓ Strategy '%-20s': AllocationPct = %.2f", s.ID(), cfg.AllocationPct)
		}
	}
}

// Strategies come only from Go files in pkg/strategy itself: a SQL pipeline
// folder registers only when a strategy in this package owns it.
func TestSQLPipelinesRegisterOnlyWithGoStrategy(t *testing.T) {
	AutoRegisterSQLStrategies("../../..", "../../../data/market_history.db")

	// voo_up3 still has voo_up3.go
	if _, ok := Get("voo_up3-sql"); !ok {
		t.Errorf("voo_up3-sql should be registered (owned by a strategy)")
	}
}
