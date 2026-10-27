package ready

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
)

const liveStack = "markov_model_mull+streak-shpu-down3+markov_model_amuu+markov_model_vrtl"

func find(rep Report, name string) Check {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func TestStaticAllowlistResolvesFromTheBundle(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	rep := Run(context.Background(), Options{Static: true, Allowlist: []string{liveStack}})
	if rep.Status != OK || len(rep.Checks) != 2 || rep.Checks[0].Name != "allowlist" || rep.Checks[1].Name != "allocation" {
		t.Fatalf("report %+v, want ok allowlist and allocation checks", rep)
	}
}

func TestStaticAllowlistNamesWhatIsMissing(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	rep := Run(context.Background(), Options{Static: true, Allowlist: []string{"markov_model_mull+streak-no-such-row"}})
	c := find(rep, "allowlist")
	if rep.Status != Bad || c.Status != Bad || !strings.Contains(c.Detail, "streak-no-such-row") {
		t.Fatalf("got %+v, want a bad check naming streak-no-such-row", rep)
	}
}

func TestEmptyAllowlistIsBad(t *testing.T) {
	rep := Run(context.Background(), Options{Static: true})
	if rep.Status != Bad {
		t.Fatalf("got %+v, want bad", rep)
	}
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := bootstrap.OpenDB(filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestKillswitchCheck(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 10, 14, 11, 0, 0, 0, job_runs.EasternTime())
	if k := killswitchCheck(db); k.Status != OK {
		t.Fatalf("released killswitch should be ok, got %+v", k)
	}
	if _, err := killswitch.Set(db, true, "test", "ready test", now); err != nil {
		t.Fatal(err)
	}
	if k := killswitchCheck(db); k.Status != Bad {
		t.Fatalf("engaged killswitch should be bad, got %+v", k)
	}
}

func TestCashCheckUsesTheBuyingPowerRuleOfTheJobs(t *testing.T) {
	db := openDB(t)
	// A CASH account as Schwab reports it: availableFunds and buyingPower are 0, the usable
	// amount is cashAvailableForTrading. It must not warn.
	cashAcct := `{"accountNumber":"87654321","type":"CASH","currentBalances":{"cashAvailableForTrading":50000,"cashBalance":50000,"liquidationValue":50000}}`
	if _, err := db.Exec(`INSERT INTO account_state_account (account_number, account_type, available_funds, buying_power, raw_json, fetched_at) VALUES ('87654321', 'CASH', 0, 0, ?, '2026-10-13T10:00:00Z')`, cashAcct); err != nil {
		t.Fatal(err)
	}
	c := cashCheck(db)
	if c.Status != OK || !strings.Contains(c.Detail, "$50000.00") || strings.Contains(c.Detail, "87654321") {
		t.Fatalf("cash account: %+v, want ok with $50000.00 and the number masked", c)
	}
	// An account with nothing to trade with is a warning.
	empty := `{"accountNumber":"12345678","type":"MARGIN","currentBalances":{"buyingPower":0,"cashBalance":0}}`
	if _, err := db.Exec(`INSERT INTO account_state_account (account_number, account_type, available_funds, raw_json, fetched_at) VALUES ('12345678', 'MARGIN', 0, ?, '2026-10-13T10:00:00Z')`, empty); err != nil {
		t.Fatal(err)
	}
	if c := cashCheck(db); c.Status != Warn || !strings.Contains(c.Detail, "…5678 margin: $0.00") {
		t.Fatalf("empty account: %+v, want a warn naming …5678", c)
	}
}

func TestGapGradesSessionsBehind(t *testing.T) {
	completed := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC) // Tuesday
	for date, want := range map[string]string{"2026-10-13": OK, "2026-10-12": OK, "2026-10-09": Warn, "2026-10-08": Bad, "2026-10-13 13:00:00": OK, "garbage": Bad} {
		if _, got := gap(date, completed); got != want {
			t.Errorf("gap(%q) = %s, want %s", date, got, want)
		}
	}
}

func TestRunJobPrintsTheOutcomeFirstAndSendsNothingWhenAsked(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	db := openDB(t)
	var out bytes.Buffer
	rep, err := RunJob(context.Background(), db, &out, JobOptions{Options: Options{Static: true, Allowlist: []string{liveStack}}})
	if err != nil || rep.Status != OK {
		t.Fatalf("rep %+v err %v", rep, err)
	}
	if first, _, _ := strings.Cut(out.String(), "\n"); !strings.HasPrefix(first, "OK ready: OK") {
		t.Fatalf("first line %q", first)
	}
}

func TestAllocationOffNamesStrategiesNotAtTheBundlesShare(t *testing.T) {
	cfgs := map[string]strategy.StrategyConfig{
		"a":     {AllocationPct: 0.20, PositionSizing: "fixed_pct"},
		"b":     {AllocationPct: 0.65},
		"c":     {AllocationPct: 0.25, PositionSizing: "fixed_pct"},
		"smoke": {AllocationPct: 1, PositionSizing: "fixed_shares", FixedShares: 1},
	}
	off, n := allocationOff(0.20, cfgs)
	if n != 3 || len(off) != 2 || off[0] != "b sizes 65.0%" || off[1] != "c sizes 25.0%" {
		t.Fatalf("n=%d off=%v", n, off)
	}
	if off, _ := allocationOff(0.20, map[string]strategy.StrategyConfig{"a": {AllocationPct: 0.2}}); len(off) != 0 {
		t.Fatalf("exact match flagged: %v", off)
	}
}

// Without TURSO_DATABASE_URL (always the case under go test) the database check is Bad on every
// platform: a local SQLite file is no longer a supported operational database.
func TestDatabaseCheckBadWithoutTurso(t *testing.T) {
	c := databaseCheck(context.Background(), nil)
	if c.Status != Bad || !strings.Contains(c.Detail, "TURSO_DATABASE_URL is not set") {
		t.Fatalf("got %s %q, want Bad naming TURSO_DATABASE_URL", c.Status, c.Detail)
	}
}
