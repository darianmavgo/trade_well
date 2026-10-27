package check_health

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	_ "modernc.org/sqlite"
)

func pipelineDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := job_runs.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func et(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, job_runs.EasternTime())
}

func ran(t *testing.T, db *sql.DB, job, status, summary string, finished time.Time) {
	t.Helper()
	if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{
		Job: job, Status: status, Summary: summary, StartedAt: finished.Add(-30 * time.Second), FinishedAt: finished,
	}); err != nil {
		t.Fatal(err)
	}
}

func byCheck(fs []Finding, check string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.CheckName == check {
			out = append(out, f)
		}
	}
	return out
}

// Thursday 2026-09-24 13:30 ET: every job's most recent slot is well past.
var thuAfternoon = et(2026, 9, 24, 13, 30)

// healthyThrough records a successful run for each job's latest slot.
func healthyThrough(t *testing.T, db *sql.DB, now time.Time, skip ...string) {
	t.Helper()
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	for _, spec := range job_runs.Schedule {
		if spec.Name == "check_health" || skipped[spec.Name] {
			continue
		}
		slot, _ := job_runs.LastExpected(spec, now)
		ran(t, db, spec.Name, job_runs.JobRunOK, "OK "+spec.Name, slot.Add(time.Minute))
	}
}

func TestDiagnosePipelineHealthy(t *testing.T) {
	db := pipelineDB(t)
	healthyThrough(t, db, thuAfternoon)
	fs, err := DiagnosePipeline(db, thuAfternoon)
	if err != nil || len(fs) != 0 {
		t.Fatalf("want no findings, got %v (err %v)", fs, err)
	}
}

func TestDiagnosePipelineEmptyDatabaseNamesTheFirstJob(t *testing.T) {
	db := pipelineDB(t) // job_run empty: the state of a freshly recreated database
	fs, err := DiagnosePipeline(db, thuAfternoon)
	if err != nil {
		t.Fatal(err)
	}
	root := byCheck(fs, "pipeline_root_cause")
	if len(root) != 1 || root[0].Subject != "account_state" {
		t.Fatalf("root cause should be account_state, the first job: %+v", root)
	}
	if !strings.Contains(root[0].Detail, "scan_market") || !strings.Contains(root[0].Detail, "submit_orders") {
		t.Errorf("root cause should name downstream jobs: %s", root[0].Detail)
	}
	never := byCheck(fs, "job_never_ran")
	if len(never) != 5 {
		t.Errorf("want 5 jobs never ran (check_health excluded), got %d: %+v", len(never), never)
	}
	for _, f := range fs {
		if f.Severity != "CRITICAL" || f.Category != "job_pipeline" || f.AccountNumber != "all" {
			t.Errorf("unexpected finding shape: %+v", f)
		}
	}
}

func TestDiagnosePipelineFailedJobIsTheRootCauseWithItsError(t *testing.T) {
	db := pipelineDB(t)
	healthyThrough(t, db, thuAfternoon, "scan_market")
	slot, _ := job_runs.LastExpected(scheduleOf(t, "scan_market"), thuAfternoon)
	ran(t, db, "scan_market", job_runs.JobRunFailed, "HTTP 500: scan_market: prepare market db: no such file", slot.Add(time.Minute))

	fs, err := DiagnosePipeline(db, thuAfternoon)
	if err != nil {
		t.Fatal(err)
	}
	root := byCheck(fs, "pipeline_root_cause")
	if len(root) != 1 || root[0].Subject != "scan_market" {
		t.Fatalf("root cause should be scan_market: %+v", fs)
	}
	failed := byCheck(fs, "job_failed")
	if len(failed) != 1 || !strings.Contains(failed[0].Detail, "prepare market db: no such file") {
		t.Errorf("the job's own error must be in the finding: %+v", failed)
	}
	if !strings.Contains(root[0].Detail, "stage_orders") {
		t.Errorf("downstream jobs missing: %s", root[0].Detail)
	}
}

func TestDiagnosePipelineMissedSlotShowsWhatRanLast(t *testing.T) {
	db := pipelineDB(t)
	healthyThrough(t, db, thuAfternoon, "manage_exits")
	// manage_exits last ran two days ago, then the 09:35 slot today was missed.
	ran(t, db, "manage_exits", job_runs.JobRunOK, "OK manage_exits accounts=3", et(2026, 9, 22, 15, 51))

	fs, _ := DiagnosePipeline(db, thuAfternoon)
	missed := byCheck(fs, "job_missed_slot")
	if len(missed) != 1 || missed[0].Subject != "manage_exits" {
		t.Fatalf("want one missed slot for manage_exits: %+v", fs)
	}
	if !strings.Contains(missed[0].Detail, "09:35") || !strings.Contains(missed[0].Detail, "OK manage_exits accounts=3") {
		t.Errorf("finding should give the missed slot and the last run: %s", missed[0].Detail)
	}
}

