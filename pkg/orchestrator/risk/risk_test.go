package risk

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	_ "modernc.org/sqlite"
)

func TestParseLadder(t *testing.T) {
	got, err := ParseLadder("0.20:0, 0.10:0.5")
	if err != nil || len(got) != 2 || got[0].DrawdownPct != 0.10 || got[1].Scale != 0 {
		t.Fatalf("ParseLadder = %v, %v", got, err)
	}
	if got, err := ParseLadder(""); err != nil || len(got) != 0 {
		t.Fatalf("empty ladder = %v, %v", got, err)
	}
	for _, bad := range []string{"0.1", "x:0.5", "1.5:0.5", "0.1:2", "0:0.5"} {
		if _, err := ParseLadder(bad); err == nil {
			t.Errorf("ParseLadder(%q) should fail", bad)
		}
	}
}

func TestAssess(t *testing.T) {
	l := DefaultLimits()
	cases := []struct {
		name                     string
		equity, peak, prev, long float64
		scale                    float64
		halt                     bool
	}{
		{"at the high", 100, 100, 99, 50, 1, false},
		{"9% down", 91, 100, 91, 50, 1, false},
		{"10% down halves", 90, 100, 90, 50, 0.5, false},
		{"15% down still half", 85, 100, 85, 50, 0.5, false},
		{"20% down halts", 80, 100, 80, 50, 0, true},
		{"6% day loss halts at 4% drawdown", 96, 100, 102.1, 50, 0, true},
		{"4% day loss is fine", 98, 100, 102, 50, 1, false},
		{"no history", 100, 0, 0, 0, 1, false},
		{"zero equity", 0, 100, 100, 0, 0, true},
	}
	for _, c := range cases {
		s := Assess(l, c.equity, c.peak, c.prev, c.long)
		if s.Scale != c.scale || s.HaltEntries != c.halt {
			t.Errorf("%s: scale %v halt %v, want %v %v (%v)", c.name, s.Scale, s.HaltEntries, c.scale, c.halt, s.Reasons)
		}
	}
	// A new high never reports a drawdown even when the stored peak is lower.
	if s := Assess(l, 120, 100, 100, 0); s.Peak != 120 || s.Drawdown != 0 {
		t.Errorf("new high: peak %v drawdown %v", s.Peak, s.Drawdown)
	}
}

func TestGrossRoom(t *testing.T) {
	s := Assess(Limits{MaxGrossPct: 1}, 1000, 1000, 1000, 400)
	if got := s.GrossRoom(1000, 400); got != 600 {
		t.Errorf("room = %v, want 600", got)
	}
	if got := s.GrossRoom(1000, 1500); got != 0 {
		t.Errorf("over the cap room = %v, want 0", got)
	}
	if got := (State{}).GrossRoom(1000, 1); !math.IsInf(got, 1) {
		t.Errorf("no cap room = %v, want +Inf", got)
	}
}

// openDB is a real SQLite file with the account mirror tables the job reads.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "risk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE TABLE account_state_account (account_number TEXT PRIMARY KEY, liquidation_value REAL, fetched_at DATETIME NOT NULL)`,
		`CREATE TABLE account_state_position (account_number TEXT, symbol TEXT, long_quantity REAL, market_value REAL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func setEquity(t *testing.T, db *sql.DB, acct string, equity float64, at time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO account_state_account VALUES (?,?,?) ON CONFLICT(account_number) DO UPDATE SET liquidation_value=excluded.liquidation_value, fetched_at=excluded.fetched_at`, acct, equity, at.UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestRunJobLadderAcrossSessions(t *testing.T) {
	db := openDB(t)
	day := func(n int) time.Time { return time.Date(2026, 10, 12+n, 20, 0, 0, 0, time.UTC) } // Mon 12th, 16:00 ET
	exec := func(n int, equity float64) *Result {
		setEquity(t, db, "111", equity, day(n))
		var out bytes.Buffer
		res, err := RunJob(context.Background(), db, &out, JobOptions{Limits: DefaultLimits(), Now: day(n)})
		if err != nil {
			t.Fatalf("day %d: %v", n, err)
		}
		return res
	}

	exec(0, 1000) // first session: full size, nothing announced
	if s, _ := Load(db, "111"); s.Scale != 1 || s.Peak != 1000 {
		t.Fatalf("first state = %+v", s)
	}
	exec(1, 1100)     // new high
	r := exec(2, 980) // 10.9% below 1100 and -10.9% on the day: day loss halts
	if s, _ := Load(db, "111"); !s.HaltEntries || len(r.Changed) != 1 {
		t.Fatalf("after a 10.9%% day: %+v changed=%d", s, len(r.Changed))
	}
	r = exec(3, 989) // up on the day, still 10% below the peak: half size, still a change from halted
	s, _ := Load(db, "111")
	if s.HaltEntries || s.Scale != 0.5 || len(r.Changed) != 1 {
		t.Fatalf("recovering: %+v changed=%d", s, len(r.Changed))
	}
	r = exec(4, 985) // nothing changes: no announcement
	if len(r.Changed) != 0 {
		t.Fatalf("unchanged scale reported as changed")
	}
}

