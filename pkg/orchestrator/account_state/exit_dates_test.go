package account_state

import (
	"database/sql"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	_ "modernc.org/sqlite"
)

func exitDatesDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	store.ParseAccounts(db, []byte(`[
	 {"securitiesAccount":{"accountNumber":"74234603","positions":[
	   {"instrument":{"symbol":"TSLL","assetType":"COLLECTIVE_INVESTMENT"},"longQuantity":1},
	   {"instrument":{"symbol":"VOO","assetType":"EQUITY"},"longQuantity":3}]}},
	 {"securitiesAccount":{"accountNumber":"25474194","positions":[
	   {"instrument":{"symbol":"TSLL","assetType":"COLLECTIVE_INVESTMENT"},"longQuantity":1}]}}]`), time.Now().UTC())
	return db
}

func plannedOf(t *testing.T, db *sql.DB, acc, sym string) sql.NullString {
	t.Helper()
	var d sql.NullString
	if err := db.QueryRow(`SELECT exit_date FROM account_state_position WHERE account_number = ? AND symbol = ?`, acc, sym).Scan(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

// A managed position shows the exit date the pipeline calculated (entry fill date plus the
// strategy's hold window); an unmanaged one shows none.
func TestPositionsShowThePlannedExitDateOfTheirManagedTrade(t *testing.T) {
	db := exitDatesDB(t)
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/New_York")
	fill := time.Date(2026, 9, 24, 9, 31, 0, 0, loc)
	if _, err := manage_exits.Open(db, manage_exits.OpenTradeParams{
		AccountNumber: "74234603", Symbol: "TSLL", StrategyName: "tsll-daily-one-share", EntryOrderID: "1001",
		EntryPrice: 10.30, TargetProfitPrice: 10.82, Quantity: 1, EntryTime: fill, FilledAt: fill, MaxTradingDays: 1,
	}); err != nil {
		t.Fatal(err)
	}

	if err := applyPlannedExitDates(db, []string{"74234603", "25474194"}); err != nil {
		t.Fatal(err)
	}
	if d := plannedOf(t, db, "74234603", "TSLL"); d.String != "2026-09-25" {
		t.Errorf("managed TSLL exit_date = %+v, want 2026-09-25", d)
	}
	if d := plannedOf(t, db, "74234603", "VOO"); d.Valid {
		t.Errorf("an unmanaged position has no planned exit, got %+v", d)
	}
	if d := plannedOf(t, db, "25474194", "TSLL"); d.Valid {
		t.Errorf("the same symbol in an account with no managed trade has none, got %+v", d)
	}

	// A resync after the trade closed clears the date.
	if _, err := db.Exec(`UPDATE manage_exit SET status = 'CLOSED_MANUAL'`); err != nil {
		t.Fatal(err)
	}
	if err := applyPlannedExitDates(db, []string{"74234603", "25474194"}); err != nil {
		t.Fatal(err)
	}
	if d := plannedOf(t, db, "74234603", "TSLL"); d.Valid {
		t.Errorf("a closed trade's date must disappear, got %+v", d)
	}
}

// Before the orchestrator has ever tracked a trade there is no manage_exit table; that is
// just "no dates", not a failed sync.
func TestNoManagedTradesTableMeansNoDatesNotAnError(t *testing.T) {
	db := exitDatesDB(t)
	if _, err := db.Exec(`DROP TABLE IF EXISTS manage_exit`); err != nil {
		t.Fatal(err)
	}
	if err := applyPlannedExitDates(db, []string{"74234603"}); err != nil {
		t.Fatalf("a missing manage_exit table must not fail the sync: %v", err)
	}
	if d := plannedOf(t, db, "74234603", "TSLL"); d.Valid {
		t.Errorf("exit_date = %+v", d)
	}
}
