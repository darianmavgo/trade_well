package strategy

import (
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// TSLLDailyOneShareStrategy buys exactly one share of TSLL (Direxion Daily
// TSLA Bull 2X) at every trading day's close and holds it one day, exiting on
// +5% take-profit, -20% stop-loss, or the next bar's close, whichever first.
//
// Unconditional entry: there is no signal filter, so this measures the raw
// edge (or lack of one) in a 2x leveraged single-stock ETF with a lopsided
// 5% target / 20% stop. TakeProfitPct is a fractional offset (0.05 = +5%);
// StopLossPct is a direct multiplier (0.80 = -20%), like every other strategy.
type TSLLDailyOneShareStrategy struct {
	marketDBPath string
	calcDBPath   string
}

func init() {
	s := &TSLLDailyOneShareStrategy{}
	Register(s)
	RegisterAlias("tsll-daily", s)
}

func (s *TSLLDailyOneShareStrategy) ID() string { return "tsll-daily-one-share" }

func (s *TSLLDailyOneShareStrategy) Name() string { return "TSLL Daily 1-Share, 1-Day Hold" }

func (s *TSLLDailyOneShareStrategy) Description() string {
	return "Buys one share of TSLL every trading day at the close and holds one day " +
		"(+5% take-profit / -20% stop-loss / 1d max hold)."
}

func (s *TSLLDailyOneShareStrategy) RequiredSymbols() []string { return []string{"TSLL"} }

// MinHistoryBars implements MinHistoryProvider: only the latest bar is needed.
func (s *TSLLDailyOneShareStrategy) MinHistoryBars() int { return 2 }

func (s *TSLLDailyOneShareStrategy) DefaultConfig() StrategyConfig {
	return StrategyConfig{
		ID:             s.ID(),
		Name:           s.Name(),
		Description:    s.Description(),
		Benchmark:      "TSLL",
		TradeSymbol:    "TSLL",
		PositionSizing: "fixed_shares",
		FixedShares:    1,
		AllocationPct:  1.0, // required by ValidateConfig; sizing is by FixedShares
		TargetPct:      1.05,
		TakeProfitPct:  0.05,
		StopLossPct:    0.80,
		HoldingWindow:  1,
		PositionCap:    1, // the prior day's share is closed before the next entry
		// TSLL (Direxion Daily TSLA Bull 1.5X) is a single-name leveraged
		// product on one of the most volatile large-cap underlyings traded;
		// even with solid ADV its quoted spread widens more than a
		// diversified leveraged index ETF like TECL during normal
		// volatility, let alone around TSLA-specific news -- 20bp reflects
		// that. Commission matches Schwab's real $0 online equity/ETF
		// commission; the token $0.0001/share stands in for SEC/FINRA TAF
		// pass-through fees Schwab still collects on sells.
		SlippagePct:        0.0020,
		CommissionPerShare: 0.0001,
	}
}

func (s *TSLLDailyOneShareStrategy) Validate() error { return ValidateConfig(s.DefaultConfig()) }

func (s *TSLLDailyOneShareStrategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// GenerateSignals runs the every_bar SQL pipeline: one entry per bar of TSLL,
// each with its own take-profit, stop and one-day hold.
func (s *TSLLDailyOneShareStrategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	return RunPipeline(s.ID(), s.Name(), s.Description(), "sql/strategies/every_bar", s.DefaultConfig(), s.marketDBPath, s.calcDBPath, "market", barsBySymbol)
}
