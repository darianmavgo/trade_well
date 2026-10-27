package appserve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

// runlogEnv points the app at a real SQLite file in a temp dir and returns
// its path. Handlers under test are real functions; the request goes through
// recordRun exactly as a cron hit does.
func runlogEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orchestrator.db")
	t.Setenv("APP_FOLDER", dir)
	t.Setenv("ORCHESTRATOR_DB", dbPath)
	return dbPath
}

func lastRunOf(t *testing.T, dbPath, job string) *job_runs.JobRun {
	t.Helper()
	db, err := bootstrap.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run, err := job_runs.LastJobRun(db, job)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func hit(h http.HandlerFunc) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/cron/x", nil))
	return w
}

func TestRecordRunRecordsSuccessWithTheHandlersOneLineOutcome(t *testing.T) {
	dbPath := runlogEnv(t)
	hit(recordRun("manage_exits", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "OK manage_exits accounts=3\n")
		fmt.Fprintf(w, "second line is not part of the summary\n")
	}))
	run := lastRunOf(t, dbPath, "manage_exits")
	if run == nil || run.Status != job_runs.JobRunOK || run.Summary != "OK manage_exits accounts=3" {
		t.Fatalf("want OK with the first line, got %+v", run)
	}
}

func TestRecordRunRecordsFailureWithTheRealError(t *testing.T) {
	dbPath := runlogEnv(t)
	w := hit(recordRun("account_state", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "account_state: schwab re-authentication required: no token available", http.StatusInternalServerError)
	}))
	if w.Code != 500 {
		t.Fatalf("the handler's status must pass through, got %d", w.Code)
	}
	run := lastRunOf(t, dbPath, "account_state")
	if run == nil || run.Status != job_runs.JobRunFailed ||
		!strings.Contains(run.Summary, "HTTP 500") || !strings.Contains(run.Summary, "re-authentication required") {
		t.Fatalf("want FAILED carrying the error, got %+v", run)
	}
}

func TestRecordRunDoesNotDoubleUpAJobThatRecordsItself(t *testing.T) {
	dbPath := runlogEnv(t)
	hit(recordRun("scan_market", func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := appconfig.Load()
		db, err := bootstrap.OpenDB(cfg.DBPath)
		if err != nil {
			t.Error(err)
			return
		}
		defer db.Close()
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: "scan_market", Status: job_runs.JobRunEmpty, Summary: "own row", RowsWritten: 0}); err != nil {
			t.Error(err)
		}
		fmt.Fprintf(w, "OK scan_market\n")
	}))
	db, _ := bootstrap.OpenDB(dbPath)
	defer db.Close()
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM job_run WHERE job = 'scan_market'`).Scan(&n)
	if n != 1 {
		t.Fatalf("want exactly the job's own row, got %d rows", n)
	}
	if run := lastRunOf(t, dbPath, "scan_market"); run.Summary != "own row" {
		t.Fatalf("the job's own richer row must win, got %+v", run)
	}
}

func TestRecordRunRecordsAPanicThenRepanics(t *testing.T) {
	dbPath := runlogEnv(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic must propagate so cronOnly reports it")
			}
		}()
		hit(recordRun("check_health", func(http.ResponseWriter, *http.Request) { panic("boom") }))
	}()
	run := lastRunOf(t, dbPath, "check_health")
	if run == nil || run.Status != job_runs.JobRunFailed || !strings.Contains(run.Summary, "panic: boom") {
		t.Fatalf("want FAILED panic row, got %+v", run)
	}
}