func TestDiagnosePipelineGivesAJobTimeToRun(t *testing.T) {
	db := pipelineDB(t)
	now := et(2026, 9, 24, 9, 20) // account_state's 09:15 slot is only 5 minutes old
	healthyThrough(t, db, now, "account_state")
	// It ran for its previous slot (Wed 16:15); the 09:15 run has not finished yet.
	prev, _ := job_runs.LastExpected(job_runs.Schedule[0], now.Add(-pipelineGrace))
	ran(t, db, "account_state", job_runs.JobRunOK, "OK account_state", prev.Add(time.Minute))
	fs, _ := DiagnosePipeline(db, now)
	for _, f := range fs {
		if f.Subject == "account_state" {
			t.Fatalf("a job within its grace period must not be flagged: %+v", f)
		}
	}
}

func TestPipelineRowsRecordsFindings(t *testing.T) {
	db := pipelineDB(t)
	rows, findings, err := PipelineRows(db, thuAfternoon)
	if err != nil || len(findings) == 0 || len(rows) != len(findings) {
		t.Fatalf("rows=%d findings=%d err=%v", len(rows), len(findings), err)
	}
	saved, err := GetRecentFindings(db, "all", 50)
	if err != nil || len(saved) != len(findings) {
		t.Fatalf("findings must be persisted for the dashboard: saved=%d want=%d err=%v", len(saved), len(findings), err)
	}
}

func TestFormatPipeline(t *testing.T) {
	if !strings.Contains(FormatPipeline(nil), "every scheduled job recorded a run") {
		t.Error("healthy text")
	}
	got := FormatPipeline([]Finding{{CheckName: "job_failed", Subject: "scan_market", Detail: "boom"}})
	if !strings.Contains(got, "PROBLEMS FOUND") || !strings.Contains(got, "scan_market: boom") {
		t.Errorf("broken text: %s", got)
	}
}

func TestPipelineRowsNeverReportsAllClearWhenTheDiagnosisFails(t *testing.T) {
	db := pipelineDB(t)
	db.Close() // a real, closed database: every query fails
	rows, findings, err := PipelineRows(db, thuAfternoon)
	if err == nil {
		t.Fatal("expected an error from a closed database")
	}
	if len(findings) != 1 || findings[0].CheckName != "pipeline_diagnosis_error" || len(rows) != 1 {
		t.Fatalf("a failed diagnosis must surface as a finding, got %+v", findings)
	}
	if strings.Contains(FormatPipeline(findings), "every scheduled job recorded") {
		t.Error("must not print the all-clear line")
	}
}

// A deliberate stop is reported as one WARNING naming the switch, not as every job
// having missed its slot.
func TestDiagnosePipelineReportsAnEngagedKillswitchInsteadOfBrokenJobs(t *testing.T) {
	db := pipelineDB(t) // empty job_run: without the switch every job would be broken
	if _, err := killswitch.Set(db, true, "darian", "TSLL misbehaving", thuAfternoon); err != nil {
		t.Fatal(err)
	}
	fs, err := DiagnosePipeline(db, thuAfternoon)
	if err != nil || len(fs) != 1 {
		t.Fatalf("want exactly one finding, got %+v (err %v)", fs, err)
	}
	f := fs[0]
	if f.CheckName != "killswitch_engaged" || f.Severity != "WARNING" || !strings.Contains(f.Detail, "TSLL misbehaving") || !strings.Contains(f.Detail, "darian") {
		t.Errorf("finding = %+v", f)
	}
	if _, err := killswitch.Set(db, false, "darian", "fixed", thuAfternoon); err != nil {
		t.Fatal(err)
	}
	fs, _ = DiagnosePipeline(db, thuAfternoon)
	if len(byCheck(fs, "job_never_ran")) == 0 {
		t.Error("with the switch off the empty history is diagnosed as broken jobs again")
	}
}

// scheduleOf is the named job's entry in job_runs.Schedule, so a test does not depend on the
// order the jobs are listed in.
func scheduleOf(t *testing.T, name string) job_runs.JobSpec {
	t.Helper()
	for _, s := range job_runs.Schedule {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("%s is not in job_runs.Schedule", name)
	return job_runs.JobSpec{}
}
