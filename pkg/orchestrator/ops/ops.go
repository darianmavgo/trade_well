// Package ops builds the /ops board: every cron job with its next slot, how its latest slot
// went, and how long it took, from the job_run history. It only reads.
package ops

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
)

// LiveJobs place, change or cancel real orders. The board runs them for real only after the
// operator types the job's name; every other job can be run with one click.
var LiveJobs = []string{"stage_orders", "submit_orders", "trigger_limits", "manage_exits"}

// IsLive reports whether job can place, change or cancel real orders.
func IsLive(job string) bool {
	for _, j := range LiveJobs {
		if j == job {
			return true
		}
	}
	return false
}

// DryRunnable are the jobs whose /cron handler takes ?dry_run=1 (the CLI's --dry-run): they size,
// report or preview without placing, selling or emailing. submit_orders has no dry run.
var DryRunnable = []string{"scan_market", "stage_orders", "trigger_limits", "manage_exits", "ready", "weekly_digest"}

// CanDryRun reports whether job has a dry run.
func CanDryRun(job string) bool {
	for _, j := range DryRunnable {
		if j == job {
			return true
		}
	}
	return false
}

// Row is one job on the board.
type Row struct {
	Job      string `json:"job"`
	Verdict  string `json:"verdict"` // a check_health verdict, or "RECORDS_ON_ACTION"
	Slot     string `json:"slot"`    // the latest slot that has had time to finish, in ET
	Last     string `json:"last"`    // status of the latest recorded run
	LastAt   string `json:"last_at"` // when it finished, in ET
	Took     string `json:"took"`
	Summary  string `json:"summary"`
	Next     string `json:"next"`    // next scheduled slot, in ET
	Live     bool   `json:"live"`    // can place real orders
	DryRun   bool   `json:"dry_run"` // has a dry run
	Detail   string `json:"detail,omitempty"`
	Severity string `json:"severity"` // ok, warn or bad, for the status tag
}

// Board is the whole page.
type Board struct {
	GeneratedAt time.Time `json:"generated_at"`
	Status      string    `json:"status"` // worst severity on the board
	Killswitch  string    `json:"killswitch"`
	Rows        []Row     `json:"rows"`
}

const timeFmt = "Mon 01-02 15:04"

// Build reads job_run and the schedule.
func Build(db *sql.DB, now time.Time) (Board, error) {
	b := Board{GeneratedAt: now.UTC(), Status: "ok", Killswitch: "released"}
	if st, err := killswitch.Current(db); err == nil && st.Engaged {
		b.Killswitch = st.String()
		b.Status = "bad"
	}
	sts, err := check_health.PipelineStatus(db, now)
	if err != nil {
		return b, err
	}
	loc := job_runs.EasternTime()
	for i, st := range sts {
		r := Row{Job: st.Job, Verdict: st.Verdict, Slot: st.Slot, Live: IsLive(st.Job), DryRun: CanDryRun(st.Job), Detail: st.Detail}
		fill(&r, st.Last, loc)
		if next, ok := job_runs.NextExpected(job_runs.Schedule[i], now); ok {
			r.Next = next.In(loc).Format(timeFmt)
		}
		switch st.Verdict {
		case check_health.VerdictOK:
			r.Severity = "ok"
		case check_health.VerdictNoneDueYet:
			r.Severity = "ok" // no slot has come due yet (a job added to the schedule today)
		default:
			r.Severity = "bad"
		}
		b.Status = worst(b.Status, r.Severity)
		b.Rows = append(b.Rows, r)
	}
	// Jobs outside Schedule: they record a run only when they acted.
	for _, name := range job_runs.NotRecordedEachRun {
		last, err := job_runs.LastJobRun(db, name)
		if err != nil {
			return b, err
		}
		r := Row{Job: name, Verdict: "RECORDS_ON_ACTION", Live: IsLive(name), DryRun: CanDryRun(name), Severity: "ok", Slot: "every 5 min, market hours"}
		fill(&r, last, loc)
		if last != nil && last.Status == job_runs.JobRunFailed {
			r.Severity = "bad"
			b.Status = worst(b.Status, "bad")
		}
		b.Rows = append(b.Rows, r)
	}
	return b, nil
}

func fill(r *Row, last *job_runs.JobRun, loc *time.Location) {
	if last == nil {
		r.Last, r.Summary = "never", "no run recorded"
		return
	}
	r.Last = last.Status
	r.LastAt = last.FinishedAt.In(loc).Format(timeFmt)
	r.Took = took(last.FinishedAt.Sub(last.StartedAt))
	r.Summary = last.Summary
}

func took(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func worst(a, b string) string {
	rank := map[string]int{"ok": 0, "warn": 1, "bad": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
