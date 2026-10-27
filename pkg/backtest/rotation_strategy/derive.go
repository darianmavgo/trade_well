package rotation_strategy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

// Params is the parameter set of one period sweep point, the values a
// gridsearch label names: Period-1d/Limit-95%/Hold-2d/TP+3%/SL-3%.
type Params struct {
	Period     string  // 1d, 1w, 1m, 1q or 1y
	EntryLimit float64 // fraction of the previous close, 1 buys at it
	HoldDays   int     // sessions after the entry session, 0 holds to the period end
	TakeProfit float64 // fractional offset over the entry, 0 is none
	StopLoss   float64 // fractional offset under the entry, 0 is none
}

var labelRe = regexp.MustCompile(`Period-(\w+)/Limit-(\d+(?:\.\d+)?)%/Hold-(\d+)d/TP\+(\d+(?:\.\d+)?)%/SL-(\d+(?:\.\d+)?)%`)

// ParseParams finds a gridsearch label in text and returns its parameters. The
// text may be the label alone or a whole line of a sweep's output, rank and
// profit included. The label holds whole percents (the sweep prints %.0f).
func ParseParams(text string) (Params, error) {
	m := labelRe.FindStringSubmatch(text)
	if m == nil {
		return Params{}, fmt.Errorf("no parameter set like Period-1d/Limit-95%%/Hold-2d/TP+3%%/SL-3%% in %q", text)
	}
	pct := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v / 100 }
	hold, _ := strconv.Atoi(m[3])
	p := Params{Period: m[1], EntryLimit: pct(m[2]), HoldDays: hold, TakeProfit: pct(m[4]), StopLoss: pct(m[5])}
	if _, ok := Periods[p.Period]; !ok {
		return Params{}, fmt.Errorf("period %q is not one of %v", p.Period, PeriodIDs)
	}
	return p, nil
}

// Label is p as a gridsearch label.
func (p Params) Label() string {
	return fmt.Sprintf("Period-%s/Limit-%.0f%%/Hold-%dd/TP+%.0f%%/SL-%.0f%%", p.Period, p.EntryLimit*100, p.HoldDays, p.TakeProfit*100, p.StopLoss*100)
}

var trailingLimit = regexp.MustCompile(`-limit\d+.*$`)

// DefaultDerivedID is the id Derive gives a copy of from with p, when none is
// named: from without its own limit suffix, then the parameters, for example
// rotation-2x-sector-pairs-daily-limit95-tp3-sl3-hold2.
func DefaultDerivedID(from string, p Params) string {
	id := trailingLimit.ReplaceAllString(from, "")
	id += fmt.Sprintf("-limit%.0f-tp%.0f-sl%.0f-hold%d", p.EntryLimit*100, p.TakeProfit*100, p.StopLoss*100, p.HoldDays)
	if p.Period != "1d" {
		id += "-" + p.Period
	}
	return id
}

// Derive is DeriveWith with the parameters in p and the source's position size.
func Derive(db *sqlx.DB, from, id, name string, p Params) (refdb.RotationStrategy, error) {
	return DeriveWith(db, from, id, name, &p, 0)
}

