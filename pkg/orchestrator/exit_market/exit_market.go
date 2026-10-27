// Package exit_market is the liquidation tool: for every managed account it cancels
// every working order and sells every long equity/ETF position at market. It is an
// operator action, not a scheduled job: it is on the CLI (`orchestrator exit_market`)
// and behind /exit_market, and is deliberately NOT a /cron route or in cron.yaml.
//
// It is live money in the strongest sense. Run with DryRun first to see the plan.
// Options that cannot be sold safely by this tool (shorts, options, mutual funds,
// cash equivalents, fractional remainders) are reported as skipped, never guessed at.
package exit_market

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// JobName is this job's name everywhere: package, route, CLI subcommand, job_run.
const JobName = "exit_market"

// ErrBusy means another run is in progress.
var ErrBusy = errors.New("exit_market: a run is already in progress")

var running sync.Mutex

// Options are the inputs to Run.
type Options struct {
	// DryRun prints the plan and changes nothing: no cancel, no sell, no database write.
	DryRun bool
	// Killswitch engages the killswitch after a live run, so the next cron does not
	// stage and buy again what was just sold.
	Killswitch bool
}

// Row is one line of the report.
type Row struct {
	Account, Symbol, Action, Status, Detail string
}

// Result summarises a run.
type Result struct {
	Accounts  int
	Cancelled int // working orders cancelled (or that would be, in a dry run)
	Sold      int // positions sold at market (or that would be)
	Failed    int
	Skipped   int
	Rows      []Row
}

// SellPlan is one position to sell.
type SellPlan struct {
	Symbol string
	Qty    float64
}

// Skip is a position this tool will not sell, and why.
type Skip struct {
	Symbol string
	Reason string
}

// AccountPlan is what would be done for one account.
type AccountPlan struct {
	Account string
	Cancel  []schwab.Order // every working order, any symbol, either side
	Sells   []SellPlan
	Skipped []Skip
}

// sellableAsset reports whether the tool may market-sell an instrument of this type.
// Schwab reports stocks and ETFs as EQUITY or COLLECTIVE_INVESTMENT; a market order
// for either is an EQUITY order.
func sellableAsset(t string) bool {
	switch strings.ToUpper(t) {
	case "EQUITY", "COLLECTIVE_INVESTMENT", "":
		return true
	}
	return false
}

// Plan works out what exiting the market means for acc, given the account's recent
// orders: cancel everything working (a bracket's take-profit and stop legs reserve the
// shares, and a working buy would fill after the sell), then sell each long position
// in whole shares. It changes nothing.
func Plan(acc *trader.ResolvedAccount, orders []schwab.Order) AccountPlan {
	p := AccountPlan{Account: acc.AccountNumber, Cancel: manage_exits.WorkingOrders(orders)}
	for _, pos := range acc.Details.Positions {
		sym := strings.ToUpper(strings.TrimSpace(pos.Instrument.Symbol))
		switch {
		case sym == "":
			continue
		case pos.ShortQuantity > 0:
			p.Skipped = append(p.Skipped, Skip{sym, fmt.Sprintf("short position of %.0f: covering is not automated, do it by hand", pos.ShortQuantity)})
		case !sellableAsset(pos.Instrument.AssetType):
			p.Skipped = append(p.Skipped, Skip{sym, "asset type " + pos.Instrument.AssetType + " is not sold by this tool"})
		case pos.LongQuantity <= 0:
			continue
		default:
			whole := math.Floor(pos.LongQuantity)
			if whole < 1 {
				p.Skipped = append(p.Skipped, Skip{sym, fmt.Sprintf("only a fractional %.4f shares: cannot be sold by a share order", pos.LongQuantity)})
				continue
			}
			p.Sells = append(p.Sells, SellPlan{Symbol: sym, Qty: whole})
			if frac := pos.LongQuantity - whole; frac > 1e-9 {
				p.Skipped = append(p.Skipped, Skip{sym, fmt.Sprintf("fractional remainder of %.4f share(s) left in the account", frac)})
			}
		}
	}
	return p
}

