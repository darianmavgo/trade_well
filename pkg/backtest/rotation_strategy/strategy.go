// Package rotation_strategy is the rotation family: every session it ranks a
// liquid universe on momentum and holds the top few names, rotating out of a
// name once it falls well down the ranking. The ranking is SQL
// (sql/strategies/rotation_strategy); this package builds each row's config and
// runs it.
package rotation_strategy

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

const (
	momentumDir = "sql/strategies/rotation_strategy"
	periodDir   = "sql/strategies/rotation_period"
)

// Periods are the calendar periods a period row can use, with the SQL that
// names the period of a session (`date` is the bar's YYYY-MM-DD). Keys sort in
// time order. Weeks start on Monday.
var Periods = map[string]string{
	"1d": "date",
	"1w": "date(date, '-' || ((CAST(strftime('%w', date) AS INTEGER) + 6) % 7) || ' days')",
	"1m": "substr(date, 1, 7)",
	"1q": "substr(date, 1, 4) || 'Q' || ((CAST(substr(date, 6, 2) AS INTEGER) + 2) / 3)",
	"1y": "substr(date, 1, 4)",
}

// PeriodIDs lists Periods from shortest to longest.
var PeriodIDs = []string{"1d", "1w", "1m", "1q", "1y"}

// annualShortBorrow is the cash-secured short's yearly fee on market value.
const annualShortBorrow = 0.01

