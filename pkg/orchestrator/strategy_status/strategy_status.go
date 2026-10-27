// Package strategy_status answers "is this strategy doing its job?" from the orchestrator's
// own database, without calling Schwab: did today's signal check run and what did it say,
// which positions the strategy created actually exist at the broker, how long each is held,
// and whether anything in the signal -> stage -> submit -> exit chain went wrong.
//
// The broker side is the last account_state sync (the account_state_position mirror), so a
// position's "confirmed_at_broker" is only as fresh as that sync, whose time is reported.
package strategy_status

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Overall verdicts, worst last.
const (
	VerdictOK        = "OK"
	VerdictAttention = "ATTENTION"
	VerdictProblem   = "PROBLEM"
)

// Problem severities.
const (
	SevInfo    = "INFO"
	SevWarning = "WARNING"
	SevProblem = "PROBLEM"
)

// DefaultWindowDays is how far back signals, staged orders and submit attempts are read.
const DefaultWindowDays = 7

// Status is the full answer for one strategy.
type Status struct {
	Strategy    string    `json:"strategy"`
	GeneratedAt string    `json:"generated_at"`
	Today       string    `json:"today_et"`
	WindowDays  int       `json:"window_days"`
	Greenlit    bool      `json:"greenlit"`
	Verdict     string    `json:"verdict"`
	Problems    []Problem `json:"problems"`

	SignalCheck SignalCheck  `json:"signal_check"`
	Positions   []Position   `json:"positions"`
	Closed      []Position   `json:"recent_closed"`
	Staged      []StagedItem `json:"staged_orders"`
	Attempts    []Attempt    `json:"submit_attempts"`

	// BrokerSource says where broker facts come from: "live" (Schwab, read for this request),
	// "mirror" (the last account_state sync, at BrokerSnapshotAt) or "none".
	BrokerSource     string       `json:"broker_source"`
	BrokerSnapshotAt string       `json:"broker_snapshot_at,omitempty"`
	LiveErrors       []string     `json:"live_errors,omitempty"`
	BrokerWorking    []WorkingLeg `json:"broker_working_orders"`

	symbols         map[string]bool // upper-cased symbols this strategy has signaled, staged or held
	UntrackedShares []UntrackedPx   `json:"untracked_shares"`
}

// WorkingLeg is an order live at the broker on one of the strategy's symbols.
type WorkingLeg struct {
	Account     string  `json:"account"`
	Symbol      string  `json:"symbol"`
	OrderID     string  `json:"order_id"`
	Instruction string  `json:"instruction"`
	OrderType   string  `json:"order_type"`
	Status      string  `json:"status"`
	Price       float64 `json:"price,omitempty"`
	StopPrice   float64 `json:"stop_price,omitempty"`
	Quantity    float64 `json:"quantity"`
}

// UntrackedPx is shares the broker holds on a strategy symbol beyond what the strategy's
// OPEN rows account for.
type UntrackedPx struct {
	Account string  `json:"account"`
	Symbol  string  `json:"symbol"`
	Held    float64 `json:"broker_held"`
	Tracked float64 `json:"tracked_open"`
}

// Problem is one thing that went wrong, or needs a look.
type Problem struct {
	Severity string `json:"severity"`
	Where    string `json:"where"` // scan, stage, submit, position, exit
	Detail   string `json:"detail"`
}

// SignalCheck is the scan result for today: result is SIGNAL, NO_SIGNAL, SCAN_FAILED or
// NOT_RUN (no scan has covered today).
type SignalCheck struct {
	Date      string   `json:"date"`
	Result    string   `json:"result"`
	LastScan  *ScanRun `json:"last_scan,omitempty"`
	CoversDay bool     `json:"last_scan_covers_today"`
	// Why is the newest scan's reason for this strategy's verdict on the session it evaluated, from
	// the pipeline's own trace (for example "no entry: state is sideways, the strategy needs bull").
	Why     string   `json:"why,omitempty"`
	Signals []Signal `json:"signals"`
}

// ScanRun is the latest scan_market job run.
type ScanRun struct {
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	FinishedAt string `json:"finished_at"`
}

