// Package clear_stale is the operator tool that stops check_health from reporting on
// strategies you no longer run. A strategy that is not greenlit (not on STRATEGY_ALLOWLIST,
// directly or as a member of a greenlit stack) can no longer stage, submit or manage anything,
// so its leftovers only produce noise: REJECTED or PENDING staged orders, and manage_exit rows
// still OPEN for a position the broker mirror (refreshed after the entry filled) no longer
// holds. Those are retired here. A greenlit strategy's rows are never touched, and neither is
// a position the broker still holds, whatever its strategy.
//
// It touches only the orchestrator database (Turso), never the broker. Nothing is deleted: a staged order
// becomes CLEARED (its reject_reason is kept) and an orphan trade becomes CLOSED_MANUAL with
// reason STALE_CLEARED. It is an operator action, not a scheduled job: it is on the CLI
// (`orchestrator clear_stale`) and behind /clear_stale, and is deliberately NOT a /cron route.
package clear_stale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// JobName is this job's name everywhere: package, route, CLI subcommand, job_run.
const JobName = "clear_stale"

// ErrBusy means another run is in progress.
var ErrBusy = errors.New("clear_stale: a run is already in progress")

var running sync.Mutex

const (
	// StatusCleared is the stage_order status of a row retired by this tool.
	StatusCleared = "CLEARED"
	// ReasonStaleCleared is the exit reason of a manage_exit row closed by this tool.
	ReasonStaleCleared = "STALE_CLEARED"
)

// Options are the inputs to Run.
type Options struct {
	// Allowlist is the greenlit strategy ids and stacks (STRATEGY_ALLOWLIST). Required: with
	// no allowlist every strategy would look retired, so Run refuses.
	Allowlist []string
	// DryRun lists what would be cleared and changes nothing: the same statements run
	// inside a transaction that is rolled back.
	DryRun bool
	// Now is the clock; zero means time.Now().
	Now time.Time
}

// Row is one line of the report.
type Row struct {
	Kind    string `json:"kind"` // rejected_order, pending_order, orphan_trade or kept_trade
	Account string `json:"account"`
	Symbol  string `json:"symbol"`
	ID      int64  `json:"id"`
	Detail  string `json:"detail"`
}

// Result summarises a run. The counts include rows that would be cleared in a dry run.
type Result struct {
	Rejected int   `json:"rejected_orders"` // REJECTED staged orders cleared
	Pending  int   `json:"pending_orders"`  // stale PENDING staged orders cleared
	Orphans  int   `json:"orphan_trades"`   // OPEN trades closed because the broker no longer holds the position
	Kept     int   `json:"kept_trades"`     // orphan candidates left alone because the mirror may simply be behind
	Rows     []Row `json:"rows"`
}

// Selects. One stage each; Go only orders them and applies the updates. The SQL is in
// sql/orchestrator/clear_stale. selectOrphanTrades is the same rule as check_health's
// orphan_active_trade, plus the two times Go compares.
var (
	selectStaleOrders  = orchestratorsql.Q("clear_stale/select_stale_orders")
	selectOrphanTrades = orchestratorsql.Q("clear_stale/select_orphan_trades")
)

// The two updates build their text from the status constants, so they stay here.
const (
	clearOrder = `
		UPDATE stage_order
		SET status = '` + StatusCleared + `', reject_reason = TRIM(IFNULL(reject_reason, '') || ' [cleared as stale]')
		WHERE id = ? AND status = ?`
	closeTrade = `
		UPDATE manage_exit
		SET status = 'CLOSED_MANUAL', exit_reason = '` + ReasonStaleCleared + `', exit_reason_code = '` + ReasonStaleCleared + `', exit_time = ?
		WHERE id = ? AND status = 'OPEN'`
)

// Run clears the stale rows and writes a report to out. It returns ErrBusy when another
// run is in progress. A live run is recorded as a clear_stale job_run.
func Run(ctx context.Context, out io.Writer, db *sql.DB, opts Options) (*Result, error) {
	if db == nil {
		return nil, errors.New("clear_stale: nil db")
	}
	if !running.TryLock() {
		return nil, ErrBusy
	}
	defer running.Unlock()

	if len(opts.Allowlist) == 0 {
		return nil, errors.New("clear_stale: no greenlit strategies (STRATEGY_ALLOWLIST is empty): refusing to treat every strategy as retired")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	mode := "LIVE: rows of strategies that are not greenlit are cleared in Turso (the broker is not contacted)"
	if opts.DryRun {
		mode = "DRY RUN: nothing is written"
	}
	fmt.Fprintf(out, "clear_stale  %s\nMode: %s\nGreenlit: %s\n",
		now.Format(time.RFC1123), mode, strings.Join(opts.Allowlist, ", "))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("clear_stale: begin: %w", err)
	}
	defer tx.Rollback()

	res := &Result{}
	if res.Rejected, err = clearOrders(ctx, out, tx, res, opts.Allowlist, "REJECTED", "rejected_order"); err != nil {
		return nil, err
	}
	if res.Pending, err = clearOrders(ctx, out, tx, res, opts.Allowlist, "PENDING", "pending_order"); err != nil {
		return nil, err
	}
	if err := closeOrphans(ctx, out, tx, res, opts.Allowlist, now); err != nil {
		return nil, err
	}
	if opts.DryRun {
		tx.Rollback()
	} else if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("clear_stale: commit: %w", err)
	}

	fmt.Fprintf(out, "\nSUMMARY  rejected_orders=%d pending_orders=%d orphan_trades=%d kept_trades=%d\n",
		res.Rejected, res.Pending, res.Orphans, res.Kept)
	if !opts.DryRun {
		summary := fmt.Sprintf("rejected_orders=%d pending_orders=%d orphan_trades=%d kept_trades=%d", res.Rejected, res.Pending, res.Orphans, res.Kept)
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: JobName, Status: job_runs.JobRunOK, Summary: summary,
			RowsWritten: res.Rejected + res.Pending + res.Orphans, StartedAt: now, FinishedAt: time.Now().UTC()}); err != nil {
			fmt.Fprintf(out, "(job_run not recorded: %v)\n", err)
		}
	}
	return res, nil
}

