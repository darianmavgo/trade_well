package check_health

import (
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
)

func exitParams(entry, filled time.Time, maxDays int) manage_exits.OpenTradeParams {
	return manage_exits.OpenTradeParams{
		AccountNumber: "74234603", Symbol: "TSLL", StrategyName: "tsll-daily-one-share", EntryOrderID: "1001",
		EntryPrice: 10.30, TargetProfitPrice: 10.82, Quantity: 1, EntryTime: entry, FilledAt: filled, MaxTradingDays: maxDays,
	}
}

func checkNames(fs []Finding) map[string]Finding {
	m := map[string]Finding{}
	for _, f := range fs {
		m[f.CheckName] = f
	}
	return m
}

// A position past its exit date is CRITICAL; one whose exit date is still ahead is not.
func TestStaleTradeIsJudgedByItsExitDate(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	if _, err := manage_exits.Open(db, exitParams(time.Now().UTC().AddDate(0, 0, -10), time.Now().UTC().AddDate(0, 0, -10), 1)); err != nil {
		t.Fatal(err)
	}
	fs, err := RunSQLHealthChecks(db, "74234603", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	stale, ok := checkNames(fs)["stale_time_expired_trade"]
	if !ok || stale.Severity != "CRITICAL" || stale.Subject != "TSLL" {
		t.Fatalf("a position 10 days past a 1-day exit date must be CRITICAL, got %+v", fs)
	}
	if _, has := checkNames(fs)["exit_date_not_from_fill"]; has {
		t.Error("a fill-sourced exit date must not be reported as an estimate")
	}

	db2 := setupTestDB(t)
	defer db2.Close()
	if _, err := manage_exits.Open(db2, exitParams(time.Now().UTC(), time.Now().UTC(), 8)); err != nil {
		t.Fatal(err)
	}
	fs2, _ := RunSQLHealthChecks(db2, "74234603", time.Now().UTC())
	if _, has := checkNames(fs2)["stale_time_expired_trade"]; has {
		t.Errorf("a position with 8 days to go is not stale: %+v", fs2)
	}
}

// An open position whose exit date never got corrected from the fill is flagged.
func TestExitDateStillAnEstimateIsFlagged(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	entry := time.Now().UTC().AddDate(0, 0, -2)
	if _, err := manage_exits.Open(db, exitParams(entry, time.Time{}, 8)); err != nil { // no broker fill time: provisional
		t.Fatal(err)
	}
	fs, _ := RunSQLHealthChecks(db, "74234603", time.Now().UTC())
	f, ok := checkNames(fs)["exit_date_not_from_fill"]
	if !ok || f.Severity != "WARNING" || f.Subject != "TSLL" {
		t.Fatalf("want a WARNING for the estimated exit date, got %+v", fs)
	}
}

// An accepted entry that never got confirmed is flagged after 3 days, and not before.
func TestStuckPendingFillIsFlagged(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	if _, err := manage_exits.RegisterPending(db, exitParams(time.Now().UTC().AddDate(0, 0, -4), time.Time{}, 1)); err != nil {
		t.Fatal(err)
	}
	fs, _ := RunSQLHealthChecks(db, "74234603", time.Now().UTC())
	if f, ok := checkNames(fs)["stuck_pending_fill"]; !ok || f.Severity != "WARNING" {
		t.Fatalf("want stuck_pending_fill, got %+v", fs)
	}
	// A pending entry is not a held position, so it must not trip the OPEN-only checks.
	for _, f := range fs {
		if f.CheckName == "orphan_active_trade" && f.Subject == "TSLL" {
			t.Errorf("a PENDING_FILL entry is not an orphan open trade: %+v", f)
		}
	}

	db2 := setupTestDB(t)
	defer db2.Close()
	_, _ = manage_exits.RegisterPending(db2, exitParams(time.Now().UTC(), time.Time{}, 1))
	fs2, _ := RunSQLHealthChecks(db2, "74234603", time.Now().UTC())
	if _, has := checkNames(fs2)["stuck_pending_fill"]; has {
		t.Error("a fresh pending entry is not stuck")
	}
}
