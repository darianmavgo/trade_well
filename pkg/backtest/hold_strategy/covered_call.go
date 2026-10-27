package hold_strategy

import (
	"fmt"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// coveredCallWindowYears is how much history the options simulation covers: the trailing two
// years, which is all Polygon's free options tier serves.
const coveredCallWindowYears = 2

// CoveredCall is a hold row with covered_call_otm_pct set. It holds the symbol and each month
// sells one call per 100 shares that many percent out of the money, expiring about a month out.
// pkg/options simulates it (see strategy.OptionOverlayProvider) on the option history that
// `market_history -source polygon-options -symbols <SYM> -otm <pct>` stores:
//
//   - a call that finishes in the money is exercised: the shares are called away at the strike
//     and all cash is used to buy back in;
//   - dividends are withdrawn as they are paid, not reinvested, and counted in total equity.
type CoveredCall struct {
	*Strategy
}

// Description says what the row holds and what it needs.
func (c *CoveredCall) Description() string {
	sym, otm := c.Row.Symbol, c.Row.CoveredCallOTMPct
	return fmt.Sprintf("Holds %s and sells a ~1-month call ~%.0f%% out of the money each month over the last %d years. "+
		"Exercised calls are re-bought immediately with all available cash; dividends are withdrawn as paid, not reinvested. "+
		"Needs Polygon option history (market_history -source polygon-options -symbols %s -otm %.0f).",
		sym, otm, coveredCallWindowYears, sym, otm)
}

// OverlaySpec is the options simulation the runner plays instead of entry signals.
func (c *CoveredCall) OverlaySpec() strategy.OverlaySpec {
	return strategy.OverlaySpec{
		Underlying: c.underlying(), OTMPct: c.Row.CoveredCallOTMPct, WindowYears: coveredCallWindowYears,
		OptionSlip: 0.02, OptionCommission: 0.65,
	}
}

// DefaultConfig is the hold config with the description of the covered call.
func (c *CoveredCall) DefaultConfig() strategy.StrategyConfig {
	cfg := c.Strategy.DefaultConfig()
	cfg.Description = c.Description()
	cfg.Benchmark = c.underlying()
	return cfg
}

// Validate checks the strike distance and the hold config.
func (c *CoveredCall) Validate() error {
	if otm := c.Row.CoveredCallOTMPct; otm <= 0 || otm > 50 {
		return fmt.Errorf("covered_call_otm_pct must be in (0, 50], got %v", otm)
	}
	return c.Strategy.Validate()
}

// GenerateSignals emits nothing: the runner simulates this strategy through its OverlaySpec.
func (c *CoveredCall) GenerateSignals(map[string][]models.Bar) []models.Signal { return nil }

func (c *CoveredCall) underlying() string { return c.Strategy.DefaultConfig().TradeSymbol }
