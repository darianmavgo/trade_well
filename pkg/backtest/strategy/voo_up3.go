package strategy

import (
	"fmt"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

// VOOUp3Strategy longs a trade ETF the day VOO closes up for GainDays
// consecutive sessions.
type VOOUp3Strategy struct {
	TradeSymbol  string
	GainDays     int
	TP           float64
	SL           float64
	Hold         int
	marketDBPath string
	calcDBPath   string
}

func NewVOOUp3Strategy() *VOOUp3Strategy {
	s := &VOOUp3Strategy{
		TradeSymbol: "TQQQ",
		GainDays:    3,
		TP:          0.05,
		SL:          0.06,
		Hold:        8,
	}
	Register(s)
	return s
}

func (s *VOOUp3Strategy) ID() string { return "voo-up3" }

func (s *VOOUp3Strategy) Name() string {
	return fmt.Sprintf("VOO %d-Up → %s", s.GainDays, s.TradeSymbol)
}

func (s *VOOUp3Strategy) Description() string {
	return fmt.Sprintf(
		"Long %s when VOO closes up %d consecutive days. Exits: +%.0f%% TP / -%.0f%% SL / %d-day hold.",
		s.TradeSymbol, s.GainDays, s.TP*100, s.SL*100, s.Hold)
}

func (s *VOOUp3Strategy) Validate() error { return ValidateConfig(s.DefaultConfig()) }

func (s *VOOUp3Strategy) RequiredSymbols() []string {
	return []string{"VOO", s.TradeSymbol}
}

func (s *VOOUp3Strategy) DefaultConfig() StrategyConfig {
	return StrategyConfig{
		ID:                 s.ID(),
		Name:               s.Name(),
		Description:        s.Description(),
		Benchmark:          "VOO",
		Timeframe:          "1d",
		PositionSizing:     "fixed_pct",
		AllocationPct:      0.65,
		TargetPct:          1.0 + s.TP,
		TakeProfitPct:      s.TP,
		StopLossPct:        1.0 - s.SL,
		HoldingWindow:      s.Hold,
		PositionCap:        1,
		CashYieldAnnual:    0.045,
		SlippagePct:        0.0005,
		CommissionPerShare: 0.0001,
	}
}

func (s *VOOUp3Strategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// GenerateSignals runs the voo_up3 SQL pipeline: the up-streak grouping and the
// cross-symbol join are calculated in SQL.
func (s *VOOUp3Strategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	days := s.GainDays
	if days <= 0 {
		days = 3
	}
	cfg := s.DefaultConfig()
	cfg.DeclineDays = days
	return RunPipeline(s.ID(), s.Name(), s.Description(), "sql/strategies/voo_up3", cfg, s.marketDBPath, s.calcDBPath, "", barsBySymbol)
}

func (s *VOOUp3Strategy) ParameterSpace() ParameterSpace {
	cfg := s.DefaultConfig()
	return ParameterSpace{
		StrategyID:   s.ID(),
		StrategyName: s.Name(),
		Description:  s.Description(),
		Symbols:      []string{s.TradeSymbol},
		SignalSymbol: "VOO",
		Direction:    "rally",
		SignalDays:   []int{2, 3, 4, 5},
		HoldDays:     []int{2, 5, 8, 12, 15},
		TakeProfits:  []float64{0.03, 0.05, 0.08, 0.12},
		StopLosses:   []float64{0.02, 0.04, 0.06, 0.08},
		Regimes:      []string{"All Regimes"},
		Allocations:  []float64{cfg.AllocationPct},
		CashYield:    cfg.CashYieldAnnual,
		Baseline: BaselineParams{
			SignalDays: s.GainDays,
			HoldDays:   s.Hold,
			TakeProfit: s.TP,
			StopLoss:   s.SL,
			Allocation: cfg.AllocationPct,
			Regime:     "All Regimes",
		},
	}
}

func init() {
	NewVOOUp3Strategy()
}
