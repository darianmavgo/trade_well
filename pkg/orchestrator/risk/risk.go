// Package risk is the pre-trade circuit breaker. Once per session it records each account's
// equity, measures drawdown from the equity high-water mark and the change since the last
// session, and decides what new entries may do: trade at full size, trade at a reduced size
// (the drawdown ladder), or not trade at all (the ladder's last step, or a daily loss past the
// limit). It also keeps total exposure under a cap.
//
// It only ever restricts NEW ENTRIES. Exits, stops and take-profits keep running, which is why
// this is not the killswitch (that stops every job, exits included). Stage_orders sizes with
// the state, and submit_orders and trigger_limits refuse to place a buy while it is halted.
//
// The state is computed by the `risk` job (after each account_state sync) and read by the
// others, so the limits are configured in one place and every reader sees the same answer.
package risk

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// JobName is the cron job, CLI subcommand, page route and job_run name.
const JobName = "risk"

// eps keeps a loss of exactly the limit (90 against a peak of 100) from slipping under it in
// floating point.
const eps = 1e-9

// Step is one rung of the drawdown ladder: from DrawdownPct (a fraction, 0.10 = 10% below the
// high-water mark) down, new entries are sized at Scale of normal. Scale 0 halts entries.
type Step struct {
	DrawdownPct float64
	Scale       float64
}

// Limits are the configured risk limits. The zero value limits nothing.
type Limits struct {
	Ladder       []Step  // sorted by DrawdownPct ascending
	DailyLossPct float64 // halt entries when equity fell this fraction since the last session (0 = off)
	MaxGrossPct  float64 // long market value may not exceed this fraction of equity (0 = off)

	// Per-order and per-day fat-finger caps. Deliberately generous: they exist to stop a
	// runaway or a typo, not to shape the strategies.
	MaxOrderPct     float64 // one entry order may cost at most this fraction of equity (0 = off)
	MaxOrdersPerDay int     // entry orders placed per account per ET session (0 = off)
	MaxPositions    int     // open long positions per account; a buy past it is refused (0 = off)

	// Automatic killswitch triggers, checked by the risk job (see AutoKillReason).
	AutoKillDrawdown float64 // engage at this drawdown from the high-water mark (0 = off)
	AutoKillFailures int     // engage after this many failed runs in a row of an order job (0 = off)
}

// DefaultLimits halves new entries 10% below the high-water mark, halts them 20% below, halts
// them after a 5% one-session loss, and caps exposure at 100% of equity (no leverage). The
// order caps are loose on purpose: one order up to 60% of equity, 40 entry orders a day and
// 60 open positions (the sector rotation holds up to 48).
func DefaultLimits() Limits {
	return Limits{
		Ladder:          []Step{{0.10, 0.5}, {0.20, 0}},
		DailyLossPct:    0.05,
		MaxGrossPct:     1.0,
		MaxOrderPct:     0.60,
		MaxOrdersPerDay: 40,
		MaxPositions:    60,

		AutoKillDrawdown: 0.35,
		AutoKillFailures: 6,
	}
}

// ParseLadder reads "0.10:0.5,0.20:0": drawdown fraction, a colon, the entry scale. An empty
// string is an empty ladder (no drawdown limit).
func ParseLadder(s string) ([]Step, error) {
	var out []Step
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dd, sc, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("risk ladder step %q: want drawdown:scale, such as 0.10:0.5", part)
		}
		d, err1 := strconv.ParseFloat(strings.TrimSpace(dd), 64)
		c, err2 := strconv.ParseFloat(strings.TrimSpace(sc), 64)
		if err1 != nil || err2 != nil || d <= 0 || d >= 1 || c < 0 || c > 1 {
			return nil, fmt.Errorf("risk ladder step %q: drawdown must be in (0,1) and scale in [0,1]", part)
		}
		out = append(out, Step{d, c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DrawdownPct < out[j].DrawdownPct })
	return out, nil
}

// State is one account's assessment.
type State struct {
	Account     string
	AssessedAt  time.Time
	Session     string  // ET session date the equity belongs to
	Equity      float64 // latest recorded equity
	Peak        float64 // highest recorded equity
	Drawdown    float64 // fraction below Peak, >= 0
	DayChange   float64 // fraction vs the previous session's equity (negative = loss)
	Gross       float64 // long market value as a fraction of equity
	Scale       float64 // 1 = full size, 0 = no new entries
	HaltEntries bool
	MaxGross    float64 // the cap in force (0 = none)
	Reasons     []string

	// The order caps in force when this state was assessed (0 = none); see CheckEntry.
	MaxOrderPct     float64
	MaxOrdersPerDay int
	MaxPositions    int
}

// Normal is the state of an account with no recorded equity: nothing is limited.
func Normal(account string) State { return State{Account: account, Scale: 1} }

func (s State) String() string {
	switch {
	case s.HaltEntries:
		return fmt.Sprintf("entries HALTED: %s", strings.Join(s.Reasons, "; "))
	case s.Scale < 1:
		return fmt.Sprintf("entries at %.0f%% size: %s", s.Scale*100, strings.Join(s.Reasons, "; "))
	}
	return "entries at full size"
}

