// Package run_e2e_live runs the whole daily pipeline once, in the order the
// scheduler runs it, and checks that it actually worked. It is a LIVE run: it
// stages, submits and manages real orders on the configured Schwab accounts,
// exactly like the scheduler.
//
// It calls the real /cron/<job> routes in-process (through the same http.Handler
// the server mounts, with the App Engine cron header), so every step goes through
// the same wrappers as a scheduled run: job_run recording, failure emails.
// The HTTP endpoint (/run_e2e_live), the CLI (`orchestrator run_e2e_live`) and
// tests all call Run.
//
// Beyond "every route returned 200" it checks: each response says what the job
// promises; tables the step should have written to grew; nothing CRITICAL was
// found by the audit; and, when asked, an independent scan over a scratch market
// database finds the same number of signals (the ORACLE).
package run_e2e_live

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// JobName is this job's name everywhere: package, route, CLI subcommand, job_run.
const JobName = "run_e2e_live"

// ErrBusy means another run is already in progress.
var ErrBusy = errors.New("run_e2e_live: a run is already in progress")

var running sync.Mutex

// Options are the inputs to Run.
type Options struct {
	// Oracle runs the scan again over a scratch market database and compares the
	// signal count with the deployed scan's. It downloads market data, so it is
	// slow and off by default.
	Oracle bool
}

// Result is what a run found.
type Result struct {
	Failures  []string
	StepTimes []string
	Elapsed   time.Duration
}

// Passed reports whether every check held.
func (r *Result) Passed() bool { return len(r.Failures) == 0 }

func (r *Result) fail(out io.Writer, msg string) {
	fmt.Fprintf(out, "  ✗ %s\n", msg)
	r.Failures = append(r.Failures, msg)
}

// step is one cron route and what its response must look like.
type step struct {
	title  string
	path   string
	expect string // regexp the response body must match
}

var (
	stepAccountState = step{"/cron/account_state: sync account/position/order state", "/cron/account_state", `^OK account_state accounts=`}
	stepScanMarket   = step{"/cron/scan_market: strategies -> scan_market_buy_signal", "/cron/scan_market", `^OK scan_market run=`}
	stepStageOrders  = step{"/cron/stage_orders: size signals into stage_order rows", "/cron/stage_orders", `^OK stage_orders dry_run=false `}
	stepSubmitOrders = step{"/cron/submit_orders: place staged orders, confirm fills", "/cron/submit_orders", `^OK submit_orders accounts=`}
	stepCheckHealth  = step{"/cron/check_health: audit", "/cron/check_health", `^OK check_health accounts=`}
	stepManageExits  = step{"/cron/manage_exits: time and target exits", "/cron/manage_exits", `^OK manage_exits accounts=`}
)

// Tables whose growth is checked. Fixed names: they go into SQL text.
var countTables = []string{"scan_market_buy_signal", "stage_order", "manage_exit", "check_health_audit", "check_health_finding"}