// Signal is one buy signal the strategy produced.
type Signal struct {
	ID            int64   `json:"id"`
	Symbol        string  `json:"symbol"`
	AsOfDate      string  `json:"as_of_date"`
	NextSession   string  `json:"next_session"`
	Status        string  `json:"status"`
	BuyLimit      float64 `json:"buy_limit"`
	TakeProfit    float64 `json:"take_profit"`
	StopLoss      float64 `json:"stop_loss"`
	HoldDays      int     `json:"hold_days"`
	StagedOrderID int64   `json:"staged_order_id,omitempty"`
	Notes         string  `json:"notes,omitempty"`
	ForToday      bool    `json:"for_today"`
}

// Position is one manage_exit row of the strategy with its hold window and broker check.
type Position struct {
	TradeID      int64   `json:"trade_id"`
	Account      string  `json:"account"`
	Symbol       string  `json:"symbol"`
	Status       string  `json:"status"`
	Quantity     float64 `json:"quantity"`
	EntryOrderID string  `json:"entry_order_id"`
	// Live broker facts about this row's own entry order (live source only).
	BrokerEntryStatus string   `json:"broker_entry_status,omitempty"`
	RestingTakeProfit *float64 `json:"resting_take_profit,omitempty"`
	RestingStop       *float64 `json:"resting_stop,omitempty"`
	FilledExitOrderID string   `json:"filled_exit_order_id,omitempty"`
	EntryFilledAt     string   `json:"entry_filled_at,omitempty"`
	EntryPrice        float64  `json:"entry_price"`
	TakeProfit        float64  `json:"take_profit"`
	StopLoss          float64  `json:"stop_loss"`
	MaxTradingDays    int      `json:"max_trading_days"`
	TradingDaysHeld   int      `json:"trading_days_held"`
	ExitDate          string   `json:"exit_date"`
	ExitDateSource    string   `json:"exit_date_source"`
	DaysRemaining     int      `json:"days_remaining"`
	ExitOverdue       bool     `json:"exit_overdue"`
	ExitReason        string   `json:"exit_reason,omitempty"`
	ExitTime          string   `json:"exit_time,omitempty"`
	// BrokerHeldQty is what the last sync saw at the broker for this account and symbol;
	// ConfirmedAtBroker is true when it covers the row's quantity. Both are only set for
	// OPEN rows.
	BrokerHeldQty     *float64 `json:"broker_held_qty,omitempty"`
	ConfirmedAtBroker *bool    `json:"confirmed_at_broker,omitempty"`
}

// StagedItem is a stage_order row.
type StagedItem struct {
	ID           int64   `json:"id"`
	Account      string  `json:"account"`
	Symbol       string  `json:"symbol"`
	Status       string  `json:"status"`
	Quantity     float64 `json:"quantity"`
	EntryPrice   float64 `json:"entry_price"`
	OrderID      string  `json:"order_id,omitempty"`
	BrokerStatus string  `json:"broker_status,omitempty"`
	RejectReason string  `json:"reject_reason,omitempty"`
	StagedAt     string  `json:"staged_at"`
}

// Attempt is a submit_order row: one try at placing a staged order.
type Attempt struct {
	StagedOrderID int64  `json:"staged_order_id"`
	BrokerOrderID string `json:"broker_order_id,omitempty"`
	Status        string `json:"status"`
	Detail        string `json:"detail,omitempty"`
	At            string `json:"at"`
}

// Build assembles the status of strategyID as of now. allowlist is the STRATEGY_ALLOWLIST
// value ("" when unknown). windowDays <= 0 means DefaultWindowDays. live, when not nil, is
// Schwab as read for this request and replaces the account_state mirror for every broker fact.
func Build(db *sql.DB, strategyID, allowlist string, now time.Time, windowDays int, live *Live) (*Status, error) {
	if db == nil {
		return nil, fmt.Errorf("strategy_status: nil db")
	}
	strategyID = strings.TrimSpace(strategyID)
	if strategyID == "" {
		return nil, fmt.Errorf("strategy_status: empty strategy id")
	}
	if windowDays <= 0 {
		windowDays = DefaultWindowDays
	}
	et := job_runs.EasternTime()
	today := now.In(et).Format("2006-01-02")
	since := now.In(et).AddDate(0, 0, -windowDays)
	sinceDate := since.Format("2006-01-02")

	s := &Status{
		Strategy: strategyID, GeneratedAt: now.UTC().Format(time.RFC3339), Today: today, WindowDays: windowDays,
		Greenlit: inAllowlist(allowlist, strategyID),
		Problems: []Problem{}, Positions: []Position{}, Closed: []Position{}, Staged: []StagedItem{}, Attempts: []Attempt{},
		BrokerWorking: []WorkingLeg{}, UntrackedShares: []UntrackedPx{}, symbols: map[string]bool{},
	}
	if !s.Greenlit {
		s.add(SevWarning, "scan", "strategy is not in STRATEGY_ALLOWLIST, so nothing scans, stages or trades it")
	}

	if err := s.loadSignals(db, strategyID, today, sinceDate); err != nil {
		return nil, err
	}
	if live != nil {
		s.LiveErrors = live.Errors
		for _, e := range live.Errors {
			s.add(SevWarning, "broker", "live Schwab read incomplete: %s", e)
		}
	}
	if err := s.loadStaged(db, strategyID, now, since, live); err != nil {
		return nil, err
	}
	if err := s.loadPositions(db, strategyID, now, since, live); err != nil {
		return nil, err
	}

	s.Verdict = VerdictOK
	for _, p := range s.Problems {
		switch p.Severity {
		case SevProblem:
			s.Verdict = VerdictProblem
		case SevWarning:
			if s.Verdict != VerdictProblem {
				s.Verdict = VerdictAttention
			}
		}
	}
	return s, nil
}

