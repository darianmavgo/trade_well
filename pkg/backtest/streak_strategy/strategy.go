package streak_strategy

import (
	"fmt"
	"github.com/jmoiron/sqlx"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

const pipelineDir = "sql/strategies/streak_strategy"

// Strategy is one streak_strategy row, runnable by backtest, gridsearch,
// scoreboard, livescan.
type Strategy struct {
	Row          refdb.StreakStrategy
	marketDBPath string
	calcDBPath   string
	// PipelineDir overrides the repo-root relative pipeline path. Tests set it
	// only when they need a specific directory; the embedded copy is used when
	// the relative path is not on disk.
	PipelineDir string
}

// ID is the table's id column.
func (s *Strategy) ID() string { return s.Row.ID }

// SQLDir is the pipeline directory that calculates this row's signals.
func (s *Strategy) SQLDir() string {
	if s.PipelineDir != "" {
		return s.PipelineDir
	}
	return pipelineDir
}

// Family is the strategy family, which names its result database.
func (s *Strategy) Family() string { return "streak" }

// Name is the table's name column.
func (s *Strategy) Name() string { return s.Row.Name }

// Description summarizes the watch symbol, the bought symbol, and the exits.
func (s *Strategy) Description() string {
	verb := "down"
	if s.Row.Direction == "rally" {
		verb = "up"
	}
	return fmt.Sprintf("Long %s when %s closes %s %d consecutive days (%s). Hold %dd, TP +%.1f%%, SL -%.1f%%.",
		s.Row.TradeSymbol, s.Row.SignalSymbol, verb, s.Row.SignalDays, s.Row.Regime,
		s.Row.HoldDays, s.Row.TakeProfitPct*100, s.Row.StopLossPct*100)
}

// Validate checks the row and the portfolio config it produces.
func (s *Strategy) Validate() error { return ValidateRow(s.Row) }

// RequiredSymbols is the watch symbol and the bought symbol.
func (s *Strategy) RequiredSymbols() []string {
	sig, _ := strategy.StreakSymbol(s.Row.SignalSymbol)
	tr, _ := strategy.StreakSymbol(s.Row.TradeSymbol)
	if sig == "" {
		sig = strings.ToUpper(strings.TrimSpace(s.Row.SignalSymbol))
	}
	if tr == "" {
		tr = strings.ToUpper(strings.TrimSpace(s.Row.TradeSymbol))
	}
	if sig == tr {
		return []string{sig}
	}
	return []string{sig, tr}
}

// MinHistoryBars is 203 when the regime uses SMA200, otherwise the streak
// window plus a few bars of slack.
func (s *Strategy) MinHistoryBars() int {
	if strings.Contains(s.Row.Regime, "SMA200") {
		return 203
	}
	n := s.Row.SignalDays + 3
	if n < 1 {
		return strategy.DefaultMinHistoryBars
	}
	return n
}

// DefaultConfig maps every execution column. Take-profit stays an offset.
// Stop-loss is converted to the multiplier StrategyConfig.StopLossPct uses
// (0.08 offset → 0.92). A zero offset means no stop and no target.
func (s *Strategy) DefaultConfig() strategy.StrategyConfig {
	sig, _ := strategy.StreakSymbol(s.Row.SignalSymbol)
	tr, _ := strategy.StreakSymbol(s.Row.TradeSymbol)
	tp := s.Row.TakeProfitPct
	var target, stop float64
	if tp > 0 {
		target = 1 + tp
	}
	if s.Row.StopLossPct > 0 {
		stop = 1 - s.Row.StopLossPct
	}
	return strategy.StrategyConfig{
		ID:              s.ID(),
		Name:            s.Name(),
		Description:     s.Description(),
		Benchmark:       sig,
		TradeSymbol:     tr,
		StreakDirection: s.Row.Direction,
		Regime:          strings.TrimSpace(s.Row.Regime),
		Timeframe:       "1d",
		PositionSizing:  "fixed_pct",
		AllocationPct:   s.Row.AllocationPct,
		TargetPct:       target,
		TakeProfitPct:   tp,
		StopLossPct:     stop,
		HoldingWindow:   strategy.HoldLimit(s.Row.HoldDays),
		PositionCap:     1,
		CashYieldAnnual: s.Row.CashYield,
		SlippagePct:     s.Row.SlippagePct,
		// Every streak row buys at the next session's open, with the take-profit
		// measured from that fill. The row's next_day_limit column is not read.
		NextDayOpenEntry: true,
		DeclineDays:      s.Row.SignalDays,
	}
}

// ParameterSpace is the grid gridsearch tries: the columns marked gridsearchable
// for the streak family in strategy_family_param, with the row's own values
// always included so the baseline is in the grid.
func (s *Strategy) ParameterSpace() strategy.ParameterSpace {
	cfg := s.DefaultConfig()
	ax := strategy.LoadFamilyAxes("streak")
	regime := strings.TrimSpace(s.Row.Regime)
	regimes := []string{regime}
	for _, t := range ax.Strs["regime"] {
		r := strings.ReplaceAll(t, "{signal_symbol}", strings.ToUpper(strings.TrimSpace(s.Row.SignalSymbol)))
		dup := false
		for _, have := range regimes {
			dup = dup || have == r
		}
		if !dup {
			regimes = append(regimes, r)
		}
	}
	return strategy.ParameterSpace{
		StrategyID:   s.ID(),
		StrategyName: s.Name(),
		Description:  s.Description(),
		Symbols:      []string{cfg.TradeSymbol},
		SignalSymbol: cfg.Benchmark,
		Direction:    s.Row.Direction,
		SignalDays:   strategy.UnionInts(ax.Ints("signal_days"), []int{s.Row.SignalDays}),
		HoldDays:     strategy.UnionInts(ax.Ints("hold_days"), []int{strategy.HoldLimit(s.Row.HoldDays)}),
		TakeProfits:  strategy.UnionFloats(ax.Nums["take_profit_pct"], []float64{s.Row.TakeProfitPct}),
		StopLosses:   strategy.UnionFloats(ax.Nums["stop_loss_pct"], []float64{s.Row.StopLossPct}),
		Regimes:      regimes,
		Allocations:  []float64{s.Row.AllocationPct},
		CashYield:    s.Row.CashYield,
		Baseline: strategy.BaselineParams{
			SignalDays: s.Row.SignalDays,
			HoldDays:   strategy.HoldLimit(s.Row.HoldDays),
			TakeProfit: s.Row.TakeProfitPct,
			StopLoss:   s.Row.StopLossPct,
			Allocation: s.Row.AllocationPct,
			Regime:     regime,
		},
	}
}

// SetDatabases stores the market and calc database paths.
func (s *Strategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// GenerateSignals runs the shared SQL pipeline (sql/strategies/streak_strategy)
// in the calc database. Both database paths must be set.
func (s *Strategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	return strategy.RunPipeline(s.ID(), s.Name(), s.Description(), s.SQLDir(), s.DefaultConfig(), s.marketDBPath, s.calcDBPath, "market", barsBySymbol)
}

// ValidateRow reports why a streak_strategy row cannot be registered or promoted.
func ValidateRow(row refdb.StreakStrategy) error {
	if strings.TrimSpace(row.ID) == "" {
		return fmt.Errorf("id is empty")
	}
	if reservedID(row.ID) {
		return fmt.Errorf("id %q is reserved", row.ID)
	}
	if _, ok := strategy.StreakSymbol(row.SignalSymbol); !ok {
		return fmt.Errorf("signal_symbol %q", row.SignalSymbol)
	}
	if _, ok := strategy.StreakSymbol(row.TradeSymbol); !ok {
		return fmt.Errorf("trade_symbol %q", row.TradeSymbol)
	}
	if _, ok := strategy.StreakColumn(row.Direction); !ok {
		return fmt.Errorf("direction %q", row.Direction)
	}
	if row.SignalDays < 1 {
		return fmt.Errorf("signal_days %d", row.SignalDays)
	}
	if row.HoldDays < 0 {
		return fmt.Errorf("hold_days %d", row.HoldDays)
	}
	if row.TakeProfitPct < 0 {
		return fmt.Errorf("take_profit_pct %v", row.TakeProfitPct)
	}
	if row.StopLossPct < 0 || row.StopLossPct >= 1 {
		return fmt.Errorf("stop_loss_pct %v", row.StopLossPct)
	}
	if _, ok := strategy.RegimePredicate(row.SignalSymbol, row.Regime); !ok {
		return fmt.Errorf("regime %q", row.Regime)
	}
	if row.AllocationPct <= 0 || row.AllocationPct > 1 {
		return fmt.Errorf("allocation_pct %v", row.AllocationPct)
	}
	if row.CashYield < 0 {
		return fmt.Errorf("cash_yield %v", row.CashYield)
	}
	if row.SlippagePct < 0 {
		return fmt.Errorf("slippage_pct %v", row.SlippagePct)
	}
	if row.NextDayLimit != 0 && row.NextDayLimit != 1 {
		return fmt.Errorf("next_day_limit %d", row.NextDayLimit)
	}
	return nil
}

func reservedID(id string) bool {
	n := strings.ToLower(id)
	n = strings.ReplaceAll(n, "-", "")
	n = strings.ReplaceAll(n, "_", "")
	n = strings.ReplaceAll(n, " ", "")
	return n == "streakstrategy"
}

// NewFamily returns the streak_strategy family reading the reference DB at path().
// Rows are not registered one by one: a row is read and built when
// strategy.Get asks for its id.
func NewFamily(path func() string) *strategy.RowFamily {
	return &strategy.RowFamily{
		FamilyName: "streak",
		Table:      "streak_strategy",
		Path:       path,
		Build: func(db *sqlx.DB, id string) (strategy.Strategy, bool, error) {
			row, ok, err := refdb.StreakStrategyByID(db, id)
			if err != nil || !ok {
				return nil, false, err
			}
			if err := ValidateRow(row); err != nil {
				return nil, false, err
			}
			return &Strategy{Row: row}, true, nil
		},
	}
}

// Register adds the streak family, reading refdb.DefaultPath. It reads no rows.
func Register() {
	RegisterFrom(refdb.DefaultPath)
}

// RegisterFrom adds the streak family reading the reference DB at path,
// replacing any earlier streak family (tests point it at a temp DB).
func RegisterFrom(path string) {
	strategy.RegisterFamily(NewFamily(func() string { return path }))
}
