package strategy

import (
	"testing"
)

func TestAllStrategiesHaveAllocationPct(t *testing.T) {
	// Every strategy is a row of a family now, so this package registers none of its own; a
	// strategy registered here must still be sized sensibly.
	for _, s := range List() {
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

	// Every family folder is run by its family, not registered by itself as a "-sql" strategy.
	for _, dir := range []string{"streak_strategy", "hold_strategy", "tree_strategy", "price_action_reclaim_strategy"} {
		if _, ok := Get(dir + "-sql"); ok {
			t.Errorf("%s-sql is registered, but no strategy in this package owns the folder", dir)
		}
	}
}
