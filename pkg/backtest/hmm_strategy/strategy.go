// Package hmm_strategy is the hmm strategy family: trade a symbol from the regime the hidden
// Markov model of another (or the same) symbol is in. The model is trained by `train hmm` and
// read here, never refit. Its regimes are volatility regimes, so a rule works with the stressed
// regime, found by volatility and not by state number.
package hmm_strategy

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/dailybars"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

const pipelineDir = "sql/strategies/hmm_strategy"

// DefaultSymbol is the symbol of the one model that exists today. A row's hmm_symbol and
// trade_symbol default to it.
const DefaultSymbol = "QQQ"

// The two entry rules a row can use.
const (
	// RuleCalmFilter is long on every session whose stressed-regime probability is under stress_prob.
	RuleCalmFilter = "calm_filter"
	// RuleCalmDown is long on the session the stressed-regime probability falls back under
	// stress_prob after a session at or over it.
	RuleCalmDown = "calm_down"
	// RuleNormalFilter is long on every session whose most likely regime is the middle volatility
	// rank (neither the calmest nor the most stressed). It ignores stress_prob.
	RuleNormalFilter = "normal_filter"
	// RuleStressFilter is long on every session whose stressed-regime probability is at or over
	// stress_prob. It is for funds that hold up or rise when the model's symbol is stressed.
	RuleStressFilter = "stress_filter"
)

// Strategy is one hmm_strategy row, runnable by backtest, gridsearch, scoreboard and livescan.
type Strategy struct {
	Row          refdb.HMMStrategy
	marketDBPath string
	calcDBPath   string
	PipelineDir  string
}

// ID is the table's id column.
func (s *Strategy) ID() string { return s.Row.ID }

// Family is the strategy family, which names its result database.
func (s *Strategy) Family() string { return "hmm" }

// Name is the table's name column.
func (s *Strategy) Name() string { return s.Row.Name }

func (s *Strategy) hmmSymbol() string {
	if sym := strings.ToUpper(strings.TrimSpace(s.Row.HMMSymbol)); sym != "" {
		return sym
	}
	return DefaultSymbol
}

func (s *Strategy) tradeSymbol() string {
	if sym := strings.ToUpper(strings.TrimSpace(s.Row.TradeSymbol)); sym != "" {
		return sym
	}
	return s.hmmSymbol()
}

// Description summarizes the strategy.
func (s *Strategy) Description() string {
	rule := "while the " + s.hmmSymbol() + " model's stressed regime is under"
	switch s.Row.Rule {
	case RuleCalmDown:
		rule = "when the " + s.hmmSymbol() + " model's stressed regime falls back under"
	case RuleStressFilter:
		return fmt.Sprintf("Buy %s the next session open while the %s model's stressed regime is at or over %.0f%% probability. Hold %dd, TP +%.1f%%, SL -%.1f%%.",
			s.tradeSymbol(), s.hmmSymbol(), s.Row.StressProb*100, s.Row.HoldDays, s.Row.TakeProfitPct*100, s.Row.StopLossPct*100)
	case RuleNormalFilter:
		return fmt.Sprintf("Buy %s the next session open while the %s model's most likely regime is the middle (normal) volatility one. Hold %dd, TP +%.1f%%, SL -%.1f%%.",
			s.tradeSymbol(), s.hmmSymbol(), s.Row.HoldDays, s.Row.TakeProfitPct*100, s.Row.StopLossPct*100)
	}
	return fmt.Sprintf("Buy %s the next session open %s %.0f%% probability. Hold %dd, TP +%.1f%%, SL -%.1f%%.",
		s.tradeSymbol(), rule, s.Row.StressProb*100, s.Row.HoldDays, s.Row.TakeProfitPct*100, s.Row.StopLossPct*100)
}

// Validate checks the row.
func (s *Strategy) Validate() error { return ValidateRow(s.Row) }

// RequiredSymbols is the model symbol and the traded symbol.
func (s *Strategy) RequiredSymbols() []string {
	sig, tr := s.hmmSymbol(), s.tradeSymbol()
	if sig == tr {
		return []string{sig}
	}
	return []string{sig, tr}
}