// DeriveWith adds a rotation_strategy row that is a copy of the row from, and
// returns it. A non-nil p replaces its period, buy limit, hold, take profit and stop.
// A positive positionPct (a fraction of portfolio value, for a pick = all row) sets the
// size of each position: allocation_pct and max_weight_pct are both set to it, because
// a row that picks every name is sized at the smaller of the two. The copy keeps
// everything else, the symbol list by its symbol_lists id included, and the new row
// must pass the family's own validation or nothing is written. An id that already
// exists is an error. An empty id or name takes DefaultDerivedID (or the source id with
// -pos<N> when only the position size changes) or the source's name with the changes.
func DeriveWith(db *sqlx.DB, from, id, name string, p *Params, positionPct float64) (refdb.RotationStrategy, error) {
	if p == nil && positionPct <= 0 {
		return refdb.RotationStrategy{}, fmt.Errorf("nothing to change: give a parameter set, a position size or both")
	}
	if positionPct < 0 || positionPct > 1 {
		return refdb.RotationStrategy{}, fmt.Errorf("position size %v is not a fraction of portfolio value in (0, 1]", positionPct)
	}
	tx, err := db.Beginx()
	if err != nil {
		return refdb.RotationStrategy{}, err
	}
	defer tx.Rollback()

	var srcName string
	if err := tx.Get(&srcName, `SELECT name FROM rotation_strategy WHERE id = ?`, from); err != nil {
		return refdb.RotationStrategy{}, fmt.Errorf("%q is not a rotation strategy (only rotation period rows can be derived): %w", from, err)
	}
	src, ok, err := refdb.RotationStrategyByID(tx, from)
	if err != nil || !ok {
		return refdb.RotationStrategy{}, fmt.Errorf("read %q: %v", from, err)
	}
	if positionPct > 0 && src.Pick != "all" {
		return refdb.RotationStrategy{}, fmt.Errorf("a position size applies to a pick = all row, and %q picks %q", from, src.Pick)
	}
	params := Params{Period: src.Period, EntryLimit: src.EntryLimitPct, HoldDays: src.HoldDays, TakeProfit: src.TakeProfitPct, StopLoss: src.StopLossPct}
	if p != nil {
		params = *p
	}
	alloc, maxWeight := src.AllocationPct, src.MaxWeightPct
	if positionPct > 0 {
		alloc, maxWeight = positionPct, positionPct
	}
	if id == "" {
		id = from + fmt.Sprintf("-pos%.0f", positionPct*100)
		if p != nil {
			id = DefaultDerivedID(from, params)
			if positionPct > 0 {
				id += fmt.Sprintf("-pos%.0f", positionPct*100)
			}
		}
	}
	var exists int
	if err := tx.Get(&exists, `SELECT COUNT(*) FROM rotation_strategy WHERE id = ?`, id); err != nil {
		return refdb.RotationStrategy{}, err
	}
	if exists > 0 {
		return refdb.RotationStrategy{}, fmt.Errorf("strategy %q already exists", id)
	}
	if name == "" {
		var changes []string
		if p != nil {
			changes = append(changes, strings.ReplaceAll(params.Label(), "/", " "))
		}
		if positionPct > 0 {
			changes = append(changes, fmt.Sprintf("Position %.0f%%", positionPct*100))
		}
		name = fmt.Sprintf("%s [%s]", srcName, strings.Join(changes, ", "))
	}
	// Columns are listed so a copy of a row never picks up a column by position.
	if _, err := tx.Exec(`INSERT INTO rotation_strategy
		(id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma,
		 allocation_pct, cash_yield, slippage_pct, period, side, pick,
		 entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct, cooldown)
		SELECT ?, ?, symbols, universe_size, top_k, exit_buffer, ?, regime_symbol, regime_sma,
		       ?, cash_yield, slippage_pct, ?, side, pick,
		       ?, ?, ?, ?, cooldown
		FROM rotation_strategy WHERE id = ?`,
		id, name, maxWeight, alloc, params.Period, params.EntryLimit, params.TakeProfit, params.HoldDays, params.StopLoss, from); err != nil {
		return refdb.RotationStrategy{}, err
	}
	row, ok, err := refdb.RotationStrategyByID(tx, id)
	if err != nil || !ok {
		return refdb.RotationStrategy{}, fmt.Errorf("read back %q: %v", id, err)
	}
	if err := (&Strategy{Row: row}).Validate(); err != nil {
		return refdb.RotationStrategy{}, fmt.Errorf("%s with those changes is not a runnable strategy: %w", from, err)
	}
	if err := tx.Commit(); err != nil {
		return refdb.RotationStrategy{}, err
	}
	return row, nil
}
