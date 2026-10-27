package clear_stale

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/schemas"
)

var now = time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)

// "live" is greenlit directly, "stk_a" and "stk_b" are greenlit as members of the stack
// stk_a+stk_b; "old" is on no allowlist.
var allow = []string{"live", "stk_a+stk_b"}

// seed builds a real database with orders and trades of greenlit and retired strategies.
func seed(t *testing.T) *sql.DB {
	t.Helper()
	db, err := appconfig.OpenDB(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := schemas.EnsureAll(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	order := func(sym, strat, status, reason string, staged time.Time) {
		exec(`INSERT INTO stage_order (account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price, status, reject_reason, staged_at)
			VALUES ('111', ?, ?, 1, 10, 11, 9, ?, ?, ?)`, strat, sym, status, reason, staged)
	}
	fresh := now.Add(-2 * time.Hour)
	order("OLDREJ", "old", "REJECTED", "status 429", fresh) // retired: cleared however recent
	order("OLDPEND", "old", "PENDING", "", fresh)
	order("OLDSUB", "old", "SUBMITTED", "", fresh)            // a live order is never touched
	order("LIVEREJ", "live", "REJECTED", "status 429", fresh) // greenlit: still worth seeing
	order("LIVEPEND", "live", "PENDING", "", fresh)

	trade := func(sym, strat string, filled time.Time) {
		exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_price, target_profit_price, quantity, entry_time, status, entry_filled_at)
			VALUES ('111', ?, ?, ?, 10, 11, 1, ?, 'OPEN', ?)`, sym, strat, strat, filled, filled)
	}
	trade("GONE", "old", now.AddDate(0, 0, -5))      // retired, not held, mirror has seen the fill: closed
	trade("TODAY", "old", now.Add(-4*time.Hour))     // retired but filled after the mirror was fetched: kept
	trade("HELD", "old", now.AddDate(0, 0, -5))      // retired but the broker still holds it: kept
	trade("LIVEGONE", "live", now.AddDate(0, 0, -5)) // greenlit orphan: kept
	trade("STKGONE", "stk_b", now.AddDate(0, 0, -5)) // member of a greenlit stack: kept
	exec(`INSERT INTO account_state_account (account_number, fetched_at) VALUES ('111', ?)`, now.Add(-6*time.Hour))
	exec(`INSERT INTO account_state_position (account_number, symbol, asset_type, long_quantity, fetched_at) VALUES ('111', 'HELD', 'EQUITY', 1, ?)`, now.Add(-6*time.Hour))
	return db
}

func status(t *testing.T, db *sql.DB, q, sym string) string {
	t.Helper()
	var s string
	if err := db.QueryRow(q, sym).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

const orderStatus = `SELECT status FROM stage_order WHERE symbol = ?`
const tradeStatus = `SELECT status FROM manage_exit WHERE symbol = ?`

func TestRunRefusesAnEmptyAllowlist(t *testing.T) {
	db := seed(t)
	if _, err := Run(context.Background(), &bytes.Buffer{}, db, Options{Now: now}); err == nil {
		t.Fatal("an empty allowlist would make every strategy look retired")
	}
	if got := status(t, db, orderStatus, "LIVEREJ"); got != "REJECTED" {
		t.Errorf("refused run changed LIVEREJ to %s", got)
	}
}

func TestRunDryRunReportsAndWritesNothing(t *testing.T) {
	db := seed(t)
	var out bytes.Buffer
	res, err := Run(context.Background(), &out, db, Options{DryRun: true, Allowlist: allow, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rejected != 1 || res.Pending != 1 || res.Orphans != 1 || res.Kept != 1 {
		t.Fatalf("dry run counts = %+v\n%s", res, out.String())
	}
	if got := status(t, db, orderStatus, "OLDREJ"); got != "REJECTED" {
		t.Errorf("dry run changed OLDREJ to %s", got)
	}
	if got := status(t, db, tradeStatus, "GONE"); got != "OPEN" {
		t.Errorf("dry run changed GONE to %s", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_run WHERE job = ?`, JobName).Scan(&n); err != nil || n != 0 {
		t.Errorf("dry run recorded a job_run: n=%d err=%v", n, err)
	}
}

func TestRunLiveClearsOnlyRetiredStrategies(t *testing.T) {
	db := seed(t)
	var out bytes.Buffer
	res, err := Run(context.Background(), &out, db, Options{Allowlist: allow, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rejected != 1 || res.Pending != 1 || res.Orphans != 1 || res.Kept != 1 {
		t.Fatalf("counts = %+v\n%s", res, out.String())
	}
	want := map[string]string{"OLDREJ": StatusCleared, "OLDPEND": StatusCleared, "OLDSUB": "SUBMITTED", "LIVEREJ": "REJECTED", "LIVEPEND": "PENDING"}
	for sym, w := range want {
		if got := status(t, db, orderStatus, sym); got != w {
			t.Errorf("order %s = %s, want %s", sym, got, w)
		}
	}
	for sym, w := range map[string]string{"GONE": "CLOSED_MANUAL", "TODAY": "OPEN", "HELD": "OPEN", "LIVEGONE": "OPEN", "STKGONE": "OPEN"} {
		if got := status(t, db, tradeStatus, sym); got != w {
			t.Errorf("trade %s = %s, want %s", sym, got, w)
		}
	}
	if got := status(t, db, `SELECT exit_reason FROM manage_exit WHERE symbol = ?`, "GONE"); got != ReasonStaleCleared {
		t.Errorf("GONE exit_reason = %s", got)
	}
	if got := status(t, db, `SELECT reject_reason FROM stage_order WHERE symbol = ?`, "OLDREJ"); got != "status 429 [cleared as stale]" {
		t.Errorf("reject_reason lost or not annotated: %q", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_run WHERE job = ?`, JobName).Scan(&n); err != nil || n != 1 {
		t.Errorf("live run job_runs = %d err=%v, want 1", n, err)
	}

	// Running again finds nothing new.
	again, err := Run(context.Background(), &out, db, Options{Allowlist: allow, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if again.Rejected+again.Pending+again.Orphans != 0 {
		t.Errorf("second run cleared more: %+v", again)
	}
}