// MinHistoryBars is the bars a live scan needs per symbol. The regime comes from the saved model
// and Prepare, not from these bars, which only price the order.
func (s *Strategy) MinHistoryBars() int { return 25 }

// Prepare brings the saved model up to the last completed session, unless it is already there. A
// live scan calls it each session: it downloads the symbol's daily bars and runs the filter with
// the parameters frozen at the model's trained_through, the same way an out-of-sample backtest
// scores the sessions after its cutoff. It never refits and never creates a model: a symbol with
// none needs `train hmm`. The model file's last date is the only thing it consults, so a caller
// that already holds predictions for newer sessions (the orchestrator loads them from its
// database before the scan) makes Prepare return without a download; the strategy itself needs no
// database and works from the bundled model alone.
func (s *Strategy) Prepare(ctx context.Context, marketDB string) error {
	sym := s.hmmSymbol()
	modelDB := appenv.HMMDB()
	trained, ok := ModelLastDate(modelDB, sym)
	if !ok {
		return fmt.Errorf("no hidden Markov model for %s in %s: run `train hmm -symbols %s`", sym, modelDB, sym)
	}
	done := calendar.LastCompletedSession(time.Now()).Format("2006-01-02")
	if trained >= done {
		return nil
	}
	barsDB := appenv.DailyBarsDB()
	if _, err := dailybars.Run(ctx, dailybars.Config{DB: barsDB, Symbols: []string{sym}, Now: time.Now()}, logWriter{}); err != nil {
		return fmt.Errorf("refresh %s daily bars: %w", sym, err)
	}
	if _, err := train.ExtendHMM(ctx, train.ExtendHMMConfig{BarsDB: barsDB, ModelDB: modelDB, Symbols: []string{sym}, Through: done}); err != nil {
		return fmt.Errorf("extend hmm %s: %w", sym, err)
	}
	return nil
}

// logWriter sends the daily bars download's progress lines to the log.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	log.Print(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// DefaultConfig maps every execution column. An entry buys at the next session's open: the regime
// is known only at the signal session's close.
func (s *Strategy) DefaultConfig() strategy.StrategyConfig {
	tp := s.Row.TakeProfitPct
	var target, stop float64
	if tp > 0 {
		target = 1 + tp
	}
	if s.Row.StopLossPct > 0 {
		stop = 1 - s.Row.StopLossPct
	}
	return strategy.StrategyConfig{
		ID:               s.ID(),
		Name:             s.Name(),
		Description:      s.Description(),
		Benchmark:        s.hmmSymbol(),
		TradeSymbol:      s.tradeSymbol(),
		StreakDirection:  "long",
		Regime:           s.Row.Rule,
		Timeframe:        "1d",
		PositionSizing:   "fixed_pct",
		AllocationPct:    s.Row.AllocationPct,
		TargetPct:        target,
		TakeProfitPct:    tp,
		StopLossPct:      stop,
		HoldingWindow:    strategy.HoldLimit(s.Row.HoldDays),
		PositionCap:      1,
		CashYieldAnnual:  s.Row.CashYield,
		SlippagePct:      s.Row.SlippagePct,
		NextDayOpenEntry: true,
		SQLParams: map[string]string{
			"RULE":        s.Row.Rule,
			"STRESS_PROB": fmt.Sprintf("%g", s.Row.StressProb),
		},
	}
}