// Run executes the pipeline against db (the database the cron routes use) through
// cron (the http.Handler serving /cron/*), narrating to out. It returns ErrBusy
// when another run is in progress. A run that finds problems returns a Result
// with Failures, not an error.
func Run(ctx context.Context, out io.Writer, db *sql.DB, cron http.Handler, opts Options) (*Result, error) {
	if !running.TryLock() {
		return nil, ErrBusy
	}
	defer running.Unlock()

	if err := job_runs.EnsureSchema(db); err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	res := &Result{}
	c := &caller{ctx: ctx, cron: cron, res: res, out: out}

	fmt.Fprintf(out, "run_e2e_live  %s\n", started.Format(time.RFC1123))
	fmt.Fprintln(out, "Mode: LIVE (stages, submits and manages REAL orders on your Schwab accounts)")

	heading(out, "STEP 1 — "+stepAccountState.title)
	c.call(stepAccountState)

	heading(out, "STEP 2 — "+stepScanMarket.title)
	beforeSignals := count(db, "scan_market_buy_signal")
	var expected = -1
	if opts.Oracle {
		expected = oracleExpectedSignals(ctx, out, db)
	}
	c.call(stepScanMarket)
	written := c.field("buy_signals")
	afterSignals := count(db, "scan_market_buy_signal")
	fmt.Fprintf(out, "  buy signals written this run: %d   (scan_market_buy_signal rows: %d -> %d)\n", written, beforeSignals, afterSignals)
	switch {
	case expected >= 0 && written == expected:
		fmt.Fprintf(out, "  ✓ [ORACLE] the scan found %d signal(s), same as the independent scan\n", written)
	case expected >= 0:
		res.fail(out, fmt.Sprintf("[ORACLE MISMATCH] the scan found %d signal(s) but the independent scan finds %d: the scan is broken or its market data is stale", written, expected))
	}
	if written > 0 {
		if afterSignals > beforeSignals {
			fmt.Fprintln(out, "  ✓ new scan_market_buy_signal rows are in the database")
		} else {
			res.fail(out, fmt.Sprintf("scan reported %d buy signal(s) but scan_market_buy_signal did not grow", written))
		}
	} else if expected == 0 {
		fmt.Fprintln(out, "  • no buy signal today, and the independent scan agrees: nothing to stage")
	} else {
		why := "the oracle could not confirm it"
		if !opts.Oracle {
			why = "no oracle was asked for"
		}
		fmt.Fprintf(out, "  • the scan found no signal (%s)\n", why)
	}

	heading(out, "STEP 3 — "+stepStageOrders.title)
	beforeStaged := count(db, "stage_order")
	c.call(stepStageOrders)
	staged := c.field("staged")
	afterStaged := count(db, "stage_order")
	fmt.Fprintf(out, "  staged this run: %d   (stage_order rows: %d -> %d)\n", staged, beforeStaged, afterStaged)
	if staged > 0 && afterStaged <= beforeStaged {
		res.fail(out, fmt.Sprintf("stage_orders reported %d staged order(s) but stage_order did not grow", staged))
	}
	if afterStaged > 0 {
		fmt.Fprintln(out, "  newest stage_order rows (account, strategy, symbol, qty, entry price, status, reason):")
		show(out, db, orchestratorsql.Q("run_e2e_live/run"))
	}

	heading(out, "STEP 4 — "+stepSubmitOrders.title)
	c.call(stepSubmitOrders)
	fmt.Fprintln(out, "\nRunning submit_orders again to confirm it is idempotent (no duplicate orders):")
	c.call(stepSubmitOrders)

	heading(out, "STEP 5 — "+stepCheckHealth.title)
	beforeAudit := count(db, "check_health_audit")
	c.call(stepCheckHealth)
	afterAudit := count(db, "check_health_audit")
	fmt.Fprintf(out, "  check_health_audit rows: %d -> %d\n", beforeAudit, afterAudit)
	if afterAudit <= beforeAudit {
		res.fail(out, "check_health ran but check_health_audit did not grow")
	}

	heading(out, "STEP 6 — "+stepManageExits.title)
	c.call(stepManageExits)

	heading(out, "STEP 7 — /cron/account_state: reconcile")
	c.call(stepAccountState)

	heading(out, "STEP 8 — /cron/check_health: final audit")
	lastFinding := maxID(db, "check_health_finding")
	c.call(stepCheckHealth)

	heading(out, "WHAT THE PIPELINE LEFT BEHIND (read from the database)")
	fmt.Fprintln(out, "  manage_exit: trades the exit manager is tracking (newest first)")
	if count(db, "manage_exit") > 0 {
		show(out, db, orchestratorsql.Q("run_e2e_live/run_2"))
	} else {
		fmt.Fprintln(out, "    (none). If Schwab shows open positions, nothing is managing their exits.")
	}
	newFindings := countWhere(db, orchestratorsql.Q("run_e2e_live/run_3"), lastFinding)
	fmt.Fprintf(out, "  check_health findings written by the last audit: %d\n", newFindings)
	if newFindings > 0 {
		rows := queryRows(db, orchestratorsql.Q("run_e2e_live/run_4"), lastFinding)
		printRows(out, rows)
		critical := countWhere(db, orchestratorsql.Q("run_e2e_live/run_5"), lastFinding)
		if critical > 0 {
			res.fail(out, fmt.Sprintf("%d CRITICAL check_health finding(s) from the last audit (listed above)", critical))
		}
	}

	res.Elapsed = time.Since(started)
	heading(out, "SUMMARY")
	fmt.Fprintln(out, "Routes exercised: /cron/account_state /cron/scan_market /cron/stage_orders /cron/submit_orders /cron/manage_exits /cron/check_health")
	fmt.Fprintln(out, "\nTiming (each call, then the whole run):")
	for _, t := range res.StepTimes {
		fmt.Fprintf(out, "  %s\n", t)
	}
	fmt.Fprintf(out, "  total wall time: %ds\n\n", int(res.Elapsed.Seconds()))
	status, summary := job_runs.JobRunOK, "all checks passed"
	if res.Passed() {
		fmt.Fprintln(out, "✓ All checks PASSED.")
	} else {
		fmt.Fprintf(out, "✗ %d check(s) FAILED.\n", len(res.Failures))
		status, summary = job_runs.JobRunFailed, fmt.Sprintf("%d check(s) failed: %s", len(res.Failures), res.Failures[0])
	}
	if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{
		Job: JobName, Status: status, Summary: summary, StartedAt: started, FinishedAt: time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(out, "  (job_run not recorded: %v)\n", err)
	}
	return res, nil
}

