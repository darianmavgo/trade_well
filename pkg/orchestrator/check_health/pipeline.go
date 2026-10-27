package check_health

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
)

// pipelineGrace is how long after its scheduled time a job may still be
// running before it counts as having missed the slot (cron jobs run for
// seconds to a few minutes; their handler timeout is 12).
const pipelineGrace = 15 * time.Minute

// pipelineAccount is the account_number on pipeline findings: they describe the
// system, not one account.
const pipelineAccount = "all"

// Verdicts a job can get in PipelineStatus.
const (
	VerdictOK         = "OK"          // recorded a successful run for its latest slot
	VerdictNeverRan   = "NEVER_RAN"   // no job_run row at all
	VerdictMissed     = "MISSED_SLOT" // last recorded run is older than the latest slot
	VerdictFailed     = "FAILED"      // ran for the latest slot and failed
	VerdictNoneDueYet = "NOT_DUE"     // no scheduled slot has come due yet
)

// JobStatus is one scheduled job measured against its latest scheduled slot.
type JobStatus struct {
	Job      string
	Expected time.Time        // latest slot that has had pipelineGrace to finish; zero when NOT_DUE
	Slot     string           // Expected formatted in ET
	Verdict  string           // one of the Verdict constants
	Last     *job_runs.JobRun // most recent recorded run, nil if none
	Detail   string           // the same sentence the finding carries; empty when OK
}

// PipelineStatus measures every job in job_runs.Schedule (pipeline order)
// against its latest scheduled slot, using only job_run. DiagnosePipeline turns
// the broken ones into findings; the /check_health page shows all of them.
func PipelineStatus(db *sql.DB, now time.Time) ([]JobStatus, error) {
	if db == nil {
		return nil, fmt.Errorf("PipelineStatus: nil db")
	}
	if err := job_runs.EnsureSchema(db); err != nil {
		return nil, err
	}
	loc := job_runs.EasternTime()
	out := make([]JobStatus, 0, len(job_runs.Schedule))

	for _, spec := range job_runs.Schedule {
		st := JobStatus{Job: spec.Name}
		last, err := job_runs.LastJobRun(db, spec.Name)
		if err != nil {
			return out, fmt.Errorf("last run of %s: %w", spec.Name, err)
		}
		st.Last = last

		expected, ok := job_runs.LastExpected(spec, now.Add(-pipelineGrace))
		if !ok {
			st.Verdict = VerdictNoneDueYet
			out = append(out, st)
			continue
		}
		st.Expected = expected
		st.Slot = expected.In(loc).Format("Mon 2006-01-02 15:04 MST")
		ago := now.Sub(expected).Round(time.Minute)

		switch {
		case last == nil:
			st.Verdict = VerdictNeverRan
			st.Detail = fmt.Sprintf(
				"expected at %s (%s ago) but job_run has no row for it at all: the cron never reached it, or this database was recreated since (check /ready), or it died before recording",
				st.Slot, ago)
		case last.FinishedAt.Before(expected.Add(-time.Minute)):
			st.Verdict = VerdictMissed
			st.Detail = fmt.Sprintf(
				"expected at %s (%s ago); its last recorded run finished %s ET with status %s: %s",
				st.Slot, ago, last.FinishedAt.In(loc).Format("Mon 01-02 15:04"), last.Status, orNone(last.Summary))
		case last.Status == job_runs.JobRunFailed:
			st.Verdict = VerdictFailed
			st.Detail = fmt.Sprintf(
				"ran for the %s slot at %s ET and FAILED: %s",
				st.Slot, last.FinishedAt.In(loc).Format("15:04"), orNone(last.Summary))
		default:
			st.Verdict = VerdictOK
		}
		out = append(out, st)
	}
	return out, nil
}

