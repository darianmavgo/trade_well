package ops

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

func TestBoardShowsEachJobWithItsLatestSlot(t *testing.T) {
	db, err := bootstrap.OpenDB(filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	loc := job_runs.EasternTime()
	now := time.Date(2026, 10, 14, 11, 0, 0, 0, loc) // Wednesday
	rec := func(job, status, sum string, at time.Time, took time.Duration) {
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: job, Status: status, Summary: sum, StartedAt: at, FinishedAt: at.Add(took)}); err != nil {
			t.Fatal(err)
		}
	}
	rec("scan_market", job_runs.JobRunFailed, "LIVESCAN_FAILED", time.Date(2026, 10, 13, 18, 0, 0, 0, loc), 2*time.Second)
	rec("check_health", job_runs.JobRunOK, "OK check_health", time.Date(2026, 10, 14, 10, 0, 0, 0, loc), 90*time.Second)
	rec("trigger_limits", job_runs.JobRunOK, "placed 1", time.Date(2026, 10, 14, 9, 31, 0, 0, loc), time.Second)

	b, err := Build(db, now)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]Row{}
	for _, r := range b.Rows {
		rows[r.Job] = r
	}
	if r := rows["scan_market"]; r.Severity != "bad" || r.Last != "FAILED" || r.Took != "2s" || r.Live {
		t.Errorf("scan_market row %+v", r)
	}
	if r := rows["check_health"]; r.Severity != "ok" || r.Took != "1m30s" || r.Next != "Thu 10-15 10:00" {
		t.Errorf("check_health row %+v", r)
	}
	if r := rows["trigger_limits"]; !r.Live || r.Verdict != "RECORDS_ON_ACTION" || r.Last != "OK" {
		t.Errorf("trigger_limits row %+v", r)
	}
	// ready's first slot (Wed 09:00) came due and nothing ran it: that is a finding, not silence.
	if r := rows["ready"]; r.Severity != "bad" || r.Verdict != "NEVER_RAN" {
		t.Errorf("ready row %+v, want bad NEVER_RAN", r)
	}
	before, err := Build(db, time.Date(2026, 10, 13, 12, 0, 0, 0, loc)) // before ready's NotBefore date
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range before.Rows {
		if r.Job == "ready" && r.Severity != "ok" {
			t.Errorf("ready before its first day: %+v, want ok", r)
		}
	}
	if b.Status != "bad" {
		t.Errorf("board status %s, want bad (scan_market failed)", b.Status)
	}
	if !IsLive("submit_orders") || IsLive("scan_market") {
		t.Error("IsLive wrong")
	}
}