// Assess applies the limits to the numbers. prevEquity <= 0 means there is no earlier session.
// It is pure: the same inputs always give the same state.
func Assess(l Limits, equity, peak, prevEquity, longValue float64) State {
	s := State{Scale: 1, Equity: equity, Peak: math.Max(peak, equity), MaxGross: l.MaxGrossPct,
		MaxOrderPct: l.MaxOrderPct, MaxOrdersPerDay: l.MaxOrdersPerDay, MaxPositions: l.MaxPositions}
	if s.Peak > 0 {
		s.Drawdown = math.Max(0, 1-equity/s.Peak)
	}
	if prevEquity > 0 {
		s.DayChange = equity/prevEquity - 1
	}
	if equity > 0 {
		s.Gross = longValue / equity
	}
	for _, step := range l.Ladder {
		if s.Drawdown >= step.DrawdownPct-eps && step.Scale < s.Scale {
			s.Scale = step.Scale
			s.Reasons = append(s.Reasons, fmt.Sprintf("drawdown %.1f%% is past the %.0f%% step (entries at %.0f%%)", s.Drawdown*100, step.DrawdownPct*100, step.Scale*100))
		}
	}
	if l.DailyLossPct > 0 && prevEquity > 0 && -s.DayChange >= l.DailyLossPct-eps {
		s.Scale = 0
		s.Reasons = append(s.Reasons, fmt.Sprintf("equity fell %.1f%% since the last session (limit %.1f%%)", -s.DayChange*100, l.DailyLossPct*100))
	}
	s.HaltEntries = s.Scale <= 0
	if equity <= 0 {
		s.Scale, s.HaltEntries = 0, true
		s.Reasons = append(s.Reasons, "no positive equity")
	}
	return s
}

// GrossRoom is how much more long exposure the cap allows, in dollars: MaxGross x the account's
// live equity less what is already held. With no cap it is +Inf.
func (s State) GrossRoom(equity, longValue float64) float64 {
	if s.MaxGross <= 0 {
		return math.Inf(1)
	}
	return math.Max(0, s.MaxGross*equity-longValue)
}

// EnsureSchema creates the tables: one equity row per account per session, and the latest
// assessment per account.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("risk/ensure_schema"))
	if err != nil {
		return err
	}
	// A table made before the order caps existed keeps its rows and gains the columns.
	for _, c := range []struct{ name, decl string }{
		{"max_order_pct", "REAL NOT NULL DEFAULT 0"},
		{"max_orders_per_day", "INTEGER NOT NULL DEFAULT 0"},
		{"max_positions", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := dbutil.AddColumn(db, "risk_state", c.name, c.decl); err != nil {
			return err
		}
	}
	return nil
}

// Load reads the account's latest assessment. An account never assessed is Normal, so a
// fresh database does not refuse trades; check_health-style staleness is the caller's to
// judge from AssessedAt.
func Load(db *sql.DB, account string) (State, error) {
	if db == nil {
		return Normal(account), fmt.Errorf("risk: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return Normal(account), err
	}
	var s State
	var halt int
	var reasons string
	err := db.QueryRow(orchestratorsql.Q("risk/load"), account).
		Scan(&s.AssessedAt, &s.Session, &s.Equity, &s.Peak, &s.Drawdown, &s.DayChange, &s.Gross, &s.Scale, &halt, &s.MaxGross, &reasons,
			&s.MaxOrderPct, &s.MaxOrdersPerDay, &s.MaxPositions)
	if err == sql.ErrNoRows {
		return Normal(account), nil
	}
	if err != nil {
		return Normal(account), err
	}
	s.Account, s.HaltEntries = account, halt == 1
	if reasons != "" {
		s.Reasons = strings.Split(reasons, "\n")
	}
	return s, nil
}

// All lists every account's latest assessment, by account number.
func All(db *sql.DB) ([]State, error) {
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(orchestratorsql.Q("risk/all"))
	if err != nil {
		return nil, err
	}
	var accts []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return nil, err
		}
		accts = append(accts, a)
	}
	rows.Close()
	out := make([]State, 0, len(accts))
	for _, a := range accts {
		s, err := Load(db, a)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func save(db *sql.DB, s State) error {
	halt := 0
	if s.HaltEntries {
		halt = 1
	}
	_, err := db.Exec(orchestratorsql.Q("risk/save"),
		s.Account, s.AssessedAt.UTC(), s.Session, s.Equity, s.Peak, s.Drawdown, s.DayChange, s.Gross, s.Scale, halt, s.MaxGross, strings.Join(s.Reasons, "\n"),
		s.MaxOrderPct, s.MaxOrdersPerDay, s.MaxPositions)
	return err
}

// Point is one recorded session's equity.
type Point struct {
	Account string  `json:"account"`
	Session string  `json:"session"`
	Equity  float64 `json:"equity"`
}

// History lists the newest recorded equity rows, newest first.
func History(db *sql.DB, limit int) ([]Point, error) {
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}
	rows, err := db.Query(orchestratorsql.Q("risk/history"), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Account, &p.Session, &p.Equity); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
