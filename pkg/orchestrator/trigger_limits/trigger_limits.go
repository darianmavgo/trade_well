// Package trigger_limits places the limit entries pkg/stage_orders held back.
//
// A strategy that puts a day limit on dozens of names (the sector-pairs rotation puts
// one on each of 48 ETFs, every session, each worth 20% of equity) cannot rest all
// of them at the broker: a working buy order reserves its cash, so the first few names
// alphabetically would be funded and the rest refused, and the strategy the backtest
// ran spends cash only on the orders that fill. stage_orders therefore stages these
// orders as WATCHING. This job runs every few minutes through the session. When a
// name's low of the day has reached its limit it sizes the order against the cash left
// at that moment and makes it PENDING, and pkg/submit_orders places it as a day limit
// (with its take-profit and stop). At the close it cancels the ones whose limit was
// never reached: buy limits expire every day. Names are taken in alphabetical order
// within one pass; across passes the earlier trigger gets the cash.
package trigger_limits

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/calendar"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/submit_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Quote is the part of a market quote a trigger reads: the lowest and the asking price
// of the regular session so far, and the last trade.
type Quote struct {
	Low, Ask, Last float64
}

// QuoteFunc returns quotes by symbol; a symbol with no quote is left out.
type QuoteFunc func(ctx context.Context, symbols []string) (map[string]Quote, error)

// Options configures Run and RunJob.
type Options struct {
	// Now overrides the clock in tests.
	Now time.Time
	// DryRun reports what would happen and writes nothing.
	DryRun bool
	// Members holds the resolved strategies by id, for the monthly cooldown
	// (StrategyConfig.OneFillPerMonth).
	Members map[string]strategy.Strategy
	// Quotes supplies market quotes.
	Quotes QuoteFunc
	// NoCancel makes Seat place limits and nothing else: no swapping a resting order for
	// a nearer name, and no closing of a watched order that is skipped (held, cooled
	// down, under one share); those are reported as SKIPPED and left WATCHING.
	NoCancel bool
	// AnySession has Seat place every WATCHING order whatever session it is staged for
	// and at any hour, instead of only today's during 09:00 to 15:30 ET. Schwab takes a
	// day limit entered after the close for the next session.
	AnySession bool
	// MaxFailures, when above zero, ends Seat's placing once that many orders have been
	// refused or have failed, a buying-power refusal included, and has Seat carry on past
	// a refusal until then. Zero keeps the cron behaviour: stop at the first buying-power
	// refusal and treat any other failure as an error.
	MaxFailures int
}

// Row is one outcome for the report.
type Row struct {
	Account  string
	Strategy string
	Symbol   string
	Status   string // PLACED, CANCELLED, EXPIRED
	Detail   string
}

// Result summarizes one account's pass.
type Result struct {
	Watching  int // WATCHING rows found
	Triggered int // limits reached
	Promoted  int // made PENDING for submit_orders
	Cancelled int // triggered but not placed (no cash, held, cooled down)
	Expired   int // never reached by the close
	Details   []string
	Rows      []Row
}

const (
	openMinutes  = 9*60 + 30
	closeMinutes = 16 * 60
)

// sessionOf is the session a watched order is for: the one recorded when it was
// staged, otherwise the first session at or after its staging time that has not
// closed.
func sessionOf(o stage_orders.StagedOrder) string {
	if o.SessionDate != "" {
		return o.SessionDate
	}
	et := o.StagedAt.In(job_runs.EasternTime())
	day := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, time.UTC)
	if !calendar.IsSession(day) || et.Hour() >= 16 {
		day = calendar.AddTradingDays(day, 1)
	}
	return day.Format("2006-01-02")
}

