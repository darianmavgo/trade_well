package strategy

import (
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// DividendCoveredCallStrategy holds a dividend ETF and each month sells one
// call per 100 shares roughly 10% (or 5%) out of the money, expiring ~1 month out.
// Simulated by pkg/options (see OptionOverlayProvider) over the trailing two
// years, which is all the history Polygon's free options tier serves.
//
//   - a call that finishes in the money is exercised: the shares are called
//     away at the strike and all cash is immediately used to buy back in;
//   - dividends are withdrawn from the account as they are paid (not
//     reinvested), and counted in the reported total equity;
//   - needs `market_history -source polygon-options -symbols <SYM> -otm <10|5>` first.
type DividendCoveredCallStrategy struct {
	Symbol string
	Label  string
	OTMPct float64 // target strike distance; 10 keeps the plain "-covered-call" ID
}

func init() {
	for _, e := range []struct{ sym, label string }{
		{"SCHD", "Schwab U.S. Dividend Equity ETF"},
		{"VYM", "Vanguard High Dividend Yield ETF"},
		{"DVY", "iShares Select Dividend ETF"},
	} {
		Register(&DividendCoveredCallStrategy{Symbol: e.sym, Label: e.label, OTMPct: 10})
		Register(&DividendCoveredCallStrategy{Symbol: e.sym, Label: e.label, OTMPct: 5})
	}
}

func (s *DividendCoveredCallStrategy) ID() string {
	if s.OTMPct == 10 {
		return strings.ToLower(s.Symbol) + "-covered-call"
	}
	return fmt.Sprintf("%s-covered-call-%.0fpct", strings.ToLower(s.Symbol), s.OTMPct)
}

func (s *DividendCoveredCallStrategy) Name() string {
	return fmt.Sprintf("%s Covered Call %.0f%% OTM (%s, dividends withdrawn)", s.Symbol, s.OTMPct, s.Label)
}

func (s *DividendCoveredCallStrategy) Description() string {
	return fmt.Sprintf("Holds %s and sells a ~1-month call ~%.0f%% out of the money each month over the last 2 years. "+
		"Exercised calls are re-bought immediately with all available cash; dividends are withdrawn as paid, not reinvested. "+
		"Needs Polygon option history (market_history -source polygon-options -symbols %s -otm %.0f).", s.Symbol, s.OTMPct, s.Symbol, s.OTMPct)
}

func (s *DividendCoveredCallStrategy) OverlaySpec() OverlaySpec {
	return OverlaySpec{Underlying: s.Symbol, OTMPct: s.OTMPct, WindowYears: 2, OptionSlip: 0.02, OptionCommission: 0.65}
}

func (s *DividendCoveredCallStrategy) RequiredSymbols() []string { return []string{s.Symbol} }

func (s *DividendCoveredCallStrategy) DefaultConfig() StrategyConfig {
	return StrategyConfig{
		ID:                 s.ID(),
		Name:               s.Name(),
		Description:        s.Description(),
		Benchmark:          s.Symbol,
		TargetPct:          999.0,
		StopLossPct:        0.0001,
		HoldingWindow:      99999,
		PositionCap:        1,
		AllocationPct:      1.0,
		SlippagePct:        0.0005,
		CommissionPerShare: 0.0001,
	}
}

func (s *DividendCoveredCallStrategy) Validate() error { return ValidateConfig(s.DefaultConfig()) }

// GenerateSignals emits nothing: the runner simulates this strategy through
// its OverlaySpec, not through stock entry signals.
func (s *DividendCoveredCallStrategy) GenerateSignals(map[string][]models.Bar) []models.Signal {
	return nil
}

func (s *DividendCoveredCallStrategy) SetDatabases(marketDBPath, calcDBPath string) {}
