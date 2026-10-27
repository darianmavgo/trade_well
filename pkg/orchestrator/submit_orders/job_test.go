package submit_orders

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

func TestRunJobDryRunEmptyIsEmptyAndWritesNothing(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	accts := []*trader.ResolvedAccount{{AccountNumber: "111"}}

	res, err := RunJob(context.Background(), db, nil, accts, JobOptions{DryRun: true})
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if res.Status != job_runs.JobRunEmpty || res.StagedOrdersFound != 0 {
		t.Fatalf("status=%q found=%d, want EMPTY/0", res.Status, res.StagedOrdersFound)
	}
	if run, _ := job_runs.LastJobRun(db, "submit_orders"); run != nil {
		t.Fatalf("dry run must not record a job_run, got %+v", run)
	}
}

func TestRunJobLiveWithoutClientRefuses(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	_, err := RunJob(context.Background(), db, nil, []*trader.ResolvedAccount{{AccountNumber: "111"}}, JobOptions{})
	if !errors.Is(err, errNoLiveClient) {
		t.Fatalf("err = %v, want errNoLiveClient", err)
	}
}

func TestRunJobAccountFailureIsReportedNotSwallowed(t *testing.T) {
	// A nil db makes Run fail for every account; the job must keep going,
	// report each failure as an ERROR row, and return FAILED plus an error.
	accts := []*trader.ResolvedAccount{{AccountNumber: "111"}, {AccountNumber: "222"}}
	res, err := RunJob(context.Background(), nil, nil, accts, JobOptions{DryRun: true})
	if err == nil {
		t.Fatal("expected an error when accounts fail")
	}
	if res.Status != job_runs.JobRunFailed || len(res.Errors) != 2 {
		t.Fatalf("status=%q errors=%v, want FAILED with 2 errors", res.Status, res.Errors)
	}
	for _, acct := range []string{"111", "222"} {
		found := false
		for _, r := range res.Rows {
			if r.Account == acct && r.Status == "ERROR" {
				found = true
			}
		}
		if !found {
			t.Errorf("no ERROR row for account %s: %+v", acct, res.Rows)
		}
	}
}

func TestOpenTradeFailureSurfacesAsErrorRow(t *testing.T) {
	res := &Result{}
	acc := &trader.ResolvedAccount{AccountNumber: "111"}
	so := stage_orders.StagedOrder{ID: 7, Symbol: "TECL", StrategyName: "s", Instruction: "BUY"}
	openTrade(nil, acc, so, "999", time.Time{}, time.Now(), res) // nil db => manage_exits.Open fails
	if len(res.Rows) != 1 || res.Rows[0].Status != "ERROR" || !strings.Contains(res.Rows[0].Detail, "NOT created") {
		t.Fatalf("expected one ERROR row about lost exit tracking, got %+v", res.Rows)
	}
	if len(res.Details) != 1 {
		t.Fatalf("expected a detail line for the log, got %v", res.Details)
	}
}

func TestReportSummarySubjects(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		res  JobResult
		want string
	}{
		{"empty", JobResult{Accounts: 1}, "no staged orders"},
		{"mixed", JobResult{Accounts: 1, StagedOrdersFound: 3, Submitted: 2, Filled: 1, PendingFill: 1, Rejected: 1}, "3 found, 2 submitted, 1 filled, 1 pending fill, 1 rejected"},
		{"failed", JobResult{Accounts: 1, StagedOrdersFound: 2, Submitted: 1, Filled: 1, Errors: []string{"boom"}}, "FAILED (1 error(s)), 2 found, 1 submitted, 1 filled"},
	}
	for _, c := range cases {
		got := ReportSummary(&c.res, now)
		if got.SubjectSuffix != c.want {
			t.Errorf("%s: suffix %q, want %q", c.name, got.SubjectSuffix, c.want)
		}
	}
	if s := ReportSummary(&JobResult{Errors: []string{"boom"}}, now); !strings.Contains(strings.Join(s.Lines, "\n"), "ACTION REQUIRED: boom") {
		t.Errorf("errors must appear as ACTION REQUIRED lines, got %v", s.Lines)
	}
}

// The exit date exists from the moment an order is accepted and is corrected when the
// fill is confirmed, on the same manage_exit row.
func TestAcceptedEntryGetsAProvisionalExitDateThenTheFillCorrectsIt(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	so := stage_orders.StagedOrder{ID: 7, Symbol: "TSLL", StrategyName: "tsll-daily-one-share", Instruction: "BUY",
		Quantity: 1, EntryPrice: 10.30, TakeProfitPrice: 10.82, StopLossPrice: 8.24, MaxTradingDays: 1}
	loc, _ := time.LoadLocation("America/New_York")
	submitted := time.Date(2026, 9, 24, 17, 10, 0, 0, loc) // Thursday evening

	res := &Result{}
	registerPending(db, acc, so, "1001", submitted, res)
	if len(res.Rows) != 0 {
		t.Fatalf("registering must not fail: %+v", res.Rows)
	}
	var status, exitDate, source string
	if err := db.QueryRow(`SELECT status, exit_date, exit_date_source FROM manage_exit`).Scan(&status, &exitDate, &source); err != nil {
		t.Fatal(err)
	}
	if status != manage_exits.TradeStatusPendingFill || exitDate != "2026-09-28" || source != manage_exits.ExitDateProvisional {
		t.Fatalf("after acceptance: %s %s %s", status, exitDate, source)
	}

	filled := time.Date(2026, 9, 28, 9, 31, 0, 0, loc) // it filled Monday
	openTrade(db, acc, so, "1001", filled, filled.Add(15*time.Minute), res)
	if len(res.Rows) != 0 {
		t.Fatalf("opening must not fail: %+v", res.Rows)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*), MAX(status), MAX(exit_date), MAX(exit_date_source) FROM manage_exit`).Scan(&n, &status, &exitDate, &source); err != nil {
		t.Fatal(err)
	}
	if n != 1 || status != manage_exits.TradeStatusOpen || exitDate != "2026-09-29" || source != manage_exits.ExitDateFill {
		t.Errorf("after the fill: rows=%d %s %s %s", n, status, exitDate, source)
	}

	// An entry that dies unfilled is cancelled, never opened.
	registerPending(db, acc, stage_orders.StagedOrder{ID: 8, Symbol: "MARA", StrategyName: "mara_tree", Quantity: 5, EntryPrice: 13, TakeProfitPrice: 14, MaxTradingDays: 1}, "2002", submitted, res)
	cancelPending(db, acc, "2002", "CANCELED")
	if err := db.QueryRow(`SELECT status FROM manage_exit WHERE entry_order_id = '2002'`).Scan(&status); err != nil || status != manage_exits.TradeStatusCancelled {
		t.Errorf("canceled entry status = %q (err %v)", status, err)
	}
}
