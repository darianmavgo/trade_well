package run_e2e_live

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, ensure := range []func(*sql.DB) error{job_runs.EnsureSchema, scan_market.EnsureSchema, stage_orders.EnsureSchema, manage_exits.EnsureSchema, check_health.EnsureSchema} {
		if err := ensure(db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Only one live run at a time: a second request must not start another.
func TestRunRefusesWhileAnotherRunIsInProgress(t *testing.T) {
	running.Lock()
	defer running.Unlock()
	var out strings.Builder
	_, err := Run(context.Background(), &out, testDB(t), http.NewServeMux(), Options{})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if out.Len() != 0 {
		t.Errorf("a refused run must print nothing, got %q", out.String())
	}
}

func TestCountHelpersOnlyTouchKnownTables(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`INSERT INTO job_run (job, status, summary, rows_written, started_at, finished_at) VALUES ('x','OK','',0,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if got := count(db, "stage_order"); got != 0 {
		t.Errorf("empty stage_order = %d", got)
	}
	// A name outside the allowlist is never put into SQL.
	if got := count(db, "job_run; DROP TABLE job_run"); got != 0 {
		t.Errorf("unknown table = %d", got)
	}
	if _, err := db.Exec(`SELECT 1 FROM job_run`); err != nil {
		t.Errorf("job_run must still exist: %v", err)
	}
	if !validTable("check_health_finding") || validTable("job_run") {
		t.Error("validTable allowlist is wrong")
	}
}

func TestFieldReadsNumbersFromTheLastResponse(t *testing.T) {
	c := &caller{last: "OK stage_orders dry_run=false accounts=3 signals=1 staged=2 dismissed=0"}
	for name, want := range map[string]int{"staged": 2, "signals": 1, "accounts": 3, "dismissed": 0, "missing": 0} {
		if got := c.field(name); got != want {
			t.Errorf("field(%s) = %d, want %d", name, got, want)
		}
	}
	// "buy_signals" must not be confused with "signals".
	c.last = "OK scan_market run=x livescan_signals=4 buy_signals=1"
	if got := c.field("buy_signals"); got != 1 {
		t.Errorf("buy_signals = %d", got)
	}
}

func TestAccountStateStructIsSummarised(t *testing.T) {
	got := acctCounts.ReplaceAllString("&{AccountsFound:3 OrdersFound:27 TransactionsFound:110 Extra:1}", "accounts=$1 orders=$2 transactions=$3")
	if got != "accounts=3 orders=27 transactions=110" {
		t.Errorf("got %q", got)
	}
}