// DiagnosePipeline says which scheduled job did not do its job. It turns the
// broken jobs from PipelineStatus into findings, in pipeline order so an
// upstream failure is listed first, and names the first broken job as the root
// cause with the jobs downstream of it. check_health itself is not checked: it
// is the one running.
//
// It only reads job_run, so it works even when the broker, the token or the
// account list is unusable, which is exactly when it is needed.
func DiagnosePipeline(db *sql.DB, now time.Time) ([]Finding, error) {
	// A deliberately stopped pipeline is not a broken one: say so, once, instead of
	// reporting every job as having missed its slot.
	if ks, err := killswitch.Current(db); err == nil && ks.Engaged {
		return []Finding{{
			RunAt: now, AccountNumber: pipelineAccount, CheckName: "killswitch_engaged",
			Category: "job_pipeline", Severity: "WARNING", Subject: killswitch.JobName,
			Detail: ks.String() + ". Every /cron/* route is refused until it is released at /killswitch, so jobs are not running by design.",
		}}, nil
	}
	statuses, err := PipelineStatus(db, now)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	var broken []string
	for _, st := range statuses {
		if st.Job == "check_health" {
			continue
		}
		var check string
		switch st.Verdict {
		case VerdictNeverRan:
			check = "job_never_ran"
		case VerdictMissed:
			check = "job_missed_slot"
		case VerdictFailed:
			check = "job_failed"
		default:
			continue
		}
		findings = append(findings, Finding{
			RunAt: now, AccountNumber: pipelineAccount, CheckName: check,
			Category: "job_pipeline", Severity: "CRITICAL", Subject: st.Job, Detail: st.Detail,
		})
		broken = append(broken, st.Job)
	}

	if len(broken) > 0 {
		downstream := downstreamOf(broken[0])
		detail := fmt.Sprintf("first job in the pipeline that did not do its job: %s (see its finding)", broken[0])
		if len(downstream) > 0 {
			detail += "; everything after it depends on its output: " + strings.Join(downstream, ", ")
		}
		if len(broken) > 1 {
			detail += fmt.Sprintf(". Also broken: %s", strings.Join(broken[1:], ", "))
		}
		findings = append([]Finding{{
			RunAt: now, AccountNumber: pipelineAccount, CheckName: "pipeline_root_cause",
			Category: "job_pipeline", Severity: "CRITICAL", Subject: broken[0], Detail: detail,
		}}, findings...)
	}
	return findings, nil
}

// downstreamOf lists the scheduled jobs after job in pipeline order.
func downstreamOf(job string) []string {
	var out []string
	seen := false
	for _, s := range job_runs.Schedule {
		if s.Name == "check_health" || s.Name == "ready" || s.Name == "weekly_digest" {
			continue // they report on the pipeline; nothing depends on their output
		}
		if seen {
			out = append(out, s.Name)
		}
		if s.Name == job {
			seen = true
		}
	}
	return out
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no summary recorded)"
	}
	return s
}

// PipelineRows runs DiagnosePipeline, records each finding in
// check_health_finding, and returns them as report rows. Both the cron handler
// and the CLI call it once per check_health run, before anything that needs the
// broker, so a broken pipeline is reported even when account resolution fails.
func PipelineRows(db *sql.DB, now time.Time) ([]Row, []Finding, error) {
	findings, err := DiagnosePipeline(db, now)
	for i := range findings {
		_ = RecordFinding(db, &findings[i])
	}
	if err != nil {
		// Never let a diagnosis that could not run read as "all clear".
		findings = append(findings, Finding{
			RunAt: now, AccountNumber: pipelineAccount, CheckName: "pipeline_diagnosis_error",
			Category: "job_pipeline", Severity: "CRITICAL", Subject: "check_health",
			Detail: "the pipeline diagnosis could not run, so no job was verified: " + err.Error(),
		})
	}
	rows := make([]Row, 0, len(findings))
	for _, f := range findings {
		rows = append(rows, Row{
			Account: f.AccountNumber, Strategy: f.Category, Symbol: f.Subject,
			Signal: f.CheckName, Status: f.Severity, Detail: f.Detail,
		})
	}
	return rows, findings, err
}
