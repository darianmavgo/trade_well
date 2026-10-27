package strategy

import (
	"fmt"
	"math"
	"sort"
)

// ParamSet represents a key-value dictionary of numeric strategy parameters.
type ParamSet map[string]float64

// Get retrieves a float parameter with a fallback default.
func (p ParamSet) Get(key string, defaultVal float64) float64 {
	if v, ok := p[key]; ok {
		return v
	}
	return defaultVal
}

// GetInt retrieves an integer parameter with a fallback default.
func (p ParamSet) GetInt(key string, defaultVal int) int {
	if v, ok := p[key]; ok {
		return int(v)
	}
	return defaultVal
}

// ParamRange defines a parameter range for optimization / grid search.
type ParamRange struct {
	Name string  `json:"name"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Step float64 `json:"step"`
}

// Values generates all parameter values defined by this range.
func (r ParamRange) Values() []float64 {
	if r.Step <= 0 {
		return []float64{r.Min}
	}
	var vals []float64
	for v := r.Min; v <= r.Max+1e-9; v += r.Step {
		vals = append(vals, v)
	}
	return vals
}

// ValidateConfig performs standard validation checks on StrategyConfig.
func ValidateConfig(cfg StrategyConfig) error {
	if cfg.HoldingWindow < 1 {
		return fmt.Errorf("holding window must be >= 1, got %d", cfg.HoldingWindow)
	}
	if cfg.PositionCap < 1 {
		return fmt.Errorf("position cap must be >= 1, got %d", cfg.PositionCap)
	}
	if cfg.AllocationPct <= 0 || cfg.AllocationPct > 1.0 {
		return fmt.Errorf("allocation percentage must be between 0 and 1.0, got %f", cfg.AllocationPct)
	}
	return nil
}

// BaselineParams records the default/baked-in configuration of a strategy.
type BaselineParams struct {
	SignalDays int     `json:"signal_days"`
	HoldDays   int     `json:"hold_days"`
	TakeProfit float64 `json:"take_profit"`
	StopLoss   float64 `json:"stop_loss"`
	Allocation float64 `json:"allocation"`
	Regime     string  `json:"regime"`
	Period     string  `json:"period,omitempty"`      // period sweeps: the row's own period
	EntryLimit float64 `json:"entry_limit,omitempty"` // period sweeps: the row's own buy limit fraction (0 = none)
}

// ParameterSpace defines the parameter dimensions and search bounds for a strategy during grid search.
type ParameterSpace struct {
	StrategyID   string         `json:"strategy_id"`
	StrategyName string         `json:"strategy_name"`
	Description  string         `json:"description"`
	Symbols      []string       `json:"symbols"`       // Tradable symbols
	SignalSymbol string         `json:"signal_symbol"` // Symbol for signal generation (e.g. GLD, VOO)
	Direction    string         `json:"direction"`     // "drop" (reversal bounce) or "rally" (breakout / short)
	SignalDays   []int          `json:"signal_days"`
	HoldDays     []int          `json:"hold_days"`
	TakeProfits  []float64      `json:"take_profits"`
	StopLosses   []float64      `json:"stop_losses"`
	Regimes      []string       `json:"regimes"`
	Allocations  []float64      `json:"allocations"`
	CashYield    float64        `json:"cash_yield"`
	Baseline     BaselineParams `json:"baseline"`
	// FixedEntries means the strategy's own signal pipeline decides the entries
	// and only the exits (hold, take-profit, stop) are searched. The strategy
	// then runs once and its signals are repriced per grid point.
	FixedEntries bool `json:"fixed_entries,omitempty"`
	// Periods are calendar periods (1d, 1w, 1m, 1q, 1y) the strategy's
	// own pipeline is rerun for, one result set each. Only a strategy that
	// implements PeriodVariants sets it. The strategy trades the whole market
	// (or its candidate list), so Symbols is not an axis.
	Periods []string `json:"periods,omitempty"`
	// EntryLimits are buy-limit fractions of the previous close (1.0 is the close,
	// 0.9 is 10% under; 0 is no limit) a period strategy is repriced for. Only a
	// strategy with period variants sets it.
	EntryLimits []float64 `json:"entry_limits,omitempty"`
}

// Perms is the number of grid points: the product of the axes. A period sweep
// has no per-symbol or per-signal-day axis.
func (p ParameterSpace) Perms() int {
	n := len(p.HoldDays) * len(p.TakeProfits) * len(p.StopLosses) * len(p.Regimes) * len(p.Allocations)
	if len(p.Periods) > 0 {
		return n * len(p.Periods) * max(len(p.EntryLimits), 1)
	}
	return n * len(p.Symbols) * len(p.SignalDays)
}

// PeriodVariants is implemented by a strategy whose signals depend on a calendar
// period, so gridsearch can run the same strategy over each period.
type PeriodVariants interface {
	PeriodVariant(period string) Strategy
}

// EntriesFixed reports whether a search varies exits only (see FixedEntries).
// "tree_bounce" is the older spelling of the same thing.
func (p ParameterSpace) EntriesFixed() bool {
	return p.FixedEntries || p.Direction == "tree_bounce"
}

// ParameterSpaceProvider is optionally implemented by strategies that declare their custom search space.
type ParameterSpaceProvider interface {
	ParameterSpace() ParameterSpace
}

// AssessParameterSpace inspects a strategy to determine its baked-in parameters
// and generate a tailored parameter space for grid search optimization.
func AssessParameterSpace(s Strategy) ParameterSpace {
	if p, ok := s.(ParameterSpaceProvider); ok {
		return p.ParameterSpace()
	}

	cfg := s.DefaultConfig()

	var symbols []string
	if req, ok := s.(RequiredSymbolsProvider); ok && len(req.RequiredSymbols()) > 0 {
		symbols = req.RequiredSymbols()
	} else if cfg.Benchmark != "" {
		symbols = []string{cfg.Benchmark}
	} else {
		symbols = []string{"VOO"}
	}

	signalSym := symbols[0]
	if cfg.Benchmark != "" {
		signalSym = cfg.Benchmark
	}

	tp := cfg.TakeProfitPct
	if tp == 0 && cfg.TargetPct > 1.0 {
		tp = cfg.TargetPct - 1.0
	}

	sl := cfg.StopLossPct
	if sl > 0.5 && sl < 1.0 {
		sl = 1.0 - sl
	}

	hold := cfg.HoldingWindow
	if hold <= 0 {
		hold = 8
	}

	alloc := cfg.AllocationPct
	if alloc <= 0 {
		alloc = 0.65
	}

	// Build holding windows around the baked-in default
	holdSet := map[int]bool{2: true, 4: true, 6: true, 8: true, 10: true, 12: true, 15: true, hold: true}
	var holdDays []int
	for h := range holdSet {
		holdDays = append(holdDays, h)
	}
	sort.Ints(holdDays)

	// Build TP options around the baked-in default
	tpList := []float64{0.0, 0.02, 0.03, 0.04, 0.05, 0.06, 0.07, 0.08, 0.10}
	if tp > 0 {
		hasTP := false
		for _, t := range tpList {
			if math.Abs(t-tp) < 1e-4 {
				hasTP = true
				break
			}
		}
		if !hasTP {
			tpList = append(tpList, tp)
			sort.Float64s(tpList)
		}
	}

	// Build SL options around the baked-in default
	slList := []float64{0.0, 0.02, 0.03, 0.05, 0.07}
	if sl > 0 {
		hasSL := false
		for _, s := range slList {
			if math.Abs(s-sl) < 1e-4 {
				hasSL = true
				break
			}
		}
		if !hasSL {
			slList = append(slList, sl)
			sort.Float64s(slList)
		}
	}

	regimes := []string{"All Regimes", fmt.Sprintf("%s>=SMA200", signalSym), fmt.Sprintf("%s<SMA200", signalSym)}

	return ParameterSpace{
		StrategyID:   s.ID(),
		StrategyName: s.Name(),
		Description:  s.Description(),
		Symbols:      symbols,
		SignalSymbol: signalSym,
		Direction:    "drop",
		SignalDays:   []int{2, 3, 4, 5},
		HoldDays:     holdDays,
		TakeProfits:  tpList,
		StopLosses:   slList,
		Regimes:      regimes,
		Allocations:  []float64{alloc},
		CashYield:    cfg.CashYieldAnnual,
		Baseline: BaselineParams{
			SignalDays: 3,
			HoldDays:   hold,
			TakeProfit: tp,
			StopLoss:   sl,
			Allocation: alloc,
			Regime:     "All Regimes",
		},
	}
}

// UnionFloats returns base with every value of extra added, sorted, without
// duplicates (within 1e-9).
func UnionFloats(base, extra []float64) []float64 {
	out := append([]float64(nil), base...)
	for _, e := range extra {
		dup := false
		for _, b := range out {
			if math.Abs(b-e) < 1e-9 {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, e)
		}
	}
	sort.Float64s(out)
	return out
}

// UnionInts is UnionFloats for ints.
func UnionInts(base, extra []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range append(append([]int(nil), base...), extra...) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

// UnionStrings returns base followed by the values of extra it lacks, in order.
func UnionStrings(base, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range append(append([]string(nil), base...), extra...) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// NoHoldLimit is the hold_days value that means no time limit. A row's 0 means
// the same, and HoldLimit maps it to this so the config always has a window.
const NoHoldLimit = 99999

// HoldLimit is the holding window for a row's hold_days: 0 (or less) is no limit.
func HoldLimit(days int) int {
	if days <= 0 {
		return NoHoldLimit
	}
	return days
}

// HasLimit reports whether any grid point enters on a buy limit, so the sweep
// needs the hourly bars that decide those fills.
func (p ParameterSpace) HasLimit() bool {
	if p.Baseline.EntryLimit > 0 {
		return true
	}
	for _, l := range p.EntryLimits {
		if l > 0 {
			return true
		}
	}
	return false
}