// Run exits the market for every account in accts through tc, narrating to out. A live
// run also does the bookkeeping that keeps the pipeline from undoing it: OPEN manage_exit
// rows for the sold symbols are closed (CLOSED_MANUAL), pending entries are cancelled,
// and staged orders still waiting to be submitted are rejected. It returns ErrBusy when
// another run is in progress; a run with failures returns a Result, not an error.
func Run(ctx context.Context, out io.Writer, db *sql.DB, tc *trader.TraderClient, accts []*trader.ResolvedAccount, opts Options) (*Result, error) {
	if !running.TryLock() {
		return nil, ErrBusy
	}
	defer running.Unlock()
	if tc == nil && !opts.DryRun {
		return nil, errors.New("exit_market: no live broker client")
	}
	started := time.Now().UTC()
	res := &Result{Accounts: len(accts)}
	mode := "LIVE: cancelling orders and SELLING at market"
	if opts.DryRun {
		mode = "DRY RUN: nothing is cancelled, sold or written"
	}
	fmt.Fprintf(out, "exit_market  %s\nMode: %s\nAccounts: %d\n", started.Format(time.RFC1123), mode, len(accts))

	for _, acc := range accts {
		exitAccount(ctx, out, db, tc, acc, opts, res)
	}

	if !opts.DryRun && opts.Killswitch && db != nil {
		if _, err := killswitch.Set(db, true, JobName, "exit_market sold everything", time.Now()); err != nil {
			addRow(out, res, Row{Action: "KILLSWITCH", Status: "ERROR", Detail: "could not engage the killswitch: " + err.Error()})
		} else {
			killed := killswitch.KillAll()
			addRow(out, res, Row{Action: "KILLSWITCH", Status: "ENGAGED", Detail: fmt.Sprintf("cron is off; %d running execution(s) cancelled. Release it at /killswitch when ready.", len(killed))})
		}
	}

	fmt.Fprintf(out, "\nSUMMARY  accounts=%d cancelled=%d sold=%d skipped=%d failed=%d  (%.0fs)\n",
		res.Accounts, res.Cancelled, res.Sold, res.Skipped, res.Failed, time.Since(started).Seconds())
	if !opts.DryRun && db != nil {
		status := job_runs.JobRunOK
		if res.Failed > 0 {
			status = job_runs.JobRunFailed
		}
		summary := fmt.Sprintf("accounts=%d cancelled=%d sold=%d skipped=%d failed=%d", res.Accounts, res.Cancelled, res.Sold, res.Skipped, res.Failed)
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: JobName, Status: status, Summary: summary, RowsWritten: res.Sold, StartedAt: started, FinishedAt: time.Now().UTC()}); err != nil {
			fmt.Fprintf(out, "(job_run not recorded: %v)\n", err)
		}
		rows := make([]send_email.Row, len(res.Rows))
		for i, r := range res.Rows {
			rows[i] = send_email.Row{Account: r.Account, Symbol: r.Symbol, Signal: r.Action, Status: r.Status, Detail: r.Detail}
		}
		subject, body := send_email.BuildJobReportSummary(JobName, send_email.Summary{
			SubjectSuffix: fmt.Sprintf("sold %d, cancelled %d orders, %d failed", res.Sold, res.Cancelled, res.Failed),
			Lines:         []string{"Operator-triggered liquidation at " + started.Format(time.RFC1123) + ". " + summary},
		}, rows)
		if err := send_email.SendHTMLEmail(db, JobName, subject, body); err != nil {
			fmt.Fprintf(out, "(report email not sent: %v)\n", err)
		}
	}
	return res, nil
}

func addRow(out io.Writer, res *Result, r Row) {
	res.Rows = append(res.Rows, r)
	fmt.Fprintf(out, "  %-9s %-8s %-10s %s\n", r.Status, r.Account, r.Symbol, r.Detail)
}

