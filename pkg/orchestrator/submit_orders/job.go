package submit_orders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// JobOptions are the inputs to RunJob.
type JobOptions struct {
	DryRun bool        // preview only: no broker calls, no job_run row, no email
	Log    *log.Logger // nil = log.Default()
}

// JobResult is the outcome of one submit_orders run across every account.
type JobResult struct {
	Accounts          int
	StagedOrdersFound int
	Submitted         int // accepted by the broker (Filled + PendingFill)
	Filled            int // confirmed filled, position now tracked by manage_exits
	PendingFill       int // accepted, fill not yet confirmed
	Rejected          int
	Unresolved        int      // still PENDING / SUBMITTING / CLAIM_LOST after this run
	Errors            []string // per-account failures and positions that lost exit tracking
	Rows              []Row
	Status            string // job_runs.JobRunOK / JobRunEmpty / JobRunFailed
	Summary           string // one line, the same text recorded in job_run
	EmailErr          error  // nil when sent (or dry run)
}

// RunJob is the submit_orders job: Run for every account, one failing account
// never stopping the rest, then one job_run row, one structured log line and
// one report email. The CLI (RunCommand) and GET /cron/submit_orders both call
// it, so the log line, the email and the run history are identical from either.
//
// A non-nil error means at least one account or position failed; the result is
// still fully populated and the report email has already gone out, so callers
// should return an error status without sending a second failure email.
func RunJob(ctx context.Context, db *sql.DB, tc *trader.TraderClient, accts []*trader.ResolvedAccount, opts JobOptions) (*JobResult, error) {
	lg := opts.Log
	if lg == nil {
		lg = log.Default()
	}
	started := time.Now().UTC()
	out := &JobResult{Accounts: len(accts)}
	if !opts.DryRun && tc == nil {
		return out, errNoLiveClient
	}
	lg.Printf("[submit_orders] start accounts=%d dry_run=%v live_client=%v", len(accts), opts.DryRun, tc != nil)

	for _, acc := range accts {
		res, err := Run(ctx, db, tc, acc, opts.DryRun)
		if res != nil {
			out.StagedOrdersFound += res.StagedOrdersFound
			out.Submitted += res.Submitted
			out.Rejected += res.Rejected
			out.Rows = append(out.Rows, res.Rows...)
			for _, d := range res.Details {
				lg.Printf("[submit_orders] %s: %s", acc.AccountNumber, d)
			}
			lg.Printf("[submit_orders] %s: found=%d submitted=%d rejected=%d",
				acc.AccountNumber, res.StagedOrdersFound, res.Submitted, res.Rejected)
		}
		if err != nil {
			msg := fmt.Sprintf("account %s: %v", acc.AccountNumber, err)
			out.Errors = append(out.Errors, msg)
			out.Rows = append(out.Rows, Row{Account: acc.AccountNumber, Status: "ERROR", Detail: "submit_orders failed for this account: " + err.Error()})
			lg.Printf("[submit_orders] ERROR %s", msg)
		}
	}

	for _, r := range out.Rows {
		switch r.Status {
		case "SUBMITTED":
			out.Filled++
		case "SUBMITTED_PENDING_FILL":
			out.PendingFill++
		case "SUBMITTING", "PENDING", "CLAIM_LOST":
			out.Unresolved++
		case "FAILED_SUBMIT", "ERROR_BUILD_ORDER", "FAILED_CLAIM":
			// An order that could not be built, claimed or sent is a failed run, not a quiet
			// rejection: the job must not report OK while staged orders went nowhere.
			out.Errors = append(out.Errors, fmt.Sprintf("%s %s: %s %s", r.Account, r.Symbol, r.Status, r.Detail))
		case "ERROR":
			// An account-level ERROR row is already in Errors; a lost-tracking
			// ERROR row from openTrade is not.
			if !strings.HasPrefix(r.Detail, "submit_orders failed for this account") {
				out.Errors = append(out.Errors, fmt.Sprintf("%s %s: %s", r.Account, r.Symbol, r.Detail))
			}
		}
	}

	out.Status = job_runs.JobRunOK
	switch {
	case len(out.Errors) > 0:
		out.Status = job_runs.JobRunFailed
	case out.StagedOrdersFound == 0:
		out.Status = job_runs.JobRunEmpty
	}
	out.Summary = fmt.Sprintf("dry_run=%v accounts=%d found=%d submitted=%d filled=%d pending_fill=%d rejected=%d unresolved=%d errors=%d",
		opts.DryRun, out.Accounts, out.StagedOrdersFound, out.Submitted, out.Filled, out.PendingFill, out.Rejected, out.Unresolved, len(out.Errors))

	jobRunState := "skipped(dry_run)"
	emailState := "skipped(dry_run)"
	if !opts.DryRun {
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{
			Job: "submit_orders", Status: out.Status, Summary: out.Summary, RowsWritten: out.Submitted,
			StartedAt: started, FinishedAt: time.Now().UTC(),
		}); err != nil {
			jobRunState = "FAILED(" + err.Error() + ")"
		} else {
			jobRunState = "recorded"
		}

		sum := ReportSummary(out, started)
		subject, body := send_email.BuildJobReportSummary("submit_orders", sum, EmailRows(out.Rows))
		if out.EmailErr = send_email.SendHTMLEmail(db, "submit_orders", subject, body); out.EmailErr != nil {
			emailState = "FAILED(" + out.EmailErr.Error() + ")"
		} else {
			emailState = "sent"
		}
	}
	lg.Printf("[submit_orders] done status=%s %s job_run=%s email=%s", out.Status, out.Summary, jobRunState, emailState)

	if len(out.Errors) > 0 {
		return out, fmt.Errorf("submit_orders: %d error(s): %s", len(out.Errors), strings.Join(out.Errors, "; "))
	}
	return out, nil
}

