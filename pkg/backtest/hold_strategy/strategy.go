package hold_strategy

import (
	"context"
	"fmt"
	"github.com/jmoiron/sqlx"
	"log"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/markov_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

const pipelineDir = "sql/strategies/hold_strategy"

type Strategy struct {
	Row          refdb.HoldStrategy
	marketDBPath string
	calcDBPath   string
	reinvest     bool
}

func (s *Strategy) ID() string { return s.Row.ID }

// Family is the strategy family, which names its result database.
func (s *Strategy) Family() string { return "hold" }

func (s *Strategy) Name() string { return s.Row.Name }

func (s *Strategy) Description() string {
	desc := fmt.Sprintf("Buys and holds %s on the first available historical bar.", s.Row.Symbol)
	if s.Row.TrailingStopPct > 0 {
		desc = fmt.Sprintf("Buys and holds %s with %.0f%% trailing stop.", s.Row.Symbol, s.Row.TrailingStopPct*100)
		if s.Row.SMAReentryPeriod > 0 {
			desc += fmt.Sprintf(" Hops back in when Close > SMA%d.", s.Row.SMAReentryPeriod)
		}
	}
	if exits := s.exitsText(); exits != "" {
		desc += " Exits: " + exits + "."
	}
	if sym := s.regimeSymbol(); sym != "" {
		desc = fmt.Sprintf("Holds %s except while the %s Markov state is bear: sells on a bear bar, buys back on the first sideways or bull bar.", s.Row.Symbol, sym)
	}
	if s.Row.TotalReturn > 0 {
		desc += " Simulated on dividend-adjusted prices (total return)."
	}
	return desc
}

// exitsText lists the exits the row sets, "" when it sets none.
func (s *Strategy) exitsText() string {
	var parts []string
	if s.Row.TakeProfitPct > 0 {
		parts = append(parts, fmt.Sprintf("take profit +%.1f%%", s.Row.TakeProfitPct*100))
	}
	if s.Row.StopLossPct > 0 {
		parts = append(parts, fmt.Sprintf("stop loss -%.1f%%", s.Row.StopLossPct*100))
	}
	if h := s.Row.HoldDays; h > 0 && h < strategy.NoHoldLimit {
		parts = append(parts, fmt.Sprintf("sell after %d days", h))
	}
	return strings.Join(parts, ", ")
}

// regimeSymbol is the upper-cased symbol whose Markov state gates the hold, "" for none.
func (s *Strategy) regimeSymbol() string {
	return strings.ToUpper(strings.TrimSpace(s.Row.RegimeSymbol))
}

func (s *Strategy) RequiredSymbols() []string { return []string{s.Row.Symbol} }

func (s *Strategy) UsesTotalReturn() bool { return s.Row.TotalReturn > 0 }

func (s *Strategy) DefaultConfig() strategy.StrategyConfig {
	cfg := strategy.StrategyConfig{
		ID:                 s.ID(),
		Name:               s.Name(),
		Description:        s.Description(),
		Benchmark:          "VOO",
		TradeSymbol:        strings.ToUpper(strings.TrimSpace(s.Row.Symbol)),
		TargetPct:          999.0,  // Never exit via profit target
		StopLossPct:        0.0001, // Never exit via stop loss
		HoldingWindow:      99999,  // Never exit via time limit
		PositionCap:        1,
		AllocationPct:      s.Row.AllocationPct,
		CashYieldAnnual:    s.Row.CashYield,
		SlippagePct:        s.Row.SlippagePct,
		CommissionPerShare: 0.0001,
	}
	if s.Row.TrailingStopPct > 0 {
		// A bailing row keeps the setup of the old hold_bail family so its results do not move.
		cfg.Benchmark = "SPY"
		cfg.PositionSizing = "fixed_pct"
		cfg.StopLossPct = 0.001
		cfg.UseTrailingStop = true
		cfg.TrailingStopPct = s.Row.TrailingStopPct
		cfg.ReentryCooldownDays = 1 // a 1-day breath after an exit prevents a same-day re-whipsaw
	}
	return s.applyExits(cfg)
}

// applyExits writes the row's take-profit, stop-loss and hold into cfg. A value
// of 0 (hold: 0 or 99999) is not set and leaves the hold's own never-exit
// setting alone, so a row without exits behaves as it always did.
func (s *Strategy) applyExits(cfg strategy.StrategyConfig) strategy.StrategyConfig {
	if tp := s.Row.TakeProfitPct; tp > 0 {
		cfg.TargetPct = 1 + tp
		cfg.TakeProfitPct = tp
	}
	if sl := s.Row.StopLossPct; sl > 0 {
		cfg.StopLossPct = 1 - sl
	}
	if h := s.Row.HoldDays; h > 0 && h < strategy.NoHoldLimit {
		cfg.HoldingWindow = h
	}
	return cfg
}

// PipelineDir is the directory of .sql files that calculates this strategy's
// signals; `backtest stale` compares its newest file to a result's time.
func (s *Strategy) PipelineDir() string { return filepath.Join(appenv.Folder(), pipelineDir) }

func (s *Strategy) Validate() error {
	r := s.Row
	switch {
	case r.TakeProfitPct < 0:
		return fmt.Errorf("take_profit_pct must not be negative, got %v", r.TakeProfitPct)
	case r.StopLossPct < 0 || r.StopLossPct >= 1:
		return fmt.Errorf("stop_loss_pct must be in [0, 1), got %v", r.StopLossPct)
	case r.HoldDays < 0:
		return fmt.Errorf("hold_days must not be negative, got %d", r.HoldDays)
	}
	return strategy.ValidateConfig(s.DefaultConfig())
}

func (s *Strategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// GenerateSignals runs the hold_strategy pipeline: an entry on the first bar of
// the run and, when Row.SMAReentryPeriod is set, on every bar closing above that
// average. Row.TrailingStopPct, when set, is the simulator's exit.
func (s *Strategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	cfg := s.DefaultConfig()
	period := max(s.Row.SMAReentryPeriod, 0)
	regime := s.regimeSymbol()
	regimeDB, regimeOn := ":memory:", "0"
	if regime != "" {
		trained, ok := markov_strategy.ModelLastDate(appenv.MarkovDB(), regime)
		if !ok {
			log.Printf("hold_strategy %s: no trained Markov model for %s in %s; run `train markov -symbols %s`", s.ID(), regime, appenv.MarkovDB(), regime)
			return nil
		}
		if market, err := storage.SymbolLastDate(s.marketDBPath, regime); err == nil && market > trained {
			log.Printf("hold_strategy %s: the model for %s was trained through %s but the market data runs to %s; run `train markov -symbols %s`", s.ID(), regime, trained, market, regime)
		}
		regimeDB, regimeOn = appenv.MarkovDB(), "1"
		period = 0 // the regime decides every entry
	}
	cfg.SQLParams = map[string]string{
		"TOTAL_RETURN":  totalReturnFlag(s.Row.TotalReturn > 0 && s.reinvest),
		"SMA_PERIOD":    strconv.Itoa(period),
		"SMA_PRECEDING": strconv.Itoa(max(period-1, 0)),
		"REGIME_ON":     regimeOn,
		"REGIME_SYMBOL": regime,
		"REGIME_DB":     regimeDB,
	}
	return strategy.RunPipeline(s.ID(), s.Name(), s.Description(), pipelineDir, cfg, s.marketDBPath, s.calcDBPath, "market", barsBySymbol)
}

// Prepare trains the regime symbol's Markov model through the latest bar when the
// saved one is behind, as a markov strategy does before a live scan. A row with
// no regime has nothing to prepare.
func (s *Strategy) Prepare(ctx context.Context, marketDB string) error {
	sym := s.regimeSymbol()
	if sym == "" {
		return nil
	}
	return (&markov_strategy.Strategy{Row: refdb.MarkovStrategy{SignalSymbol: sym}}).Prepare(ctx, marketDB)
}

// SetReinvestDividends is called by the runner before GenerateSignals: a total
// return run simulates on dividend-adjusted prices only when dividends are reinvested.
func (s *Strategy) SetReinvestDividends(reinvest bool) { s.reinvest = reinvest }

func totalReturnFlag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// ParameterSpace is the exit grid: take-profit, stop-loss and hold from
// strategy_family_param, with the row's own values on each axis so the row is a
// grid point. The entries are fixed, so only exits are searched. A regime row
// sets its own hold per entry, so its hold axis is the row's value alone.
func (s *Strategy) ParameterSpace() strategy.ParameterSpace {
	ax := strategy.LoadFamilyAxes("hold")
	hold := strategy.HoldLimit(s.Row.HoldDays)
	holdDays := strategy.UnionInts(ax.Ints("hold_days"), []int{hold})
	if s.regimeSymbol() != "" {
		holdDays = []int{hold}
	}
	return strategy.ParameterSpace{
		StrategyID:   s.ID(),
		StrategyName: s.Name(),
		Description:  s.Description(),
		Symbols:      []string{s.Row.Symbol},
		SignalSymbol: s.Row.Symbol,
		Direction:    "long",
		FixedEntries: true,
		SignalDays:   []int{1},
		HoldDays:     holdDays,
		TakeProfits:  strategy.UnionFloats(ax.Nums["take_profit_pct"], []float64{s.Row.TakeProfitPct}),
		StopLosses:   strategy.UnionFloats(ax.Nums["stop_loss_pct"], []float64{s.Row.StopLossPct}),
		Regimes:      []string{"All Regimes"},
		Allocations:  []float64{s.Row.AllocationPct},
		CashYield:    s.Row.CashYield,
		Baseline: strategy.BaselineParams{
			HoldDays:   hold,
			TakeProfit: s.Row.TakeProfitPct,
			StopLoss:   s.Row.StopLossPct,
			Allocation: s.Row.AllocationPct,
		},
	}
}

// NewFamily returns the hold_strategy family reading the reference DB at path().
// Rows are not registered one by one: a row is read and built when
// strategy.Get asks for its id.
func NewFamily(path func() string) *strategy.RowFamily {
	return &strategy.RowFamily{
		FamilyName: "hold",
		Table:      "hold_strategy",
		Path:       path,
		Build: func(db *sqlx.DB, id string) (strategy.Strategy, bool, error) {
			row, ok, err := refdb.HoldStrategyByID(db, id)
			if err != nil || !ok {
				return nil, false, err
			}
			if err := (&Strategy{Row: row}).Validate(); err != nil {
				return nil, false, err
			}
			return &Strategy{Row: row}, true, nil
		},
	}
}

// Register adds the hold family, reading refdb.DefaultPath. It reads no rows.
func Register() {
	RegisterFrom(refdb.DefaultPath)
}

// RegisterFrom adds the hold family reading the reference DB at path,
// replacing any earlier hold family (tests point it at a temp DB).
func RegisterFrom(path string) {
	strategy.RegisterFamily(NewFamily(func() string { return path }))
}