func exitAccount(ctx context.Context, out io.Writer, db *sql.DB, tc *trader.TraderClient, acc *trader.ResolvedAccount, opts Options, res *Result) {
	fmt.Fprintf(out, "\n━━ account %s ━━\n", acc.AccountNumber)
	now := time.Now()
	var orders []schwab.Order
	if tc != nil {
		raw, err := tc.Client.ListAccountOrders(ctx, acc.HashValue, schwab.OrderListRequest{From: now.AddDate(0, 0, -30), To: now})
		if err != nil {
			res.Failed++
			addRow(out, res, Row{Account: acc.AccountNumber, Action: "LIST_ORDERS", Status: "ERROR", Detail: "could not list orders, so nothing was cancelled or sold for this account: " + err.Error()})
			return
		}
		orders = raw
	}
	plan := Plan(acc, orders)
	for _, sk := range plan.Skipped {
		res.Skipped++
		addRow(out, res, Row{Account: acc.AccountNumber, Symbol: sk.Symbol, Action: "SKIP", Status: "SKIPPED", Detail: sk.Reason})
	}

	if opts.DryRun {
		res.Cancelled += len(plan.Cancel)
		for _, o := range plan.Cancel {
			addRow(out, res, Row{Account: acc.AccountNumber, Symbol: trader.OrderSymbol(&o), Action: "CANCEL", Status: "WOULD_CANCEL", Detail: describeOrder(&o)})
		}
		for _, s := range plan.Sells {
			res.Sold++
			addRow(out, res, Row{Account: acc.AccountNumber, Symbol: s.Symbol, Action: "SELL", Status: "WOULD_SELL", Detail: fmt.Sprintf("%.0f shares at market", s.Qty)})
		}
		if len(plan.Cancel) == 0 && len(plan.Sells) == 0 {
			addRow(out, res, Row{Account: acc.AccountNumber, Action: "NONE", Status: "NOTHING", Detail: "no working orders and no sellable positions"})
		}
		return
	}

	// 1. Cancel everything working, then trust the broker's list, not the replies. A
	// rate-limited or lost cancel is retried in further rounds until the broker shows
	// nothing working, so one throttled call cannot leave an order behind.
	const maxRounds = 5
	toCancel := plan.Cancel
	var fresh []schwab.Order
	var left []schwab.Order
	for round := 1; round <= maxRounds; round++ {
		if len(toCancel) > 0 {
			cancelled, errs := manage_exits.CancelOrders(ctx, tc, acc.HashValue, toCancel)
			res.Cancelled += len(cancelled)
			fmt.Fprintf(out, "  round %d: cancelled %d of %d working order(s) (%d cancel error(s))\n", round, len(cancelled), len(toCancel), len(errs))
			for _, e := range errs {
				fmt.Fprintf(out, "    %v\n", e)
			}
			// Let the broker settle (and its rate limit recover) before re-listing.
			select {
			case <-ctx.Done():
			case <-time.After(time.Duration(round) * 2 * time.Second):
			}
		}
		after := time.Now()
		var err error
		fresh, err = tc.Client.ListAccountOrders(ctx, acc.HashValue, schwab.OrderListRequest{From: after.AddDate(0, 0, -7), To: after})
		if err != nil {
			res.Failed++
			addRow(out, res, Row{Account: acc.AccountNumber, Action: "VERIFY", Status: "ERROR", Detail: "could not re-list orders after cancelling, so nothing was sold: " + err.Error()})
			return
		}
		left = manage_exits.WorkingOrders(fresh)
		if len(left) == 0 {
			break
		}
		toCancel = left
	}
	if len(left) > 0 {
		res.Failed++
		addRow(out, res, Row{Account: acc.AccountNumber, Action: "VERIFY", Status: "ERROR", Detail: fmt.Sprintf("%d order(s) are still working at the broker after %d rounds; cancel them by hand, then re-run. A sell of a symbol they reserve will be refused", len(left), maxRounds)})
	} else if len(plan.Cancel) > 0 {
		fmt.Fprintf(out, "  verified: no working orders left at the broker\n")
	}

	// 2. Sell each position at market.
	sold := map[string]float64{} // symbol -> last price, for closing the manage_exit row
	for _, s := range plan.Sells {
		if err := manage_exits.ExitAtMarket(ctx, tc, acc, s.Symbol, s.Qty, fresh); err != nil {
			res.Failed++
			addRow(out, res, Row{Account: acc.AccountNumber, Symbol: s.Symbol, Action: "SELL", Status: "ERROR", Detail: err.Error()})
			continue
		}
		res.Sold++
		last := 0.0
		if q, qerr := tc.Client.GetQuote(ctx, s.Symbol); qerr == nil {
			last = trader.ExtractQuoteLastPrice(q)
		}
		sold[s.Symbol] = last
		addRow(out, res, Row{Account: acc.AccountNumber, Symbol: s.Symbol, Action: "SELL", Status: "SOLD", Detail: fmt.Sprintf("%.0f shares at market (last ~$%.2f)", s.Qty, last)})
	}

	// 3. Keep the pipeline from undoing it.
	if db != nil {
		for _, line := range Settle(db, acc.AccountNumber, sold, time.Now().UTC()) {
			fmt.Fprintf(out, "  %s\n", line)
		}
	}
}