// ReportSummary is the totals header of the submit_orders email: an outcome in
// the subject and, above the table, what was pending, what happened to it and
// anything the operator has to act on.
func ReportSummary(r *JobResult, started time.Time) send_email.Summary {
	var s send_email.Summary
	switch {
	case len(r.Errors) > 0:
		s.SubjectSuffix = fmt.Sprintf("FAILED (%d error(s)), %d found, %d submitted, %d filled", len(r.Errors), r.StagedOrdersFound, r.Submitted, r.Filled)
	case r.StagedOrdersFound == 0:
		s.SubjectSuffix = "no staged orders"
	default:
		s.SubjectSuffix = fmt.Sprintf("%d found, %d submitted, %d filled, %d pending fill, %d rejected", r.StagedOrdersFound, r.Submitted, r.Filled, r.PendingFill, r.Rejected)
	}
	loc := job_runs.EasternTime()
	s.Lines = append(s.Lines,
		fmt.Sprintf("Run at %s (%s UTC) across %d account(s).", started.In(loc).Format("Mon 2006-01-02 15:04 MST"), started.Format("15:04"), r.Accounts),
		fmt.Sprintf("Staged orders found: %d. Submitted to the broker: %d (filled: %d, accepted with fill unconfirmed: %d). Rejected/failed: %d. Unresolved: %d.",
			r.StagedOrdersFound, r.Submitted, r.Filled, r.PendingFill, r.Rejected, r.Unresolved))
	if r.StagedOrdersFound == 0 && len(r.Errors) == 0 {
		s.Lines = append(s.Lines, "Nothing was pending: no order was staged for this run, so nothing was submitted. Check the stage_orders report if you expected orders.")
	}
	for _, e := range r.Errors {
		s.Lines = append(s.Lines, "ACTION REQUIRED: "+e)
	}
	return s
}

var errNoLiveClient = errors.New("no live Schwab trader client available; refusing to submit orders live. Check SCHWAB_APP_KEY/SCHWAB_APP_SECRET and run `orchestrator auth`")