func TestRunJobDryRunWritesNothing(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	setEquity(t, db, "111", 1000, now)
	var out bytes.Buffer
	if _, err := RunJob(context.Background(), db, &out, JobOptions{Limits: DefaultLimits(), DryRun: true, Now: now}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM risk_equity`).Scan(&n)
	if n != 0 || !strings.Contains(out.String(), "dry_run") {
		t.Fatalf("dry run wrote %d equity rows; output %q", n, out.String())
	}
}

func TestRunJobSkipsStaleAndEmptyAccounts(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	setEquity(t, db, "old", 1000, now.Add(-10*24*time.Hour))
	setEquity(t, db, "empty", 0, now)
	var out bytes.Buffer
	res, err := RunJob(context.Background(), db, &out, JobOptions{Limits: DefaultLimits(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.States) != 0 || !strings.Contains(out.String(), "too old") || !strings.Contains(out.String(), "no equity") {
		t.Fatalf("states %d output %q", len(res.States), out.String())
	}
}

func TestResetPeakRestartsHighWaterMark(t *testing.T) {
	db := openDB(t)
	day := func(n int) time.Time { return time.Date(2026, 10, 12+n, 20, 0, 0, 0, time.UTC) }
	step := func(n int, equity float64, reset bool) {
		setEquity(t, db, "111", equity, day(n))
		if _, err := RunJob(context.Background(), db, &bytes.Buffer{}, JobOptions{Limits: DefaultLimits(), Now: day(n), ResetPeak: reset}); err != nil {
			t.Fatal(err)
		}
	}
	step(0, 1000, false)
	step(1, 700, false) // a withdrawal looks like a 30% loss
	if s, _ := Load(db, "111"); !s.HaltEntries {
		t.Fatalf("withdrawal should look like a halt: %+v", s)
	}
	step(2, 700, true)
	if s, _ := Load(db, "111"); s.HaltEntries || s.Peak != 700 {
		t.Fatalf("after reset: %+v", s)
	}
}

func TestLoadUnassessedAccountIsNormal(t *testing.T) {
	db := openDB(t)
	s, err := Load(db, "nobody")
	if err != nil || s.Scale != 1 || s.HaltEntries {
		t.Fatalf("Load = %+v, %v", s, err)
	}
}

func TestCheckEntry(t *testing.T) {
	s := Assess(DefaultLimits(), 1000, 1000, 1000, 0)
	cases := []struct {
		name          string
		cost, equity  float64
		today, open   int
		wantRejection bool
	}{
		{"normal order", 200, 1000, 3, 10, false},
		{"exactly at the order cap", 600, 1000, 0, 0, false},
		{"over the order cap", 601, 1000, 0, 0, true},
		{"daily order cap reached", 100, 1000, 40, 0, true},
		{"one below the daily cap", 100, 1000, 39, 0, false},
		{"position cap reached", 100, 1000, 0, 60, true},
	}
	for _, c := range cases {
		if got := s.CheckEntry(c.cost, c.equity, c.today, c.open); (got != "") != c.wantRejection {
			t.Errorf("%s: reason %q, want rejection=%v", c.name, got, c.wantRejection)
		}
	}
	if got := (State{}).CheckEntry(1e9, 1000, 1e6, 1e6); got != "" {
		t.Errorf("zero caps must limit nothing, got %q", got)
	}
}

func TestCountsAndCapsSurviveSaveAndLoad(t *testing.T) {
	db := openDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE stage_order (account_number TEXT, instruction TEXT, status TEXT, submitted_at DATETIME, staged_at DATETIME)`,
		`INSERT INTO account_state_position (account_number, symbol, long_quantity, market_value) VALUES ('A', 'X', 5, 1), ('A', 'Y', 0, 0), ('B', 'X', 9, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Date(2026, 10, 15, 4, 0, 0, 0, time.UTC)
	rows := []struct {
		acct, instr, status string
		at                  time.Time
	}{
		{"A", "BUY", "SUBMITTED", start.Add(time.Hour)},                  // counts
		{"A", "BUY", "SUBMITTED_PENDING_FILL", start.Add(2 * time.Hour)}, // counts
		{"A", "BUY", "SUBMITTED", start.Add(-time.Hour)},                 // yesterday
		{"A", "BUY", "REJECTED", start.Add(time.Hour)},                   // never placed
		{"A", "SELL", "SUBMITTED", start.Add(time.Hour)},                 // an exit, not an entry
		{"B", "BUY", "SUBMITTED", start.Add(time.Hour)},                  // another account
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO stage_order VALUES (?,?,?,?,?)`, r.acct, r.instr, r.status, r.at, r.at); err != nil {
			t.Fatal(err)
		}
	}
	orders, open, err := Counts(db, "A", start)
	if err != nil || orders != 2 || open != 1 {
		t.Fatalf("Counts = %d orders, %d open, %v; want 2, 1", orders, open, err)
	}

	s := Assess(DefaultLimits(), 1000, 1000, 1000, 0)
	s.Account, s.AssessedAt, s.Session = "A", time.Now(), "2026-10-15"
	if err := save(db, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(db, "A")
	if err != nil || got.MaxOrderPct != 0.60 || got.MaxOrdersPerDay != 40 || got.MaxPositions != 60 {
		t.Fatalf("Load = %+v, %v", got, err)
	}
}