var safeSymbol = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.\-]*$`)

// Strategy is one rotation_strategy row.
type Strategy struct {
	Row          refdb.RotationStrategy
	marketDBPath string
	calcDBPath   string
}

func (s *Strategy) ID() string { return s.Row.ID }

// Family is the family name, for logs and listings.
func (s *Strategy) Family() string { return "rotation" }

func (s *Strategy) Name() string { return s.Row.Name }

// periodic reports whether the row is a period winner (period set) rather than
// the daily momentum ranking.
func (s *Strategy) periodic() bool { return s.Row.Period != "" }

func (s *Strategy) Description() string {
	if s.periodic() {
		return s.periodDescription()
	}
	desc := fmt.Sprintf("Holds the top %d of %d liquid names by momentum, sold once ranked worse than %d.",
		s.Row.TopK, s.Row.UniverseSize, s.Row.TopK+s.Row.ExitBuffer)
	if s.Row.RegimeSMA > 0 {
		desc += fmt.Sprintf(" Only while %s closes above its %d session average.", s.Row.RegimeSymbol, s.Row.RegimeSMA)
	}
	return desc
}

func (s *Strategy) periodDescription() string {
	unit := map[string]string{"1d": "day", "1w": "week", "1m": "month", "1q": "quarter", "1y": "calendar year"}[s.Row.Period]
	if s.Row.Pick == "all" {
		return s.allDescription(unit)
	}
	kind := "winner"
	if s.Row.Pick == "loser" {
		kind = "loser"
	}
	names := "the biggest " + kind
	if s.Row.TopK > 1 {
		names = fmt.Sprintf("the %d biggest %ss", s.Row.TopK, kind)
	}
	switch s.Row.Side {
	case "short":
		return fmt.Sprintf("Shorts %s of the previous %s and covers on the last session of the new %s. "+
			"Cash-secured (collateral = notional) with a %.0f%% annual borrow fee on market value.",
			names, unit, unit, annualShortBorrow*100)
	case "inverse":
		return fmt.Sprintf("Buys the matched-leverage inverse ETF of %s of the previous %s and sells it on the last session of the new %s. "+
			"Skips a pick that has no inverse ETF in the bar history, or whose inverse did not trade on the entry date.", names, unit, unit)
	}
	return fmt.Sprintf("Buys %s of the previous %s and holds it for the new %s.", names, unit, unit)
}

// allDescription describes a row that enters every name of its list each period.
func (s *Strategy) allDescription(unit string) string {
	r := s.Row
	desc := fmt.Sprintf("Each %s, enters every one of its %d names", unit, len(s.candidates()))
	if r.EntryLimitPct > 0 {
		desc += fmt.Sprintf(" with a buy limit at %.0f%% of the previous close that expires unfilled at the close", r.EntryLimitPct*100)
	}
	desc += fmt.Sprintf(", each position %.0f%% of portfolio value.", s.weight()*100)
	var exits []string
	if r.TakeProfitPct > 0 {
		exits = append(exits, fmt.Sprintf("take profit %.0f%% over the entry", r.TakeProfitPct*100))
	}
	if r.StopLossPct > 0 {
		exits = append(exits, fmt.Sprintf("stop %.0f%% under it", r.StopLossPct*100))
	}
	if r.HoldDays > 0 {
		exits = append(exits, fmt.Sprintf("sell at the close %d sessions after the entry session", r.HoldDays))
	}
	if len(exits) > 0 {
		desc += " Exits: " + strings.Join(exits, ", ") + "."
	}
	if r.Cooldown == "month" {
		desc += " A name that fills is out of the rotation until the next calendar month."
	}
	return desc
}

// candidates is the row's symbol list, upper case. Validate has already refused an unsafe entry.
func (s *Strategy) candidates() []string {
	var out []string
	for sym := range strings.SplitSeq(s.Row.Symbols, ",") {
		if sym = strings.ToUpper(strings.TrimSpace(sym)); sym != "" {
			out = append(out, sym)
		}
	}
	return out
}

// RequiredSymbols is every candidate, so the runner loads bars for any name the
// ranking may pick, plus the regime symbol when the gate is on.
func (s *Strategy) RequiredSymbols() []string {
	out := s.candidates()
	if len(out) == 0 {
		return nil // every symbol in the market database
	}
	if s.Row.RegimeSMA > 0 {
		out = append(out, strings.ToUpper(strings.TrimSpace(s.Row.RegimeSymbol)))
	}
	return out
}

// weight is the share of equity in one name: an even split of the sleeve across
// top_k names, capped at max_weight_pct.
func (s *Strategy) weight() float64 {
	if s.periodic() && s.Row.Pick == "all" {
		// Every name is entered, so the sleeve is not split: each position is its own
		// share of equity, never more than max_weight_pct.
		return min(s.Row.AllocationPct, s.Row.MaxWeightPct)
	}
	w := s.Row.AllocationPct / float64(max(s.Row.TopK, 1))
	if s.Row.MaxWeightPct > 0 && w > s.Row.MaxWeightPct {
		w = s.Row.MaxWeightPct
	}
	return w
}

func (s *Strategy) DefaultConfig() strategy.StrategyConfig {
	if s.periodic() {
		cfg := strategy.StrategyConfig{
			ID:                s.ID(),
			Name:              s.Name(),
			Description:       s.Description(),
			TargetPct:         999.0,                                                                // never exit via profit target
			StopLossPct:       0.0,                                                                  // 0 disables the stop loss
			HoldingWindow:     map[bool]int{true: s.Row.HoldDays, false: 99999}[s.Row.HoldDays > 0], // else the period's last session is the exit
			PositionCap:       s.positionCap(),
			AllocationPct:     s.Row.AllocationPct,
			CashYieldAnnual:   s.Row.CashYield,
			SlippagePct:       s.Row.SlippagePct,
			ShortBorrowAnnual: map[bool]float64{true: annualShortBorrow}[s.Row.Side == "short"],
			EntryLimitPct:     s.Row.EntryLimitPct,
			OneFillPerMonth:   s.Row.Cooldown == "month",
		}
		if s.Row.TakeProfitPct > 0 {
			cfg.TargetPct = 0 // the simulator prefers TargetPct when it is above 1
			cfg.TakeProfitPct = s.Row.TakeProfitPct
		}
		if s.Row.StopLossPct > 0 {
			cfg.StopLossPct = 1 - s.Row.StopLossPct // the config holds the stop as a multiplier
		}
		// A limit order fills during the entry session, so its stop or target can be
		// reached that same session. A close entry has no session left to judge.
		cfg.SameDayExit = s.Row.EntryLimitPct > 0
		if s.Row.Pick == "all" {
			cfg.AllocationPct = s.weight() // each name is sized alone, not as a share of top_k
		}
		return cfg
	}
	return strategy.StrategyConfig{
		ID:                 s.ID(),
		Name:               s.Name(),
		Description:        s.Description(),
		Benchmark:          "QQQ",
		TargetPct:          999.0,  // the ranking is the only exit
		StopLossPct:        0.0001, // never exit via stop loss
		HoldingWindow:      99999,  // never exit via time limit
		PositionCap:        max(s.Row.TopK, 1),
		AllocationPct:      s.Row.AllocationPct,
		CashYieldAnnual:    s.Row.CashYield,
		SlippagePct:        s.Row.SlippagePct,
		CommissionPerShare: 0.005,
	}
}

// positionCap is how many names are held at once: top_k, or with pick = all as many
// as the list has (the only limit is each position's size).
func (s *Strategy) positionCap() int {
	if s.Row.Pick == "all" {
		return max(len(s.candidates()), 1)
	}
	return max(s.Row.TopK, 1)
}

func (s *Strategy) pipelineDir() string {
	if s.periodic() {
		return periodDir
	}
	return momentumDir
}

func (s *Strategy) PipelineDir() string { return filepath.Join(appenv.Folder(), s.pipelineDir()) }

func (s *Strategy) Validate() error {
	r := s.Row
	switch {
	case r.TopK < 1:
		return fmt.Errorf("top_k must be at least 1, got %d", r.TopK)
	case r.UniverseSize < r.TopK:
		return fmt.Errorf("universe_size %d is smaller than top_k %d", r.UniverseSize, r.TopK)
	case r.ExitBuffer < 0:
		return fmt.Errorf("exit_buffer must not be negative, got %d", r.ExitBuffer)
	case r.MaxWeightPct <= 0 || r.MaxWeightPct > 1:
		return fmt.Errorf("max_weight_pct must be in (0, 1], got %v", r.MaxWeightPct)
	case r.RegimeSMA < 0:
		return fmt.Errorf("regime_sma must not be negative, got %d", r.RegimeSMA)
	case r.RegimeSMA > 0 && !safeSymbol.MatchString(strings.ToUpper(strings.TrimSpace(r.RegimeSymbol))):
		return fmt.Errorf("regime_symbol %q is not a symbol", r.RegimeSymbol)
	}
	if s.Row.Period != "" {
		if _, ok := Periods[s.Row.Period]; !ok {
			return fmt.Errorf("period %q is not one of %v", s.Row.Period, PeriodIDs)
		}
		switch s.Row.Side {
		case "long", "short", "inverse":
		default:
			return fmt.Errorf("side %q is not long, short or inverse", s.Row.Side)
		}
		switch s.Row.Pick {
		case "winner", "loser":
		case "all":
			if len(s.candidates()) == 0 {
				return fmt.Errorf("pick all needs a list of symbols")
			}
		default:
			return fmt.Errorf("pick %q is not winner, loser or all", s.Row.Pick)
		}
	} else if s.Row.Side != "" && s.Row.Side != "long" {
		return fmt.Errorf("side %q needs a period: only a period row can be short or inverse", s.Row.Side)
	}
	if r.HoldDays < 0 {
		return fmt.Errorf("hold_days must not be negative, got %d", r.HoldDays)
	}
	switch r.Cooldown {
	case "", "month":
	default:
		return fmt.Errorf("cooldown %q is not empty or month", r.Cooldown)
	}
	if r.EntryLimitPct != 0 || r.TakeProfitPct != 0 || r.StopLossPct != 0 || r.HoldDays != 0 || r.Cooldown != "" {
		switch {
		case s.Row.Period == "" || s.Row.Side != "long":
			return fmt.Errorf("entry_limit_pct, take_profit_pct, stop_loss_pct, hold_days and cooldown need a long period row")
		case r.StopLossPct < 0 || r.StopLossPct >= 1:
			return fmt.Errorf("stop_loss_pct must be in [0, 1), got %v", r.StopLossPct)
		case r.EntryLimitPct < 0 || r.EntryLimitPct > 1:
			return fmt.Errorf("entry_limit_pct must be in [0, 1], got %v", r.EntryLimitPct)
		case r.TakeProfitPct < 0:
			return fmt.Errorf("take_profit_pct must not be negative, got %v", r.TakeProfitPct)
		}
	}
	syms := s.candidates()
	if len(syms) == 0 && !s.periodic() {
		return fmt.Errorf("symbols is empty: the runner loads bars only for the listed candidates")
	}
	for _, sym := range syms {
		if !safeSymbol.MatchString(sym) {
			return fmt.Errorf("symbols: %q is not a symbol", sym)
		}
	}
	return strategy.ValidateConfig(s.DefaultConfig())
}

func (s *Strategy) SetDatabases(marketDBPath, calcDBPath string) {
	s.marketDBPath = marketDBPath
	s.calcDBPath = calcDBPath
}

// SQLParams are the values the pipeline's __PLACEHOLDERS__ take for this row.
func (s *Strategy) SQLParams() map[string]string {
	if s.periodic() {
		return s.periodSQLParams()
	}
	quoted := make([]string, 0, len(s.candidates()))
	for _, sym := range s.candidates() {
		quoted = append(quoted, "'"+sym+"'")
	}
	regimeOn := "0"
	if s.Row.RegimeSMA > 0 {
		regimeOn = "1"
	}
	return map[string]string{
		"USE_LIST":         "1",
		"SYMBOLS":          strings.Join(quoted, ","),
		"UNIVERSE_SIZE":    strconv.Itoa(s.Row.UniverseSize),
		"TOP_K":            strconv.Itoa(s.Row.TopK),
		"EXIT_RANK":        strconv.Itoa(s.Row.TopK + s.Row.ExitBuffer),
		"REGIME_ON":        regimeOn,
		"REGIME_SYMBOL":    strings.ToUpper(strings.TrimSpace(s.Row.RegimeSymbol)),
		"REGIME_SMA":       strconv.Itoa(max(s.Row.RegimeSMA, 1)),
		"REGIME_PRECEDING": strconv.Itoa(max(s.Row.RegimeSMA, 1) - 1),
		"ALLOC":            strconv.FormatFloat(s.weight(), 'f', 6, 64),
	}
}

// periodSQLParams are the values the rotation_period pipeline takes for this row.
func (s *Strategy) periodSQLParams() map[string]string {
	quoted := make([]string, 0, len(s.candidates()))
	for _, sym := range s.candidates() {
		quoted = append(quoted, "'"+sym+"'")
	}
	useList := "1"
	if len(quoted) == 0 {
		useList, quoted = "0", []string{"''"}
	}
	return map[string]string{
		"USE_LIST":   useList,
		"SYMBOLS":    strings.Join(quoted, ","),
		"TOP_K":      strconv.Itoa(max(s.Row.TopK, 1)),
		"PICK_LIMIT": strconv.Itoa(s.pickLimit()),
		// A 1d row with a hold window has no earlier period-end sale: its next session
		// would end every hold after one session.
		"PERIOD_EXIT": map[bool]string{true: "0", false: "1"}[s.Row.Period == "1d" && s.Row.HoldDays > 0],
		"SIDE":        s.Row.Side,
		"PICK_ORDER":  map[bool]string{true: "ASC", false: "DESC"}[s.Row.Pick == "loser"],
		"PERIOD_KEY":  Periods[s.Row.Period],
		"ALLOC":       strconv.FormatFloat(s.weight(), 'f', 6, 64),
	}
}

// pickLimit is how many names the pick stage keeps each period: top_k, or every
// name of the list.
func (s *Strategy) pickLimit() int {
	if s.Row.Pick == "all" {
		return 1 << 30
	}
	return max(s.Row.TopK, 1)
}

// PeriodVariant returns the strategy over the same row with its period replaced
// (gridsearch tries each period this way). The row's id is unchanged.
func (s *Strategy) PeriodVariant(period string) strategy.Strategy {
	c := *s
	c.Row.Period = period
	return &c
}

func (s *Strategy) GenerateSignals(barsBySymbol map[string][]models.Bar) []models.Signal {
	cfg := s.DefaultConfig()
	cfg.SQLParams = s.SQLParams()
	return strategy.RunPipeline(s.ID(), s.Name(), s.Description(), s.pipelineDir(), cfg, s.marketDBPath, s.calcDBPath, "market", barsBySymbol)
}

func (s *Strategy) ParameterSpace() strategy.ParameterSpace {
	cfg := s.DefaultConfig()
	var periods []string
	var limits, takeProfits, stopLosses []float64
	holdDays := []int{cfg.HoldingWindow}
	baseHold := cfg.HoldingWindow
	if s.periodic() {
		axes := strategy.LoadFamilyAxes("rotation")
		periods = strategy.UnionStrings(axes.Strs["period"], []string{s.Row.Period})
		if s.Row.Side == "long" {
			// The row's own values stay on each axis so the baseline is a grid point.
			limits = strategy.UnionFloats(axes.Nums["entry_limit_pct"], []float64{s.Row.EntryLimitPct})
			takeProfits = strategy.UnionFloats(axes.Nums["take_profit_pct"], []float64{s.Row.TakeProfitPct})
			stopLosses = strategy.UnionFloats(axes.Nums["stop_loss_pct"], []float64{s.Row.StopLossPct})
			holdDays = strategy.UnionInts(axes.Ints("hold_days"), []int{s.Row.HoldDays})
			baseHold = s.Row.HoldDays // 0 is no hold limit, a grid point like the rest
		}
	}
	if takeProfits == nil {
		takeProfits = []float64{0}
	}
	if stopLosses == nil {
		stopLosses = []float64{0}
	}
	return strategy.ParameterSpace{
		Periods:      periods,
		EntryLimits:  limits,
		StrategyID:   s.ID(),
		StrategyName: s.Name(),
		Description:  s.Description(),
		Symbols:      s.RequiredSymbols(),
		Direction:    "long",
		FixedEntries: true,
		SignalDays:   []int{1},
		HoldDays:     holdDays,
		TakeProfits:  takeProfits,
		StopLosses:   stopLosses,
		Regimes:      []string{"All Regimes"},
		Allocations:  []float64{s.Row.AllocationPct},
		CashYield:    s.Row.CashYield,
		Baseline: strategy.BaselineParams{
			Period:     s.Row.Period,
			EntryLimit: s.Row.EntryLimitPct,
			TakeProfit: s.Row.TakeProfitPct,
			StopLoss:   s.Row.StopLossPct,
			HoldDays:   baseHold,
			Allocation: s.Row.AllocationPct,
		},
	}
}

// NewFamily is the rotation family over the reference database path returns.
func NewFamily(path func() string) *strategy.RowFamily {
	return &strategy.RowFamily{
		FamilyName: "rotation",
		Table:      "rotation_strategy",
		Path:       path,
		Build: func(db *sqlx.DB, id string) (strategy.Strategy, bool, error) {
			row, ok, err := refdb.RotationStrategyByID(db, id)
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

// Register adds the rotation family reading the default reference database.
func Register() { RegisterFrom(refdb.DefaultPath) }

// RegisterFrom adds the rotation family reading the reference database at path.
func RegisterFrom(path string) {
	strategy.RegisterFamily(NewFamily(func() string { return path }))
}

// FromRow builds the strategy for a row that is not (yet) in the reference
// database, validated like a stored one. Gridsearch's per-symbol mode sweeps one
// such row per symbol.
func FromRow(row refdb.RotationStrategy) (strategy.Strategy, error) {
	s := &Strategy{Row: row}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}