// ParameterSpace is the exit grid gridsearch tries: hold, take-profit and stop, from
// strategy_family_param, with the row's values included. The entries come from the saved model,
// run once, so only exits are searched.
func (s *Strategy) ParameterSpace() strategy.ParameterSpace {
	cfg := s.DefaultConfig()
	ax := strategy.LoadFamilyAxes("hmm")
	return strategy.ParameterSpace{
		StrategyID:   s.ID(),
		StrategyName: s.Name(),
		Description:  s.Description(),
		Symbols:      []string{cfg.TradeSymbol},
		SignalSymbol: cfg.Benchmark,
		Direction:    "long",
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

// GenerateSignals reads the symbol's saved model and turns it into entries with the shared SQL
// pipeline. It never trains: a symbol with no model produces no signals and says how to train one.
func (s *Strategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	if s.marketDBPath == "" || s.calcDBPath == "" {
		log.Printf("hmm_strategy %s: no signals: market and calc database paths are not set (SetDatabases was not called)", s.ID())
		return nil
	}
	sym := s.hmmSymbol()
	trained, ok := ModelLastDate(appenv.HMMDB(), sym)
	if !ok {
		log.Printf("hmm_strategy %s: no hidden Markov model for %s in %s; run `train hmm -symbols %s`", s.ID(), sym, appenv.HMMDB(), sym)
		return nil
	}
	done := calendar.LastCompletedSession(time.Now()).Format("2006-01-02")
	if market, err := storage.SymbolLastDate(s.marketDBPath, s.tradeSymbol()); err == nil && market > trained && trained < done {
		log.Printf("hmm_strategy %s: the model for %s runs to %s but the market data runs to %s; Prepare (live scan) or `train hmm extend` brings it up to date", s.ID(), sym, trained, market)
	}
	dir := s.PipelineDir
	if dir == "" {
		dir = pipelineDir
	}
	return strategy.RunPipeline(s.ID(), s.Name(), s.Description(), dir, s.DefaultConfig(), s.marketDBPath, s.calcDBPath, "market", barsBySymbol)
}

// ModelLastDate returns the last date the model for symbol has a prediction for, and whether a
// model exists. The file is opened read-only and never created.
func ModelLastDate(path, symbol string) (string, bool) {
	db, err := storage.OpenSQLiteReadOnly(path)
	if err != nil {
		return "", false
	}
	defer db.Close()
	var last string
	if err := db.Get(&last, "SELECT last_date FROM hmm_model_meta WHERE symbol = ?", symbol); err != nil {
		return "", false
	}
	return last, true
}

// ValidateRow reports why an hmm_strategy row cannot be registered.
func ValidateRow(row refdb.HMMStrategy) error {
	if strings.TrimSpace(row.ID) == "" {
		return fmt.Errorf("id is empty")
	}
	if reservedID(row.ID) {
		return fmt.Errorf("id %q is reserved", row.ID)
	}
	for _, c := range []struct{ name, v string }{{"hmm_symbol", row.HMMSymbol}, {"trade_symbol", row.TradeSymbol}} {
		if strings.TrimSpace(c.v) == "" {
			return fmt.Errorf("%s is empty", c.name)
		}
		if _, ok := strategy.StreakSymbol(strings.TrimSpace(c.v)); !ok {
			return fmt.Errorf("%s %q is not a plain ticker", c.name, c.v)
		}
	}
	if row.Rule != RuleCalmFilter && row.Rule != RuleCalmDown && row.Rule != RuleNormalFilter && row.Rule != RuleStressFilter {
		return fmt.Errorf("rule %q must be %s, %s, %s or %s", row.Rule, RuleCalmFilter, RuleCalmDown, RuleNormalFilter, RuleStressFilter)
	}
	if row.StressProb <= 0 || row.StressProb >= 1 {
		return fmt.Errorf("stress_prob %v must be between 0 and 1", row.StressProb)
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
	return nil
}

func reservedID(id string) bool {
	n := strings.ToLower(id)
	n = strings.ReplaceAll(n, "-", "")
	n = strings.ReplaceAll(n, "_", "")
	n = strings.ReplaceAll(n, " ", "")
	return n == "hmmstrategy"
}

// NewFamily returns the hmm_strategy family reading the reference DB at path(). Rows are not
// registered one by one: a row is read and built when strategy.Get asks for its id.
func NewFamily(path func() string) *strategy.RowFamily {
	return &strategy.RowFamily{
		FamilyName: "hmm",
		Table:      "hmm_strategy",
		Path:       path,
		Build: func(db *sqlx.DB, id string) (strategy.Strategy, bool, error) {
			row, ok, err := refdb.HMMStrategyByID(db, id)
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

// Register adds the hmm family, reading refdb.DefaultPath. It reads no rows.
func Register() {
	RegisterFrom(refdb.DefaultPath)
}

// RegisterFrom adds the hmm family reading the reference DB at path, replacing any earlier hmm
// family (tests point it at a temp DB).
func RegisterFrom(path string) {
	strategy.RegisterFamily(NewFamily(func() string { return path }))
}