// Settle does the bookkeeping after a live exit of account: it closes the OPEN
// manage_exit rows of the sold symbols (CLOSED_MANUAL, reason EXIT_MARKET), cancels
// PENDING_FILL entries (their orders were cancelled), and rejects staged orders still
// waiting for submit_orders, so tomorrow's 09:28 run does not buy it all back. sold maps
// symbol to a last price (0 when unknown, then the entry price is used). It returns a
// line per change.
func Settle(db *sql.DB, account string, sold map[string]float64, now time.Time) []string {
	var lines []string
	open, err := manage_exits.GetOpenTrades(db, account)
	if err != nil {
		return []string{"could not read open trades: " + err.Error()}
	}
	for _, t := range open {
		price, ok := sold[strings.ToUpper(t.Symbol)]
		if !ok {
			continue
		}
		if price <= 0 {
			price = t.EntryPrice
		}
		if err := manage_exits.CloseActiveTrade(db, t.ID, manage_exits.TradeStatusClosedManual, "EXIT_MARKET", price, now); err != nil {
			lines = append(lines, fmt.Sprintf("could not close %s trade %d: %v", t.Symbol, t.ID, err))
			continue
		}
		lines = append(lines, fmt.Sprintf("closed manage_exit %s (%s) as CLOSED_MANUAL", t.Symbol, t.EntryOrderID))
	}
	pending, err := manage_exits.GetPendingFillTrades(db, account)
	if err == nil {
		for _, p := range pending {
			if err := manage_exits.CancelPending(db, p.ID, "exit_market cancelled the entry"); err == nil {
				lines = append(lines, fmt.Sprintf("cancelled pending entry %s (%s)", p.Symbol, p.EntryOrderID))
			}
		}
	}
	staged, err := stage_orders.GetPendingStagedOrders(db, account)
	if err == nil {
		for _, so := range staged {
			if err := stage_orders.MarkStagedOrderRejected(db, so.ID, "exit_market: liquidated, not submitting"); err == nil {
				lines = append(lines, fmt.Sprintf("rejected staged order %s (id %d)", so.Symbol, so.ID))
			}
		}
	}
	return lines
}

func describeOrder(o *schwab.Order) string {
	side := "BUY"
	if trader.OrderIsSell(o) {
		side = "SELL"
	}
	id := int64(0)
	if o.OrderID != nil {
		id = *o.OrderID
	}
	return fmt.Sprintf("%s %s order %d (%s)", strings.ToUpper(o.Status), side, id, o.OrderType)
}
