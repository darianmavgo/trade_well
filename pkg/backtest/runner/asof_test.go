package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestLastCompletedEquitySessionMondayMorning(t *testing.T) {
	mon := time.Date(2026, 9, 21, 10, 0, 0, 0, time.FixedZone("ET", -4*3600))
	got := LastCompletedEquitySession(mon).Format("2006-01-02")
	if got != "2026-09-18" {
		t.Fatalf("got %s want 2026-09-18", got)
	}
}

func TestResolveLiveAsOfStaleNeverAllowed(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.FixedZone("ET", -4*3600))
	_, _, err := ResolveLiveAsOf("2026-09-16", now)
	if err == nil {
		t.Fatal("expected stale error")
	}
}

func TestResolveStrategies(t *testing.T) {
	_, err := ResolveStrategies("", "")
	if err == nil {
		t.Fatal("expected error on empty")
	}
}

func TestSessionsSkipExchangeHolidays(t *testing.T) {
	et := time.FixedZone("ET", -5*3600)
	// Wed 2025-11-26 evening: Thursday is Thanksgiving, so the next session is Friday.
	evening := time.Date(2025, 11, 26, 18, 0, 0, 0, et)
	if got := LastCompletedEquitySession(evening).Format("2006-01-02"); got != "2025-11-26" {
		t.Errorf("last completed %s, want 2025-11-26", got)
	}
	if got := NextEquitySession(LastCompletedEquitySession(evening)).Format("2006-01-02"); got != "2025-11-28" {
		t.Errorf("next session %s, want 2025-11-28 (Thanksgiving is closed)", got)
	}
	// Friday 2026-04-03 is Good Friday: on Saturday the last completed session is Thursday.
	sat := time.Date(2026, 4, 4, 12, 0, 0, 0, et)
	if got := LastCompletedEquitySession(sat).Format("2006-01-02"); got != "2026-04-02" {
		t.Errorf("last completed on Good Friday weekend %s, want 2026-04-02", got)
	}
	if got := NextEquitySession(LastCompletedEquitySession(sat)).Format("2006-01-02"); got != "2026-04-06" {
		t.Errorf("next session %s, want Monday 2026-04-06", got)
	}
}

func TestDropUnfinishedSessionsDropsTodayBeforeTheClose(t *testing.T) {
	// Tue 2026-10-13 10:09 ET: the open session's row (built from the hours so far) is not a finished day.
	now := time.Date(2026, 10, 13, 10, 9, 0, 0, time.FixedZone("ET", -4*3600))
	bars := map[string][]models.Bar{"SHPU": {{Symbol: "SHPU", Date: "2026-10-12"}, {Symbol: "SHPU", Date: "2026-10-13"}}}
	kept, dates := DropUnfinishedSessions(bars, now)
	if len(kept["SHPU"]) != 1 || kept["SHPU"][0].Date != "2026-10-12" || len(dates) != 1 || dates[0] != "2026-10-12" {
		t.Fatalf("kept %v, dates %v; want only 2026-10-12", kept, dates)
	}
	// After the close the same day is finished and stays.
	after := time.Date(2026, 10, 13, 16, 30, 0, 0, time.FixedZone("ET", -4*3600))
	if kept, dates := DropUnfinishedSessions(bars, after); len(kept["SHPU"]) != 2 || dates[1] != "2026-10-13" {
		t.Fatalf("after the close kept %v, dates %v; want both days", kept, dates)
	}
}

// A scratch database kept from an earlier scan, or filled by another strategy, must not reach this
// scan: the pipelines create their tables IF NOT EXISTS and would keep its rows.
func TestFreshCalcDBStartsEmptyAndPerStrategy(t *testing.T) {
	out := t.TempDir()
	a, err := freshCalcDB(out, "markov_model_mull")
	if err != nil {
		t.Fatal(err)
	}
	b, err := freshCalcDB(out, "streak-shpu-down3")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two strategies share the scratch database %s", a)
	}
	if err := os.WriteFile(a, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a+"-wal", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := freshCalcDB(out, "markov_model_mull")
	if err != nil {
		t.Fatal(err)
	}
	if again != a {
		t.Fatalf("path changed between scans: %s then %s", a, again)
	}
	for _, p := range []string{a, a + "-wal"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived into the next scan", p)
		}
	}
}

func TestReadSignalTraceWithoutTableIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calc.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE other (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if got := ReadSignalTrace(path); len(got) != 0 {
		t.Fatalf("got %v from a database with no signal_trace", got)
	}
	if got := ReadSignalTrace(filepath.Join(t.TempDir(), "missing.db")); len(got) != 0 {
		t.Fatalf("got %v from a missing database", got)
	}
}