func addRow(out io.Writer, res *Result, r Row) {
	res.Rows = append(res.Rows, r)
	fmt.Fprintf(out, "%-14s %-10s %-8s id=%d  %s\n", r.Kind, r.Account, r.Symbol, r.ID, r.Detail)
}

type orderRow struct {
	id                                          int64
	account, symbol, strategy, reason, stagedAt string
}

// clearOrders retires every staged order with the given status whose strategy is not greenlit.
func clearOrders(ctx context.Context, out io.Writer, tx *sql.Tx, res *Result, allow []string, status, kind string) (int, error) {
	rows, err := tx.QueryContext(ctx, selectStaleOrders, status)
	if err != nil {
		return 0, fmt.Errorf("clear_stale: list %s orders: %w", status, err)
	}
	var found []orderRow
	for rows.Next() {
		var o orderRow
		if err := rows.Scan(&o.id, &o.account, &o.symbol, &o.strategy, &o.reason, &o.stagedAt); err != nil {
			rows.Close()
			return 0, err
		}
		if !strategies.MemberGreenlit(allow, o.strategy) {
			found = append(found, o)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, o := range found {
		if _, err := tx.ExecContext(ctx, clearOrder, o.id, status); err != nil {
			return 0, fmt.Errorf("clear_stale: clear order %d: %w", o.id, err)
		}
		detail := o.strategy + " is not greenlit; staged " + o.stagedAt
		if o.reason != "" {
			detail += "; " + o.reason
		}
		addRow(out, res, Row{Kind: kind, Account: o.account, Symbol: o.symbol, ID: o.id, Detail: detail})
	}
	return len(found), nil
}

// closeOrphans closes an OPEN trade the broker mirror does not hold, but only when the
// mirror was refreshed after the entry filled. A mirror older than the fill simply has not
// seen the position yet (a position that filled this morning), so that trade is kept.
func closeOrphans(ctx context.Context, out io.Writer, tx *sql.Tx, res *Result, allow []string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, selectOrphanTrades)
	if err != nil {
		return fmt.Errorf("clear_stale: list orphan trades: %w", err)
	}
	type orphan struct {
		id                                                       int64
		account, symbol, strategy, strategyID, filledAt, fetched string
	}
	var found []orphan
	for rows.Next() {
		var o orphan
		if err := rows.Scan(&o.id, &o.account, &o.symbol, &o.strategy, &o.strategyID, &o.filledAt, &o.fetched); err != nil {
			rows.Close()
			return err
		}
		// A trade is still running if either id it was recorded under is greenlit.
		if !strategies.MemberGreenlit(allow, o.strategy) && (o.strategyID == "" || !strategies.MemberGreenlit(allow, o.strategyID)) {
			found = append(found, o)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, o := range found {
		filled, okF := parseStored(o.filledAt)
		fetched, okM := parseStored(o.fetched)
		switch {
		case !okF || !okM:
			res.Kept++
			addRow(out, res, Row{Kind: "kept_trade", Account: o.account, Symbol: o.symbol, ID: o.id,
				Detail: "kept: entry fill time or broker mirror time unknown, cannot tell a closed position from a mirror that is behind"})
		case !fetched.After(filled):
			res.Kept++
			addRow(out, res, Row{Kind: "kept_trade", Account: o.account, Symbol: o.symbol, ID: o.id,
				Detail: fmt.Sprintf("kept: broker mirror (%s) is older than the entry fill (%s); run account_state first", fetched.Format(time.RFC3339), filled.Format(time.RFC3339))})
		default:
			if _, err := tx.ExecContext(ctx, closeTrade, now, o.id); err != nil {
				return fmt.Errorf("clear_stale: close trade %d: %w", o.id, err)
			}
			res.Orphans++
			addRow(out, res, Row{Kind: "orphan_trade", Account: o.account, Symbol: o.symbol, ID: o.id,
				Detail: fmt.Sprintf("%s is not greenlit and the broker does not hold it as of %s: closed as %s", o.strategy, fetched.Format(time.RFC3339), ReasonStaleCleared)})
		}
	}
	return nil
}

// stored time layouts: a Go time.Time written by the SQLite driver, and RFC 3339.
var storedLayouts = []string{
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999 -0700 -0700",
	time.RFC3339Nano,
	"2006-01-02 15:04:05",
}

func parseStored(s string) (time.Time, bool) {
	for _, l := range storedLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