func recordRuns(t *testing.T, db *sql.DB, job string, statuses ...string) {
	t.Helper()
	for _, st := range statuses {
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: job, Status: st}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutoKillReason(t *testing.T) {
	db := openDB(t)
	l := DefaultLimits()
	if got, err := AutoKillReason(db, l, nil); err != nil || got != "" {
		t.Fatalf("empty database: %q, %v", got, err)
	}
	if got, _ := AutoKillReason(db, l, []State{{Account: "A", Drawdown: 0.34}}); got != "" {
		t.Errorf("34%% drawdown must not fire at the 35%% level: %q", got)
	}
	if got, _ := AutoKillReason(db, l, []State{{Account: "A", Drawdown: 0.35}}); got == "" {
		t.Errorf("35%% drawdown must fire")
	}

	recordRuns(t, db, "submit_orders", "FAILED", "FAILED", "FAILED", "FAILED", "FAILED")
	if got, _ := AutoKillReason(db, l, nil); got != "" {
		t.Errorf("5 failures in a row is under the limit of 6: %q", got)
	}
	recordRuns(t, db, "submit_orders", "FAILED")
	if got, _ := AutoKillReason(db, l, nil); got == "" {
		t.Errorf("6 failures in a row must fire")
	}
	recordRuns(t, db, "submit_orders", "OK")
	if got, _ := AutoKillReason(db, l, nil); got != "" {
		t.Errorf("a success ends the streak: %q", got)
	}
	recordRuns(t, db, "send_email", "FAILED", "FAILED", "FAILED", "FAILED", "FAILED", "FAILED", "FAILED")
	if got, _ := AutoKillReason(db, l, nil); got != "" {
		t.Errorf("a non-order job failing must not fire: %q", got)
	}
	l.AutoKillFailures, l.AutoKillDrawdown = 0, 0
	recordRuns(t, db, "stage_orders", "FAILED", "FAILED", "FAILED", "FAILED", "FAILED", "FAILED", "FAILED")
	if got, _ := AutoKillReason(db, l, []State{{Drawdown: 0.9}}); got != "" {
		t.Errorf("both triggers off: %q", got)
	}
}

func TestRunJobEngagesKillswitchOnDeepDrawdown(t *testing.T) {
	db := openDB(t)
	day := func(n int) time.Time { return time.Date(2026, 10, 12+n, 20, 0, 0, 0, time.UTC) }
	run := func(n int, equity float64, dry bool) *Result {
		setEquity(t, db, "111", equity, day(n))
		res, err := RunJob(context.Background(), db, &bytes.Buffer{}, JobOptions{Limits: DefaultLimits(), Now: day(n), DryRun: dry})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	run(0, 1000, false)
	if r := run(1, 640, true); r.AutoKill == "" { // 36% below the peak, dry run
		t.Fatalf("dry run should report the trigger")
	}
	if st, _ := killswitch.Current(db); st.Engaged {
		t.Fatalf("a dry run must not engage the killswitch")
	}
	if r := run(1, 640, false); r.AutoKill == "" {
		t.Fatalf("live run should fire")
	}
	st, err := killswitch.Current(db)
	if err != nil || !st.Engaged || st.ChangedBy != "auto: risk" || !strings.Contains(st.Reason, "high-water mark") {
		t.Fatalf("killswitch = %+v, %v", st, err)
	}
}
