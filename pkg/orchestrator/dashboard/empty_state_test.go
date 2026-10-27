package dashboard

import (
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

func TestAttachEmptyStateNeverRunVsRanEmpty(t *testing.T) {
	db, err := appconfig.OpenDB("file:empty_state_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dump := TableDump{Name: "scan_market_buy_signal"}
	attachEmptyState(&dump, db, "scan_market")
	if dump.EmptyState != "never_run" {
		t.Fatalf("want never_run, got %q (%s)", dump.EmptyState, dump.EmptyMessage)
	}

	if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{
		Job: "scan_market", Status: job_runs.JobRunEmpty, Summary: "scan_market_buy_signal=0",
		RowsWritten: 0, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	dump = TableDump{Name: "scan_market_buy_signal"}
	attachEmptyState(&dump, db, "scan_market")
	if dump.EmptyState != "ran_empty" {
		t.Fatalf("want ran_empty, got %q (%s)", dump.EmptyState, dump.EmptyMessage)
	}
	if dump.LastRunAt == "" {
		t.Fatal("expected LastRunAt")
	}
}
