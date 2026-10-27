// Package price_action_reclaim_strategy is the price action reclaim family: one symbol per row.
// A row buys when a close falls under the symbol's support and a later close reclaims it, in an
// uptrend (close at or over its moving average). The stop sits just under support. The signal
// math is SQL (sql/strategies/price_action_reclaim_strategy); this package builds each row's
// config and runs it.
package price_action_reclaim_strategy

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

const pipelineDir = "sql/strategies/price_action_reclaim_strategy"

// Strategy is one price_action_reclaim_strategy row, runnable by backtest, gridsearch,
// scoreboard, livescan and strateval.
type Strategy struct {
	Row          refdb.PriceActionReclaimStrategy
	marketDBPath string
	calcDBPath   string
}

// ID is the table's id column.
func (s *Strategy) ID() string { return s.Row.ID }

// Family is the strategy family, which names its result database.
func (s *Strategy) Family() string { return "price_action_reclaim" }

// Name is the table's name column.
func (s *Strategy) Name() string { return s.Row.Name }

// Description summarizes the setup and the exits.
func (s *Strategy) Description() string {
	return fmt.Sprintf("Buys %s when a close reclaims support (the lowest low %d to %d bars back) after closing under it, "+
		"while the close is at or over its %d-bar average. Stop %.1f%% under support, skipped when more than %.0f%% away. "+
		"TP +%.0f%%, hold %dd.",
		s.symbol(), s.Row.SupportBars, s.Row.ReclaimGap, s.Row.SMAPeriod,
		s.Row.StopCushionPct*100, s.Row.MaxStopPct*100, s.Row.TakeProfitPct*100, s.Row.HoldDays)
}

func (s *Strategy) symbol() string { return strings.ToUpper(strings.TrimSpace(s.Row.Symbol)) }

// RequiredSymbols is the traded symbol.
func (s *Strategy) RequiredSymbols() []string { return []string{s.symbol()} }

// MinHistoryBars covers the average and the support window, plus slack.
func (s *Strategy) MinHistoryBars() int {
	return max(s.Row.SMAPeriod, s.Row.SupportBars+s.Row.ReclaimGap) + 3
}

// DefaultConfig maps the row's execution columns. The stop is not here: the SQL sets each
// entry's stop under its own support as an absolute price.
func (s *Strategy) DefaultConfig() strategy.StrategyConfig {
	return strategy.StrategyConfig{
		ID:                 s.ID(),
		Name:               s.Name(),
		Description:        s.Description(),
		Benchmark:          "SPY",
		TradeSymbol:        s.symbol(),
		Timeframe:          "1d",
		PositionSizing:     "fixed_pct",
		AllocationPct:      s.Row.AllocationPct,
		TakeProfitPct:      s.Row.TakeProfitPct,
		HoldingWindow:      strategy.HoldLimit(s.Row.HoldDays),
		PositionCap:        1,
		CashYieldAnnual:    s.Row.CashYield,
		SlippagePct:        s.Row.SlippagePct,
		CommissionPerShare: 0.0001,
	}
}

// PipelineDir is the directory of .sql files that calculates this strategy's signals;
// `backtest stale` compares its newest file to a result's time.
func (s *Strategy) PipelineDir() string { return filepath.Join(appenv.Folder(), pipelineDir) }

// Validate checks the row and the portfolio config it produces.
func (s *Strategy) Validate() error {
	r := s.Row
	switch {
	case strings.TrimSpace(r.ID) == "":
		return fmt.Errorf("id is empty")
	case !validSymbol(s.symbol()):
		return fmt.Errorf("symbol %q", r.Symbol)
	case r.SMAPeriod < 2:
		return fmt.Errorf("sma_period must be at least 2, got %d", r.SMAPeriod)
	case r.SupportBars < 1 || r.ReclaimGap < 1 || r.ReclaimGap >= r.SupportBars:
		return fmt.Errorf("support_bars %d and reclaim_gap %d: want 1 <= reclaim_gap < support_bars", r.SupportBars, r.ReclaimGap)
	case r.StopCushionPct < 0 || r.StopCushionPct >= 1:
		return fmt.Errorf("stop_cushion_pct must be in [0, 1), got %v", r.StopCushionPct)
	case r.MaxStopPct <= 0 || r.MaxStopPct >= 1:
		return fmt.Errorf("max_stop_pct must be in (0, 1), got %v", r.MaxStopPct)
	case r.TakeProfitPct < 0:
		return fmt.Errorf("take_profit_pct must not be negative, got %v", r.TakeProfitPct)
	case r.HoldDays < 0:
		return fmt.Errorf("hold_days must not be negative, got %d", r.HoldDays)
	}
	return strategy.ValidateConfig(s.DefaultConfig())
}

// validSymbol accepts the characters a ticker has, so the symbol is safe to put in the SQL.
func validSymbol(sym string) bool {
	_, ok := strategy.StreakSymbol(sym)
	return ok
}

// SetDatabases is called by the runner with the market and the fresh calculation database.
func (s *Strategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// GenerateSignals runs the price_action_reclaim_strategy pipeline over the row's symbol.
func (s *Strategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	cfg := s.DefaultConfig()
	cfg.SQLParams = map[string]string{
		"SMA_PRECEDING":     strconv.Itoa(s.Row.SMAPeriod - 1),
		"SUPPORT_BARS":      strconv.Itoa(s.Row.SupportBars),
		"RECLAIM_GAP":       strconv.Itoa(s.Row.ReclaimGap),
		"STOP_CUSHION_MULT": strconv.FormatFloat(1-s.Row.StopCushionPct, 'f', 6, 64),
		"MAX_STOP_RATIO":    strconv.FormatFloat(1-s.Row.MaxStopPct, 'f', 6, 64),
	}
	return strategy.RunPipeline(s.ID(), s.Name(), s.Description(), pipelineDir, cfg, s.marketDBPath, s.calcDBPath, "market", barsBySymbol)
}

// NewFamily returns the price_action_reclaim_strategy family reading the reference DB at path().
// Rows are not registered one by one: a row is read and built when strategy.Get asks for its id.
func NewFamily(path func() string) *strategy.RowFamily {
	return &strategy.RowFamily{
		FamilyName: "price_action_reclaim",
		Table:      "price_action_reclaim_strategy",
		Path:       path,
		Build: func(db *sqlx.DB, id string) (strategy.Strategy, bool, error) {
			row, ok, err := refdb.PriceActionReclaimStrategyByID(db, id)
			if err != nil || !ok {
				return nil, false, err
			}
			s := &Strategy{Row: row}
			if err := s.Validate(); err != nil {
				return nil, false, err
			}
			return s, true, nil
		},
	}
}

// Register adds the family, reading refdb.DefaultPath. It reads no rows.
func Register() { RegisterFrom(refdb.DefaultPath) }

// RegisterFrom adds the family reading the reference DB at path, replacing any earlier one
// (tests point it at a temp DB).
func RegisterFrom(path string) {
	strategy.RegisterFamily(NewFamily(func() string { return path }))
}
