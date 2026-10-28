// Package cancel_stale_entries cancels unfilled ENTRY orders that have been resting at the
// broker for more than 24 hours. A day entry expires at the close by itself; the orders this
// catches are good-till-cancelled ones (strategies that ask for GOOD_TILL_CANCEL, and every
// entry submitted before day orders became the default) that never filled and hold buying power.
//
// Only an entry is cancelled: a top-level order whose first leg is a BUY and that is still
// working. A bracket's exit legs, an OCO exit parent (it has no legs of its own), a sell, and an
// order that has filled or ended are never touched, so a position's stop and target survive.
// Cancelling a still-working entry takes its not-yet-active exit legs with it.
//
// After a live cancel the matching stage_order row becomes CANCELLED and a PENDING_FILL
// manage_exit row for the order is cancelled, so the pipeline does not wait for a fill that
// cannot come. It runs on the CLI (`orchestrator cancel_stale_entries`), at /cancel_stale_entries
// and on cron (/cron/cancel_stale_entries).
package cancel_stale_entries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// JobName is this job's name everywhere: package, route, CLI subcommand, cron, job_run.
const JobName = "cancel_stale_entries"

// StaleAfter is how long an entry may rest before it is cancelled.
const StaleAfter = 24 * time.Hour

// lookback is how far back the broker's order list is read. Anything older than StaleAfter
// and still working is found inside it.
const lookback = 30 * 24 * time.Hour

// ErrBusy means another run is in progress.
var ErrBusy = errors.New("cancel_stale_entries: a run is already in progress")

var running sync.Mutex

// Options are the inputs to Run.
type Options struct {
	// DryRun lists what would be cancelled and changes nothing: no cancel, no database write.
	DryRun bool
	// Now is the clock; zero means time.Now().
	Now time.Time
}

// Row is one line of the report.
type Row struct {
	Account string `json:"account"`
	Symbol  string `json:"symbol"`
	OrderID string `json:"order_id"`
	Status  string `json:"status"` // WOULD_CANCEL, CANCELLED, ERROR or NOTHING
	Detail  string `json:"detail"`
}

// Result summarises a run. Cancelled counts orders that would be cancelled in a dry run.
type Result struct {
	Accounts  int   `json:"accounts"`
	Cancelled int   `json:"cancelled"`
	Failed    int   `json:"failed"`
	Rows      []Row `json:"rows"`
}

// IsEntry reports whether o is an entry order: a BUY (to open) as its first leg. An OCO parent
// has no legs and a bracket's exits are sells, so neither counts.
func IsEntry(o *schwab.Order) bool {
	legs, err := trader.ParseOrderLegs(o)
	if err != nil || len(legs) == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(legs[0].Instruction), "BUY")
}

// StaleEntries returns the orders to cancel: the top-level working entry orders in orders that
// were entered more than StaleAfter before now. Children of a bracket are not considered
// (only the top level is read), and an order with no entered time is left alone because its
// age cannot be shown.
func StaleEntries(orders []schwab.Order, now time.Time) []schwab.Order {
	cutoff := now.Add(-StaleAfter)
	var out []schwab.Order
	for i := range orders {
		o := orders[i]
		if o.OrderID == nil || !schwab.IsWorkingStatus(o.Status) || !IsEntry(&o) {
			continue
		}
		if o.EnteredTime == nil || o.EnteredTime.IsZero() || !o.EnteredTime.Time.Before(cutoff) {
			continue
		}
		out = append(out, o)
	}
	return out
}

// Run cancels the stale entries of every account in accts through tc, narrating to out. It
// returns ErrBusy when another run is in progress; a run with failures returns a Result, not an
// error. A live run records a job_run row.
func Run(ctx context.Context, out io.Writer, db *sql.DB, tc *trader.TraderClient, accts []*trader.ResolvedAccount, opts Options) (*Result, error) {
	if !running.TryLock() {
		return nil, ErrBusy
	}
	defer running.Unlock()
	if tc == nil {
		return nil, errors.New("cancel_stale_entries: no broker client")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	started := time.Now().UTC()
	res := &Result{Accounts: len(accts)}
	mode := "LIVE: cancelling stale entry orders"
	if opts.DryRun {
		mode = "DRY RUN: nothing is cancelled or written"
	}
	fmt.Fprintf(out, "[%s] %s\nMode: %s\nAccounts: %d  Entries older than %s are stale\n",
		JobName, started.Format(time.RFC1123), mode, len(accts), StaleAfter)

	for _, acc := range accts {
		cancelAccount(ctx, out, db, tc, acc, now, opts, res)
	}

	summary := fmt.Sprintf("accounts=%d cancelled=%d failed=%d", res.Accounts, res.Cancelled, res.Failed)
	fmt.Fprintf(out, "\nSUMMARY  %s  (%.0fs)\n", summary, time.Since(started).Seconds())
	if !opts.DryRun && db != nil {
		status := job_runs.JobRunOK
		switch {
		case res.Failed > 0:
			status = job_runs.JobRunFailed
		case res.Cancelled == 0:
			status = job_runs.JobRunEmpty
		}
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: JobName, Status: status, Summary: summary,
			RowsWritten: res.Cancelled, StartedAt: started, FinishedAt: time.Now().UTC()}); err != nil {
			fmt.Fprintf(out, "(job_run not recorded: %v)\n", err)
		}
	}
	return res, nil
}