// Run is one pass over acc's WATCHING orders at opts.Now.
func Run(ctx context.Context, db *sql.DB, acc *trader.ResolvedAccount, opts Options) (*Result, error) {
	res := &Result{}
	if db == nil {
		return res, fmt.Errorf("trigger_limits: nil db")
	}
	if acc == nil {
		return res, fmt.Errorf("trigger_limits: no account")
	}
	if err := stage_orders.EnsureSchema(db); err != nil {
		return res, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	et := now.In(job_runs.EasternTime())
	today := et.Format("2006-01-02")
	minutes := et.Hour()*60 + et.Minute()
	todayUTC := time.Date(et.Year(), et.Month(), et.Day(), 0, 0, 0, 0, time.UTC)
	marketOpen := calendar.IsSession(todayUTC) && minutes >= openMinutes && minutes < closeMinutes

	watching, err := stage_orders.GetWatchingStagedOrders(db, acc.AccountNumber)
	if err != nil {
		return res, err
	}
	res.Watching = len(watching)

	// Buy limits expire every day: anything for an earlier session, or for today once
	// the close has passed, was never reached.
	var live []stage_orders.StagedOrder
	for _, o := range watching {
		session := sessionOf(o)
		if session < today || (session == today && minutes >= closeMinutes) {
			res.Expired++
			res.Rows = append(res.Rows, Row{Account: acc.AccountNumber, Strategy: o.StrategyName, Symbol: o.Symbol, Status: "EXPIRED",
				Detail: fmt.Sprintf("limit $%.2f for %s was never reached", o.EntryPrice, session)})
			if !opts.DryRun {
				if err := stage_orders.CancelWatchingOrder(db, o.ID, "day limit expired unfilled: the low never reached $"+fmt.Sprintf("%.2f", o.EntryPrice)); err != nil {
					res.Details = append(res.Details, fmt.Sprintf("[%s] failed to cancel expired watch (staged id=%d): %v", o.Symbol, o.ID, err))
				}
			}
			continue
		}
		if session == today && marketOpen {
			live = append(live, o)
		}
	}
	if len(live) == 0 {
		return res, nil
	}

	if opts.Quotes == nil {
		return res, fmt.Errorf("trigger_limits: no quote source")
	}
	symbols := make([]string, 0, len(live))
	for _, o := range live {
		symbols = append(symbols, strings.ToUpper(o.Symbol))
	}
	quotes, err := opts.Quotes(ctx, symbols)
	if err != nil {
		return res, fmt.Errorf("trigger_limits: quotes: %w", err)
	}

	equity, cash := stage_orders.AccountCapital(acc)
	held := stage_orders.BrokerHeld(db, acc, acc.AccountNumber)
	filledByMonth := map[string]map[string]bool{}
	sort.SliceStable(live, func(i, j int) bool { return live[i].Symbol < live[j].Symbol })
	for _, o := range live {
		sym := strings.ToUpper(o.Symbol)
		q, ok := quotes[sym]
		if !ok || q.Low <= 0 || q.Low > o.EntryPrice+1e-9 {
			continue // no quote, or the limit has not been reached today
		}
		res.Triggered++
		cancel := func(reason string) {
			res.Cancelled++
			res.Rows = append(res.Rows, Row{Account: acc.AccountNumber, Strategy: o.StrategyName, Symbol: o.Symbol, Status: "CANCELLED", Detail: reason})
			res.Details = append(res.Details, fmt.Sprintf("[%s] limit $%.2f reached (low $%.2f) but not placed: %s", o.Symbol, o.EntryPrice, q.Low, reason))
			if !opts.DryRun {
				_ = stage_orders.CancelWatchingOrder(db, o.ID, reason)
			}
		}
		if reason := skipReason(db, opts.Members, o, held, filledByMonth, now); reason != "" {
			cancel(reason)
			continue
		}
		alloc := o.AllocPct
		if alloc <= 0 || alloc > 1 {
			alloc = 0.65
		}
		target := math.Min(equity*alloc, cash*0.95)
		qty := math.Floor(target / o.EntryPrice)
		if qty < 1 {
			cancel(fmt.Sprintf("no cash left for it (equity $%.0f, cash $%.0f, $%.2f a share)", equity, cash, o.EntryPrice))
			continue
		}
		res.Promoted++
		cash -= qty * o.EntryPrice
		held[sym] = true
		detail := fmt.Sprintf("%.0f shares @ $%.2f (low $%.2f, ask $%.2f): placing as a day limit", qty, o.EntryPrice, q.Low, q.Ask)
		res.Details = append(res.Details, fmt.Sprintf("[%s] %s (staged id=%d)", o.Symbol, detail, o.ID))
		res.Rows = append(res.Rows, Row{Account: acc.AccountNumber, Strategy: o.StrategyName, Symbol: o.Symbol, Status: "PLACED", Detail: detail})
		if !opts.DryRun {
			if _, err := stage_orders.PromoteWatchingOrder(db, o.ID, qty); err != nil {
				return res, fmt.Errorf("promote staged order %d: %w", o.ID, err)
			}
		}
	}
	return res, nil
}

// JobResult summarizes RunJob across accounts.
type JobResult struct {
	Watching, Triggered, Promoted, Cancelled, Expired int
	Seated, Swapped                                   int // pre-open placements and nearer-name swaps (Seat)
	Submit                                            *submit_orders.JobResult
	Details                                           []string
	Rows                                              []Row
}

// RunJob is the trigger_limits job: Run for every account, then, when an order was made
// PENDING, submit_orders so it reaches the broker in the same pass. A pass that did
// nothing (no watched orders, no limit reached) writes no job_run row, since it runs
// every few minutes.
func RunJob(ctx context.Context, db *sql.DB, tc *trader.TraderClient, accts []*trader.ResolvedAccount, opts Options) (*JobResult, error) {
	out := &JobResult{}
	var errs []string
	for _, acc := range accts {
		seat, serr := Seat(ctx, db, tc, acc, opts)
		if seat != nil {
			out.Seated += seat.Seated
			out.Swapped += seat.Swapped
			out.Details = append(out.Details, seat.Details...)
			out.Rows = append(out.Rows, seat.Rows...)
		}
		if serr != nil {
			errs = append(errs, fmt.Sprintf("account %s: seat: %v", acc.AccountNumber, serr))
		}
		r, err := Run(ctx, db, acc, opts)
		if r != nil {
			out.Watching += r.Watching
			out.Triggered += r.Triggered
			out.Promoted += r.Promoted
			out.Cancelled += r.Cancelled
			out.Expired += r.Expired
			out.Details = append(out.Details, r.Details...)
			out.Rows = append(out.Rows, r.Rows...)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("account %s: %v", acc.AccountNumber, err))
		}
	}
	if out.Promoted > 0 && !opts.DryRun && tc != nil {
		sub, err := submit_orders.RunJob(ctx, db, tc, accts, submit_orders.JobOptions{})
		out.Submit = sub
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	if !opts.DryRun && (out.Promoted+out.Cancelled+out.Expired+out.Seated+out.Swapped > 0 || len(errs) > 0) {
		status := job_runs.JobRunOK
		if len(errs) > 0 {
			status = job_runs.JobRunFailed
		}
		summary := fmt.Sprintf("watching=%d seated=%d swapped=%d triggered=%d placed=%d cancelled=%d expired=%d errors=%d",
			out.Watching, out.Seated, out.Swapped, out.Triggered, out.Promoted, out.Cancelled, out.Expired, len(errs))
		_, _ = job_runs.RecordJobRun(db, job_runs.JobRun{
			Job: "trigger_limits", Status: status, Summary: summary, RowsWritten: out.Promoted + out.Seated, FinishedAt: time.Now().UTC(),
		})
	}
	if len(errs) > 0 {
		return out, fmt.Errorf("trigger_limits: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// skipReason says why a watched order must not be placed ("" when it may): the account
// already holds the name, or the strategy takes a name out of the rotation for the month
// after a fill. filledByMonth caches the monthly lookups across orders.
func skipReason(db *sql.DB, members map[string]strategy.Strategy, o stage_orders.StagedOrder, held map[string]bool, filledByMonth map[string]map[string]bool, now time.Time) string {
	sym := strings.ToUpper(o.Symbol)
	if held[sym] {
		return "already holding " + sym
	}
	if m := members[o.StrategyName]; m != nil && m.DefaultConfig().OneFillPerMonth {
		month := stage_orders.SessionMonth(sessionOf(o), now)
		filled, seen := filledByMonth[month]
		if !seen {
			filled = stage_orders.FilledThisMonth(db, o.AccountNumber, month)
			filledByMonth[month] = filled
		}
		if filled[o.StrategyName+"|"+sym] {
			return "filled earlier in " + month + ": out of the rotation until next month"
		}
	}
	return ""
}