func (s *Status) add(sev, where, format string, args ...any) {
	s.Problems = append(s.Problems, Problem{Severity: sev, Where: where, Detail: fmt.Sprintf(format, args...)})
}

// Members lists each distinct strategy in the allowlist, in order, with stack entries
// ("a+b+c") split into their members.
func Members(allowlist string) []string {
	var out []string
	seen := map[string]bool{}
	for entry := range strings.SplitSeq(allowlist, ",") {
		for member := range strings.SplitSeq(entry, "+") {
			m := strings.TrimSpace(member)
			if m != "" && !seen[strings.ToLower(m)] {
				seen[strings.ToLower(m)] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// BuildAll is Build for every greenlit strategy, sharing one live broker read.
func BuildAll(db *sql.DB, allowlist string, now time.Time, windowDays int, live *Live) ([]*Status, error) {
	var out []*Status
	for _, id := range Members(allowlist) {
		st, err := Build(db, id, allowlist, now, windowDays, live)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		out = append(out, st)
	}
	return out, nil
}

// inAllowlist reports whether id is an entry of the allowlist or a member of a stack
// entry ("a+b+c"). Entries are comma separated.
func inAllowlist(allowlist, id string) bool {
	for entry := range strings.SplitSeq(allowlist, ",") {
		if strings.EqualFold(strings.TrimSpace(entry), id) {
			return true
		}
		for member := range strings.SplitSeq(entry, "+") {
			if strings.EqualFold(strings.TrimSpace(member), id) {
				return true
			}
		}
	}
	return false
}

var scanDates = regexp.MustCompile(`as_of=(\S+) next=(\S+)`)

func (s *Status) loadSignals(db *sql.DB, id, today, sinceDate string) error {
	s.SignalCheck = SignalCheck{Date: today, Signals: []Signal{}}
	run, err := job_runs.LastJobRun(db, "scan_market")
	if err != nil && !missingTable(err) {
		return fmt.Errorf("read scan_market job run: %w", err)
	}
	if run != nil {
		s.SignalCheck.LastScan = &ScanRun{Status: run.Status, Summary: run.Summary, FinishedAt: run.FinishedAt.UTC().Format(time.RFC3339)}
		if m := scanDates.FindStringSubmatch(run.Summary); m != nil {
			s.SignalCheck.CoversDay = m[1] == today || m[2] == today
		}
	}

	s.SignalCheck.Why = lastReason(db, id)

	rows, err := db.Query(orchestratorsql.Q("strategy_status/load_signals"), id, sinceDate)
	if err != nil && !missingTable(err) {
		return fmt.Errorf("read buy signals: %w", err)
	}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var g Signal
			if err := rows.Scan(&g.ID, &g.Symbol, &g.AsOfDate, &g.NextSession, &g.Status, &g.BuyLimit, &g.TakeProfit,
				&g.StopLoss, &g.HoldDays, &g.StagedOrderID, &g.Notes); err != nil {
				return err
			}
			s.symbols[strings.ToUpper(g.Symbol)] = true
			g.ForToday = g.AsOfDate == today || g.NextSession == today
			s.SignalCheck.Signals = append(s.SignalCheck.Signals, g)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	forToday := 0
	for _, g := range s.SignalCheck.Signals {
		if !g.ForToday {
			continue
		}
		forToday++
		if g.Status == "DISMISSED" {
			s.add(SevInfo, "stage", "signal %d (%s) was dismissed, so no order was staged: %s", g.ID, g.Symbol, orNone(g.Notes))
		}
	}
	switch {
	case forToday > 0:
		s.SignalCheck.Result = "SIGNAL"
	case run != nil && run.Status == job_runs.JobRunFailed:
		s.SignalCheck.Result = "SCAN_FAILED"
		s.add(SevProblem, "scan", "the last scan_market run FAILED at %s: %s", run.FinishedAt.UTC().Format(time.RFC3339), run.Summary)
	case s.SignalCheck.CoversDay:
		s.SignalCheck.Result = "NO_SIGNAL"
	default:
		s.SignalCheck.Result = "NOT_RUN"
		day, _ := time.Parse("2006-01-02", today)
		if calendar.IsSession(day) {
			// Today's entries come from last evening's scan (Sun-Thu 18:00 ET). A session with
			// no record of it is not "fine": either the scan did not run or this database
			// lost its record, and either way today's signal check cannot be confirmed here.
			s.add(SevWarning, "scan", "%s is a trading day but this database has no scan_market record for it (last recorded: %s). The scan runs every evening Sun-Thu, so either it did not run or its record was lost -- check the cron log before trusting any 'no signal' here",
				today, lastScanText(run))
		} else {
			s.add(SevInfo, "scan", "%s is not a trading day, so no entry scan is expected for it (last recorded: %s)", today, lastScanText(run))
		}
	}
	return nil
}

func lastScanText(run *job_runs.JobRun) string {
	if run == nil {
		return "never run"
	}
	return fmt.Sprintf("%s %s, %s", run.Status, run.FinishedAt.UTC().Format(time.RFC3339), run.Summary)
}

func (s *Status) loadStaged(db *sql.DB, id string, now, since time.Time, live *Live) error {
	rows, err := db.Query(orchestratorsql.Q("strategy_status/load_staged"),
		id, since.UTC())
	if err != nil && !missingTable(err) {
		return fmt.Errorf("read stage_order: %w", err)
	}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var o StagedItem
			var at any
			if err := rows.Scan(&o.ID, &o.Account, &o.Symbol, &o.Status, &o.Quantity, &o.EntryPrice, &o.OrderID, &o.RejectReason, &at); err != nil {
				return err
			}
			o.StagedAt = ts(at)
			s.symbols[strings.ToUpper(o.Symbol)] = true
			if live != nil && o.OrderID != "" && live.Accounts[o.Account] {
				st, _ := manage_exits.EntryFill(live.Orders[o.Account], o.OrderID)
				o.BrokerStatus = st
				switch st {
				case "":
					s.add(SevWarning, "submit", "staged order %d: broker has no order %s in the last 58 days", o.ID, o.OrderID)
				case "CANCELED", "REJECTED", "EXPIRED":
					s.add(SevProblem, "submit", "staged order %d (%s): broker order %s is %s, it never filled", o.ID, o.Symbol, o.OrderID, st)
				}
			}
			s.Staged = append(s.Staged, o)
			age := now.Sub(parseTS(at))
			switch o.Status {
			case stage_orders.StagedOrderRejected:
				s.add(SevProblem, "stage", "staged order %d (%s, account %s) was REJECTED: %s", o.ID, o.Symbol, o.Account, orNone(o.RejectReason))
			case stage_orders.StagedOrderSubmitting:
				if age > 15*time.Minute {
					s.add(SevProblem, "submit", "staged order %d (%s) has been SUBMITTING for %s: the broker outcome is unknown", o.ID, o.Symbol, age.Round(time.Minute))
				}
			case stage_orders.StagedOrderPending:
				if age > 24*time.Hour {
					s.add(SevWarning, "submit", "staged order %d (%s) was staged %s ago and never submitted", o.ID, o.Symbol, age.Round(time.Hour))
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	att, err := db.Query(orchestratorsql.Q("strategy_status/load_staged_2"), id, since.UTC())
	if err != nil {
		if missingTable(err) {
			return nil
		}
		return fmt.Errorf("read submit_order: %w", err)
	}
	defer att.Close()
	for att.Next() {
		var a Attempt
		var at any
		if err := att.Scan(&a.StagedOrderID, &a.BrokerOrderID, &a.Status, &a.Detail, &at); err != nil {
			return err
		}
		a.At = ts(at)
		s.Attempts = append(s.Attempts, a)
		if a.Status != "FILLED" {
			sev := SevWarning
			if strings.HasPrefix(a.Status, "FAILED") {
				sev = SevProblem
			}
			s.add(sev, "submit", "staged order %d attempt %s at %s: %s", a.StagedOrderID, a.Status, a.At, orNone(a.Detail))
		}
	}
	return att.Err()
}

// brokerQty is the long quantity the last sync saw per "account|SYMBOL", and when.
func brokerQty(db *sql.DB) (qty map[string]float64, snapshot string, ok bool, err error) {
	rows, err := db.Query(orchestratorsql.Q("strategy_status/broker_qty"))
	if err != nil {
		if missingTable(err) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	defer rows.Close()
	qty = map[string]float64{}
	var newest time.Time
	for rows.Next() {
		var acc, sym string
		var q float64
		var at any
		if err := rows.Scan(&acc, &sym, &q, &at); err != nil {
			return nil, "", false, err
		}
		qty[acc+"|"+strings.ToUpper(sym)] += q
		if t := parseTS(at); t.After(newest) {
			newest = t
		}
	}
	if !newest.IsZero() {
		snapshot = newest.UTC().Format(time.RFC3339)
	}
	return qty, snapshot, true, rows.Err()
}

func (s *Status) loadPositions(db *sql.DB, id string, now time.Time, since time.Time, live *Live) error {
	// Broker facts: Schwab live when it was read, else the last account_state sync.
	var mirror map[string]float64
	haveMirror := false
	s.BrokerSource = "none"
	if live != nil && len(live.Accounts) > 0 {
		s.BrokerSource = "live"
	} else {
		var snapshot string
		var err error
		mirror, snapshot, haveMirror, err = brokerQty(db)
		if err != nil {
			return fmt.Errorf("read broker positions: %w", err)
		}
		if haveMirror {
			s.BrokerSource, s.BrokerSnapshotAt = "mirror", snapshot
		}
	}
	// heldAt is the broker's long quantity for the account and symbol; known is false when
	// the broker was not read for that account, in which case nothing may be concluded.
	heldAt := func(acc, sym string) (qty float64, known bool) {
		key := acc + "|" + strings.ToUpper(sym)
		if s.BrokerSource == "live" {
			return live.Held[key], live.Accounts[acc]
		}
		return mirror[key], haveMirror
	}

	rows, err := db.Query(orchestratorsql.Q("strategy_status/load_positions"), id, id)
	if err != nil && !missingTable(err) {
		return fmt.Errorf("read manage_exit: %w", err)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()
	et := job_runs.EasternTime()
	today := now.In(et).Format("2006-01-02")
	openQty := map[string]float64{} // "account|SYMBOL" -> quantity of this strategy's OPEN rows
	for rows.Next() {
		var p Position
		var filled, entered, exited any
		var code string
		if err := rows.Scan(&p.TradeID, &p.Account, &p.Symbol, &p.Status, &p.Quantity, &p.EntryOrderID, &filled, &entered,
			&p.EntryPrice, &p.TakeProfit, &p.StopLoss, &p.MaxTradingDays, &p.ExitDate, &p.ExitDateSource, &p.ExitReason, &exited, &code); err != nil {
			return err
		}
		s.symbols[strings.ToUpper(p.Symbol)] = true
		p.EntryFilledAt, p.ExitTime = ts(filled), ts(exited)
		start := parseTS(filled)
		if start.IsZero() {
			start = parseTS(entered)
		}
		switch p.Status {
		case manage_exits.TradeStatusOpen, manage_exits.TradeStatusPendingFill:
			if !start.IsZero() {
				p.TradingDaysHeld = calendar.TradingDaysBetween(etDay(start, et), etDay(now, et))
			}
			if p.ExitDate != "" {
				p.DaysRemaining = tradingDaysUntil(now, et, p.ExitDate)
				p.ExitOverdue = p.ExitDate < today
			}
			if s.BrokerSource == "live" && live.Accounts[p.Account] {
				s.checkLiveEntry(&p, live.Orders[p.Account])
			}
			if p.Status == manage_exits.TradeStatusOpen {
				openQty[p.Account+"|"+strings.ToUpper(p.Symbol)] += p.Quantity
				if q, known := heldAt(p.Account, p.Symbol); known {
					ok := q >= p.Quantity
					p.BrokerHeldQty, p.ConfirmedAtBroker = &q, &ok
				}
				s.checkOpen(&p, today)
			} else if age := now.Sub(parseTS(entered)); age > 36*time.Hour {
				s.add(SevWarning, "position", "trade %d (%s) entry order %s has waited %s to fill", p.TradeID, p.Symbol, p.EntryOrderID, age.Round(time.Hour))
			}
			s.Positions = append(s.Positions, p)
		default:
			if exitTime := parseTS(exited); exitTime.After(since) {
				p.TradingDaysHeld = calendar.TradingDaysBetween(etDay(start, et), etDay(exitTime, et))
				if p.Status == manage_exits.TradeStatusClosedTimeExpiry && p.ExitDate != "" && exitTime.In(et).Format("2006-01-02") < p.ExitDate {
					s.add(SevProblem, "exit", "trade %d (%s) was closed by time expiry on %s, before its exit date %s: %s",
						p.TradeID, p.Symbol, exitTime.In(et).Format("2006-01-02"), p.ExitDate, orNone(p.ExitReason))
				}
				if p.Status == manage_exits.TradeStatusCancelled {
					s.add(SevWarning, "exit", "trade %d (%s) entry never filled: %s", p.TradeID, p.Symbol, orNone(p.ExitReason))
				}
				s.Closed = append(s.Closed, p)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if s.BrokerSource == "live" {
		s.checkLiveBook(live, openQty)
	}
	return nil
}

// checkLiveEntry reads the row's own entry order and its exit legs from the live order
// list (matched by order id, never by symbol) and flags a row the broker contradicts.
func (s *Status) checkLiveEntry(p *Position, orders []schwab.Order) {
	if p.EntryOrderID == "" {
		return
	}
	status, _ := manage_exits.EntryFill(orders, p.EntryOrderID)
	p.BrokerEntryStatus = status
	legs := manage_exits.ExitLegs(orders, p.EntryOrderID)
	for _, l := range legs {
		if !schwab.IsWorkingStatus(l.Status) || !trader.OrderIsSell(&l) {
			continue
		}
		switch strings.ToUpper(l.OrderType) {
		case "LIMIT":
			if l.Price != nil {
				p.RestingTakeProfit = l.Price
			}
		case "STOP", "STOP_LIMIT":
			if l.StopPrice != nil {
				p.RestingStop = l.StopPrice
			}
		}
	}
	if leg := manage_exits.FilledExitLeg(legs); leg != nil && leg.OrderID != nil {
		p.FilledExitOrderID = fmt.Sprintf("%d", *leg.OrderID)
	}

	switch p.Status {
	case manage_exits.TradeStatusOpen:
		switch {
		case p.FilledExitOrderID != "":
			s.add(SevProblem, "position", "trade %d (%s) is OPEN but the broker shows its exit order %s filled: the row is stale",
				p.TradeID, p.Symbol, p.FilledExitOrderID)
		case status == "":
			s.add(SevWarning, "position", "trade %d (%s): entry order %s is not in the broker's last 58 days of orders", p.TradeID, p.Symbol, p.EntryOrderID)
		case status != "FILLED":
			s.add(SevProblem, "position", "trade %d (%s) is OPEN but its entry order %s is %s at the broker", p.TradeID, p.Symbol, p.EntryOrderID, status)
		}
	case manage_exits.TradeStatusPendingFill:
		switch status {
		case "FILLED":
			s.add(SevWarning, "position", "trade %d (%s) entry order %s FILLED at the broker but the row is still PENDING_FILL (next submit_orders or manage_exits run promotes it)",
				p.TradeID, p.Symbol, p.EntryOrderID)
		case "CANCELED", "REJECTED", "EXPIRED":
			s.add(SevWarning, "position", "trade %d (%s) entry order %s is %s at the broker but the row is still PENDING_FILL", p.TradeID, p.Symbol, p.EntryOrderID, status)
		}
	}
}

// checkLiveBook compares the broker's whole book with what the strategy's OPEN rows
// account for, on the strategy's symbols, and lists the orders working there.
func (s *Status) checkLiveBook(live *Live, openQty map[string]float64) {
	for acc := range live.Accounts {
		for _, o := range schwab.FlattenOrders(live.Orders[acc]) {
			if !schwab.IsWorkingStatus(o.Status) {
				continue
			}
			legs, err := trader.ParseOrderLegs(&o)
			if err != nil || len(legs) == 0 || !s.symbols[strings.ToUpper(legs[0].Instrument.Symbol)] {
				continue
			}
			w := WorkingLeg{Account: acc, Symbol: strings.ToUpper(legs[0].Instrument.Symbol), Instruction: legs[0].Instruction,
				OrderType: o.OrderType, Status: strings.ToUpper(o.Status), Quantity: legs[0].Quantity}
			if o.OrderID != nil {
				w.OrderID = fmt.Sprintf("%d", *o.OrderID)
			}
			if o.Price != nil {
				w.Price = *o.Price
			}
			if o.StopPrice != nil {
				w.StopPrice = *o.StopPrice
			}
			s.BrokerWorking = append(s.BrokerWorking, w)
		}
	}
	for key, held := range live.Held {
		acc, sym, _ := strings.Cut(key, "|")
		if !s.symbols[sym] || !live.Accounts[acc] {
			continue
		}
		if tracked := openQty[key]; held > tracked {
			s.UntrackedShares = append(s.UntrackedShares, UntrackedPx{Account: acc, Symbol: sym, Held: held, Tracked: tracked})
			s.add(SevWarning, "position", "account %s holds %.0f %s at the broker but this strategy's OPEN rows account for %.0f (the rest is untracked, or another strategy's)",
				acc, held, sym, tracked)
		}
	}
}

// checkOpen records what is wrong with an OPEN row from its own fields and broker checks.
func (s *Status) checkOpen(p *Position, today string) {
	if p.ConfirmedAtBroker != nil && !*p.ConfirmedAtBroker {
		s.add(SevProblem, "position", "trade %d (%s, account %s) is OPEN for %.0f but the %s broker read shows %.0f held",
			p.TradeID, p.Symbol, p.Account, p.Quantity, s.BrokerSource, *p.BrokerHeldQty)
	}
	if p.ConfirmedAtBroker != nil && *p.ConfirmedAtBroker && s.BrokerSource == "live" && p.FilledExitOrderID == "" &&
		p.BrokerEntryStatus == "FILLED" && (p.RestingTakeProfit == nil || p.RestingStop == nil) {
		s.add(SevWarning, "position", "trade %d (%s) is held with no resting %s at the broker",
			p.TradeID, p.Symbol, missingExits(p))
	}
	switch {
	case p.ExitOverdue:
		s.add(SevProblem, "exit", "trade %d (%s) is past its exit date %s and still OPEN", p.TradeID, p.Symbol, p.ExitDate)
	case p.ExitDate == today:
		s.add(SevInfo, "exit", "trade %d (%s) reaches its exit date today", p.TradeID, p.Symbol)
	}
	if p.ExitDate == "" {
		s.add(SevWarning, "exit", "trade %d (%s) is OPEN with no exit date", p.TradeID, p.Symbol)
	}
}

func missingExits(p *Position) string {
	switch {
	case p.RestingTakeProfit == nil && p.RestingStop == nil:
		return "take-profit or stop"
	case p.RestingTakeProfit == nil:
		return "take-profit"
	}
	return "stop"
}

// etDay is the New York calendar date of t as midnight UTC, the form the calendar package counts in.
func etDay(t time.Time, et *time.Location) time.Time {
	l := t.In(et)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}

// tradingDaysUntil is the trading days from today (New York) to the exit date; 0 on or after it.
func tradingDaysUntil(now time.Time, et *time.Location, exitDate string) int {
	end, err := time.Parse("2006-01-02", exitDate)
	if err != nil {
		return 0
	}
	start := etDay(now, et)
	if !end.After(start) {
		return 0
	}
	return calendar.TradingDaysBetween(start, end)
}

func missingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no reason recorded)"
	}
	return s
}

// ts renders a database time value as RFC 3339 UTC ("" for NULL).
func ts(v any) string {
	if t := parseTS(v); !t.IsZero() {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

func parseTS(v any) time.Time {
	switch x := v.(type) {
	case time.Time:
		return x
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// lastReason is the reason the newest scan recorded for the session it evaluated, or "" when no scan
// has stored one (an older scan, or the table does not exist yet).
func lastReason(db *sql.DB, id string) string {
	var why string
	if err := db.QueryRow(orchestratorsql.Q("strategy_status/last_reason"), id).Scan(&why); err != nil {
		return ""
	}
	return why
}