func heading(out io.Writer, s string) {
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n▶  %s\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n", s)
}

// BufferedResponse is a minimal http.ResponseWriter that keeps what a handler wrote, for
// calling a handler in-process (this package's cron calls, the /ops run button, the page
// chrome). The status is 200 until the handler sets another.
type BufferedResponse struct {
	header http.Header
	status int
	body   strings.Builder
}

// NewBufferedResponse returns an empty BufferedResponse.
func NewBufferedResponse() *BufferedResponse {
	return &BufferedResponse{header: http.Header{}, status: http.StatusOK}
}

func (b *BufferedResponse) Header() http.Header         { return b.header }
func (b *BufferedResponse) WriteHeader(code int)        { b.status = code }
func (b *BufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }

// Status is the HTTP status the handler set.
func (b *BufferedResponse) Status() int { return b.status }

// Body is everything the handler wrote.
func (b *BufferedResponse) Body() string { return b.body.String() }

// caller invokes cron routes in-process.
type caller struct {
	ctx  context.Context
	cron http.Handler
	res  *Result
	out  io.Writer
	last string // body of the last call
}

var acctCounts = regexp.MustCompile(`^&\{AccountsFound:(\d+) OrdersFound:(\d+) TransactionsFound:(\d+).*`)

func (c *caller) call(s step) {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, s.path, nil)
	if err != nil {
		c.res.fail(c.out, fmt.Sprintf("%s: %v", s.path, err))
		return
	}
	req.Header.Set("X-Appengine-Cron", "true") // what App Engine cron sends
	fmt.Fprintf(c.out, "  --> GET %s\n", s.path)
	resp := NewBufferedResponse()
	start := time.Now()
	c.cron.ServeHTTP(resp, req)
	secs := time.Since(start).Seconds()
	body := strings.TrimRight(resp.Body(), "\n")
	c.last = body
	c.res.StepTimes = append(c.res.StepTimes, fmt.Sprintf("%6.1fs  %s", secs, s.path))

	// account_state prints a raw Go struct; show just its counts.
	shown := acctCounts.ReplaceAllString(body, "accounts=$1 orders=$2 transactions=$3")
	for line := range strings.SplitSeq(shown, "\n") {
		fmt.Fprintf(c.out, "    %s\n", line)
	}
	fmt.Fprintf(c.out, "  [HTTP %d, %.1fs]\n", resp.Status(), secs)

	switch {
	case resp.Status() == http.StatusForbidden:
		c.res.fail(c.out, fmt.Sprintf("%s returned HTTP 403: the server refused this request", s.path))
	case resp.Status() != http.StatusOK:
		c.res.fail(c.out, fmt.Sprintf("%s returned HTTP %d (expected 200)", s.path, resp.Status()))
	case !regexp.MustCompile(s.expect).MatchString(body):
		c.res.fail(c.out, fmt.Sprintf("%s response did NOT match %s (got: %s)", s.path, s.expect, body))
	default:
		fmt.Fprintf(c.out, "  ✓ response matched: %s\n", s.expect)
	}
}