func addRow(out io.Writer, res *Result, r Row) {
	res.Rows = append(res.Rows, r)
	fmt.Fprintf(out, "  %-12s %-9s %-8s %-10s %s\n", r.Status, r.OrderID, r.Account, r.Symbol, r.Detail)
}

func cancelAccount(ctx context.Context, out io.Writer, db *sql.DB, tc *trader.TraderClient, acc *trader.ResolvedAccount, now time.Time, opts Options, res *Result) {
	fmt.Fprintf(out, "\n━━ account %s ━━\n", acc.AccountNumber)
	orders, err := tc.Client.ListAccountOrders(ctx, acc.HashValue, schwab.OrderListRequest{From: now.Add(-lookback), To: now})
	if err != nil {
		res.Failed++
		addRow(out, res, Row{Account: acc.AccountNumber, Status: "ERROR", Detail: "could not list orders, so nothing was cancelled for this account: " + err.Error()})
		return
	}
	stale := StaleEntries(orders, now)
	if len(stale) == 0 {
		addRow(out, res, Row{Account: acc.AccountNumber, Status: "NOTHING", Detail: "no stale entry orders"})
		return
	}
	bySymbol := map[string]string{}
	for i := range stale {
		id := fmt.Sprintf("%d", *stale[i].OrderID)
		bySymbol[id] = trader.OrderSymbol(&stale[i])
	}
	if opts.DryRun {
		res.Cancelled += len(stale)
		for i := range stale {
			id := fmt.Sprintf("%d", *stale[i].OrderID)
			addRow(out, res, Row{Account: acc.AccountNumber, Symbol: bySymbol[id], OrderID: id, Status: "WOULD_CANCEL", Detail: describe(&stale[i])})
		}
		return
	}

	cancelled, errs := manage_exits.CancelOrders(ctx, tc, acc.HashValue, stale)
	for _, id := range cancelled {
		res.Cancelled++
		detail := "cancelled at the broker"
		if db != nil {
			detail += settle(db, acc.AccountNumber, id)
		}
		addRow(out, res, Row{Account: acc.AccountNumber, Symbol: bySymbol[id], OrderID: id, Status: "CANCELLED", Detail: detail})
	}
	for _, e := range errs {
		res.Failed++
		addRow(out, res, Row{Account: acc.AccountNumber, Status: "ERROR", Detail: e.Error()})
	}
}

// reason is written to the rows a cancelled entry leaves behind.
const reason = "cancel_stale_entries: unfilled for over 24h"

// settle updates the database rows of a cancelled entry order and describes what it changed.
func settle(db *sql.DB, account, orderID string) string {
	var notes string
	if n, err := stage_orders.CancelStaleEntry(db, account, orderID, reason); err != nil {
		notes += "; stage_order not updated: " + err.Error()
	} else if n > 0 {
		notes += "; stage_order CANCELLED"
	}
	if ok, err := manage_exits.CancelPendingByOrder(db, account, orderID, reason); err != nil {
		notes += "; manage_exit not updated: " + err.Error()
	} else if ok {
		notes += "; pending manage_exit cancelled"
	}
	return notes
}

func describe(o *schwab.Order) string {
	entered := ""
	if o.EnteredTime != nil {
		entered = o.EnteredTime.Format(time.RFC3339)
	}
	price := ""
	if o.Price != nil {
		price = fmt.Sprintf(" @ %.2f", *o.Price)
	}
	return fmt.Sprintf("%s %s%s %s entered %s", o.OrderType, o.Duration, price, o.Status, entered)
}
