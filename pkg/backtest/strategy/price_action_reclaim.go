package strategy

import (
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// PriceActionReclaimStrategy implements a strategy based on "Once You Learn Price Action"
// four pillars:
// 1. Map: Find a significant support level.
// 2. Confirmation: Price drops below support and then reclaims it (closes above).
// 3. Invalidation: Stop loss placed at the support level, dynamically calculated.
// 4. Context: Broader trend must be upwards (e.g., price above 200 SMA).
type PriceActionReclaimStrategy struct {
	marketDBPath string
	calcDBPath   string
}

func init() {
	Register(&PriceActionReclaimStrategy{})
}

func (s *PriceActionReclaimStrategy) ID() string   { return "price-action-reclaim" }
func (s *PriceActionReclaimStrategy) Name() string { return "Price Action Reclaim" }
func (s *PriceActionReclaimStrategy) Description() string {
	return "Trades based on price action: Support established, broken, and reclaimed in an uptrending context."
}

func (s *PriceActionReclaimStrategy) DefaultConfig() StrategyConfig {
	return StrategyConfig{
		ID:                 s.ID(),
		Name:               s.Name(),
		Description:        s.Description(),
		PositionSizing:     "fixed_pct",
		AllocationPct:      0.10,
		TakeProfitPct:      0.10, // Default 10% TP
		HoldingWindow:      20,
		PositionCap:        1,
		SlippagePct:        0.0005,
		CommissionPerShare: 0.0001,
	}
}

func (s *PriceActionReclaimStrategy) Validate() error {
	return ValidateConfig(s.DefaultConfig())
}

func (s *PriceActionReclaimStrategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// GenerateSignals runs the price_action_reclaim SQL pipeline over every symbol
// with 250 bars in the window: support, the break below it and the reclaim, in an
// uptrend, are all calculated in SQL.
func (s *PriceActionReclaimStrategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	return RunPipeline(s.ID(), s.Name(), s.Description(), "sql/strategies/price_action_reclaim", s.DefaultConfig(), s.marketDBPath, s.calcDBPath, "", barsBySymbol)
}