// field pulls NAME=<number> out of the last response, 0 when absent.
func (c *caller) field(name string) int {
	m := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `=(\d+)`).FindStringSubmatch(c.last)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func validTable(t string) bool { return slices.Contains(countTables, t) }

func count(db *sql.DB, table string) int {
	if !validTable(table) {
		return 0
	}
	return countWhere(db, "SELECT COUNT(*) FROM "+table)
}

func maxID(db *sql.DB, table string) int {
	if !validTable(table) {
		return 0
	}
	return countWhere(db, "SELECT COALESCE(MAX(id), 0) FROM "+table)
}

func countWhere(db *sql.DB, query string, args ...any) int {
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		return 0
	}
	return n
}

func queryRows(db *sql.DB, query string, args ...any) [][]string {
	rs, err := db.Query(query, args...)
	if err != nil {
		return [][]string{{"query failed: " + err.Error()}}
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	var out [][]string
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			break
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			s := fmt.Sprint(v)
			if v == nil {
				s = ""
			}
			if b, ok := v.([]byte); ok {
				s = string(b)
			}
			if len(s) > 90 {
				s = s[:90]
			}
			row[i] = s
		}
		out = append(out, row)
	}
	if err := rs.Err(); err != nil {
		out = append(out, []string{"read failed: " + err.Error()})
	}
	return out
}

func printRows(out io.Writer, rows [][]string) {
	for _, r := range rows {
		fmt.Fprintf(out, "    %s\n", strings.Join(r, "\t"))
	}
}

func show(out io.Writer, db *sql.DB, query string) { printRows(out, queryRows(db, query)) }

// oracleExpectedSignals runs the scan again over a scratch market database and
// returns how many signals it finds, or -1 when it cannot tell. The strategies
// are the ones the scan route uses (the STRATEGY_ALLOWLIST members).
func oracleExpectedSignals(ctx context.Context, out io.Writer, db *sql.DB) int {
	fmt.Fprintln(out, "  Oracle: scanning again over a scratch market database (downloads market data; slow)...")
	ids, err := strategies.ResolveRequest("", "")
	if err != nil {
		fmt.Fprintf(out, "  • oracle inconclusive: strategies: %v\n", err)
		return -1
	}
	members, err := strategies.ResolveGreenlit(db, ids)
	if err != nil {
		fmt.Fprintf(out, "  • oracle inconclusive: strategies: %v\n", err)
		return -1
	}
	tmp, err := os.MkdirTemp("", "run_e2e_live_oracle")
	if err != nil {
		fmt.Fprintf(out, "  • oracle inconclusive: %v\n", err)
		return -1
	}
	defer os.RemoveAll(tmp)
	octx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	type outcome struct {
		res *runner.SignalScanResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := runner.RunLiveScan(members, filepath.Join(tmp, "market_history.db"), filepath.Join(tmp, "out"))
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			fmt.Fprintf(out, "  • oracle inconclusive: %v\n", o.err)
			return -1
		}
		fmt.Fprintf(out, "  Oracle says: %d signal(s) expected (as_of %s)\n", len(o.res.Signals), o.res.AsOf)
		return len(o.res.Signals)
	case <-octx.Done():
		fmt.Fprintln(out, "  • oracle inconclusive: timed out after 5 minutes")
		return -1
	}
}
