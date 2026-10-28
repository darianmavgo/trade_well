package markov_strategy

import (
	"context"
	"fmt"
	"github.com/jmoiron/sqlx"
	"log"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

const pipelineDir = "sql/strategies/markov_model"

// Strategy is one markov_strategy row, runnable by backtest, gridsearch,
// scoreboard, livescan, and strateval.
type Strategy struct {
	Row          refdb.MarkovStrategy
	marketDBPath string
	calcDBPath   string
	PipelineDir  string
}

// ID is the table's id column.
func (s *Strategy) ID() string { return s.Row.ID }

// Family is the strategy family, which names its result database.
func (s *Strategy) Family() string { return "markov" }

// Name is the table's name column.
func (s *Strategy) Name() string { return s.Row.Name }

// Description summarizes the strategy.
func (s *Strategy) Description() string {
	return fmt.Sprintf("Trade %s %s when %s enters %s state. Hold %dd, TP +%.1f%%, SL -%.1f%%.",
		s.Row.TradeSymbol, s.Row.Direction, s.Row.SignalSymbol, s.Row.TargetState,
		s.Row.HoldDays, s.Row.TakeProfitPct*100, s.Row.StopLossPct*100)
}

// Validate checks the row.
func (s *Strategy) Validate() error { return ValidateRow(s.Row) }

// RequiredSymbols is the signal symbol and the traded symbol.
func (s *Strategy) RequiredSymbols() []string {
	sig := strings.ToUpper(strings.TrimSpace(s.Row.SignalSymbol))
	tr := strings.ToUpper(strings.TrimSpace(s.Row.TradeSymbol))
	if sig == tr {
		return []string{sig}
	}
	return []string{sig, tr}
}

// MinHistoryBars is the bars a live scan needs per symbol. The saved model already holds its
// predictions and Prepare only scores the new sessions, from their 20-bar return, so about a
// month of closes is enough. Fitting a model needs far more (train markov), and that is a
// separate, deliberate run.
func (s *Strategy) MinHistoryBars() int {
	return 25
}

// Prepare brings the saved model up to the last completed session, unless it is already there.
// A live scan calls it each session, because the model holds one prediction per date and has none
// for a session it was not trained through. It never refits an existing model: it scores the new
// sessions with the counts frozen at the model's trained_through, which is how an out-of-sample
// backtest scores the sessions after its cutoff, so live and out-of-sample agree. A symbol with
// no model is an error: a model fitted on whatever bars the instance holds is not the one that
// was backtested, so training is a deliberate `train markov` and an export, never a side effect.
func (s *Strategy) Prepare(ctx context.Context, marketDB string) error {
	sym := strings.ToUpper(strings.TrimSpace(s.Row.SignalSymbol))
	last, err := storage.SymbolLastDate(marketDB, sym)
	if err != nil {
		return fmt.Errorf("last bar of %s in %s: %w", sym, marketDB, err)
	}
	if last == "" {
		return fmt.Errorf("no daily bars for %s in %s", sym, marketDB)
	}
	// A bar for a session still open is provisional and must not enter the saved model.
	done := calendar.LastCompletedSession(time.Now()).Format("2006-01-02")
	if last > done {
		last = done
	}
	modelDB := appenv.MarkovDB()
	trained, ok := ModelLastDate(modelDB, sym)
	if ok && trained >= last {
		return nil
	}
	if ok {
		got, err := train.ExtendMarkov(ctx, train.ExtendConfig{MarketDB: marketDB, ModelDB: modelDB, Symbols: []string{sym}, Through: done})
		if err != nil {
			return fmt.Errorf("extend markov %s: %w", sym, err)
		}
		if len(got) == 0 {
			return fmt.Errorf("extend markov %s: no bars after %s in %s (the model is behind the market data)", sym, trained, marketDB)
		}
		return nil
	}
	return fmt.Errorf("no Markov model for %s in %s: train one with `train markov -symbols %s` in backtestgosqlite, then export the bundle (scripts/export_strategies.sh) so the deployed binary carries it", sym, modelDB, sym)
}

// DefaultConfig maps every execution column.
func (s *Strategy) DefaultConfig() strategy.StrategyConfig {
	sig := strings.ToUpper(strings.TrimSpace(s.Row.SignalSymbol))
	tr := strings.ToUpper(strings.TrimSpace(s.Row.TradeSymbol))
	tp := s.Row.TakeProfitPct
	var target, stop float64
	if tp > 0 {
		target = 1 + tp
	}
	if s.Row.StopLossPct > 0 {
		stop = 1 - s.Row.StopLossPct
	}
	return strategy.StrategyConfig{
		ID:                s.ID(),
		Name:              s.Name(),
		Description:       s.Description(),
		Benchmark:         sig,
		TradeSymbol:       tr,
		StreakDirection:   s.Row.Direction,
		Regime:            s.Row.TargetState,
		Timeframe:         "1d",
		PositionSizing:    "fixed_pct",
		AllocationPct:     s.Row.AllocationPct,
		TargetPct:         target,
		TakeProfitPct:     tp,
		StopLossPct:       stop,
		HoldingWindow:     strategy.HoldLimit(s.Row.HoldDays),
		PositionCap:       1,
		CashYieldAnnual:   s.Row.CashYield,
		SlippagePct:       s.Row.SlippagePct,
		NextDayLimitEntry: s.Row.NextDayLimit != 0,
	}
}

// ParameterSpace is the exit grid gridsearch tries: hold, take-profit and stop,
// from strategy_family_param, with the row's values included. The entries come
// from the saved model for the row's state, run once, so only exits are searched.
func (s *Strategy) ParameterSpace() strategy.ParameterSpace {
	cfg := s.DefaultConfig()
	ax := strategy.LoadFamilyAxes("markov")
	return strategy.ParameterSpace{
		StrategyID:   s.ID(),
		StrategyName: s.Name(),
		Description:  s.Description(),
		Symbols:      []string{cfg.TradeSymbol},
		SignalSymbol: cfg.Benchmark,
		Direction:    s.Row.Direction,
		FixedEntries: true,
		SignalDays:   []int{1},
		HoldDays:     strategy.UnionInts(ax.Ints("hold_days"), []int{strategy.HoldLimit(s.Row.HoldDays)}),
		TakeProfits:  strategy.UnionFloats(ax.Nums["take_profit_pct"], []float64{s.Row.TakeProfitPct}),
		StopLosses:   strategy.UnionFloats(ax.Nums["stop_loss_pct"], []float64{s.Row.StopLossPct}),
		Regimes:      []string{"All Regimes"},
		Allocations:  []float64{s.Row.AllocationPct},
		CashYield:    s.Row.CashYield,
		Baseline: strategy.BaselineParams{
			HoldDays:   strategy.HoldLimit(s.Row.HoldDays),
			TakeProfit: s.Row.TakeProfitPct,
			StopLoss:   s.Row.StopLossPct,
			Allocation: s.Row.AllocationPct,
		},
	}
}

// SetDatabases stores the market and calc database paths.
func (s *Strategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// GenerateSignals reads this symbol's persisted model and turns it into
// entries with the shared SQL pipeline. It never trains: a symbol with no
// trained model produces no signals and says how to train one.
func (s *Strategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	if s.marketDBPath == "" || s.calcDBPath == "" {
		log.Printf("markov_strategy %s: no signals: market and calc database paths are not set (SetDatabases was not called); the strategy only runs through the SQL pipeline", s.ID())
		return nil
	}
	dir := s.PipelineDir
	if dir == "" {
		dir = pipelineDir
	}
	if dir == pipelineDir {
		sym := strings.ToUpper(strings.TrimSpace(s.Row.SignalSymbol))
		trained, ok := ModelLastDate(appenv.MarkovDB(), sym)
		if !ok {
			log.Printf("markov_strategy %s: no trained model for %s in %s; run `train markov -symbols %s`", s.ID(), sym, appenv.MarkovDB(), sym)
			return nil
		}
		// A bar for the session still open is not expected in the model.
		done := calendar.LastCompletedSession(time.Now()).Format("2006-01-02")
		if market, err := storage.SymbolLastDate(s.marketDBPath, sym); err == nil && market > trained && trained < done {
			log.Printf("markov_strategy %s: the model for %s was trained through %s but the market data runs to %s; it is brought up to date by Prepare (live scan) or `train markov -symbols %s`", s.ID(), sym, trained, market, sym)
		}

	}
	return strategy.RunPipeline(s.ID(), s.Name(), s.Description(), dir, s.DefaultConfig(), s.marketDBPath, s.calcDBPath, "limit", barsBySymbol)
}

// ModelLastDate returns the last bar date the model for symbol was trained
// through, and whether a model exists. The file is opened read-only and never created.
func ModelLastDate(path, symbol string) (string, bool) {
	db, err := storage.OpenSQLiteReadOnly(path)
	if err != nil {
		return "", false
	}
	defer db.Close()
	var last string
	if err := db.Get(&last, "SELECT last_date FROM markov_model_meta WHERE symbol = ?", symbol); err != nil {
		return "", false
	}
	return last, true
}

// ValidateRow reports why a markov_strategy row cannot be registered.
func ValidateRow(row refdb.MarkovStrategy) error {
	if strings.TrimSpace(row.ID) == "" {
		return fmt.Errorf("id is empty")
	}
	if reservedID(row.ID) {
		return fmt.Errorf("id %q is reserved", row.ID)
	}
	if strings.TrimSpace(row.SignalSymbol) == "" {
		return fmt.Errorf("signal_symbol is empty")
	}
	if strings.TrimSpace(row.TradeSymbol) == "" {
		return fmt.Errorf("trade_symbol is empty")
	}
	if row.Direction != "long" && row.Direction != "short" {
		return fmt.Errorf("direction %q must be long or short", row.Direction)
	}
	if strings.TrimSpace(row.TargetState) == "" {
		return fmt.Errorf("target_state is empty")
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
	return n == "markovstrategy"
}

// NewFamily returns the markov_strategy family reading the reference DB at path().
// Rows are not registered one by one: a row is read and built when
// strategy.Get asks for its id.
func NewFamily(path func() string) *strategy.RowFamily {
	return &strategy.RowFamily{
		FamilyName: "markov",
		Table:      "markov_strategy",
		Path:       path,
		Build: func(db *sqlx.DB, id string) (strategy.Strategy, bool, error) {
			row, ok, err := refdb.MarkovStrategyByID(db, id)
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

// Register adds the markov family, reading refdb.DefaultPath. It reads no rows.
func Register() {
	RegisterFrom(refdb.DefaultPath)
}

// RegisterFrom adds the markov family reading the reference DB at path,
// replacing any earlier markov family (tests point it at a temp DB).
func RegisterFrom(path string) {
	strategy.RegisterFamily(NewFamily(func() string { return path }))
}
