package strategy_status

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/submit_orders"
	_ "modernc.org/sqlite"
)

const strat = "tsll-daily-one-share"

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, ensure := range []func(*sql.DB) error{scan_market.EnsureSchema, stage_orders.EnsureSchema,
		submit_orders.EnsureSchema, manage_exits.EnsureSchema, job_runs.EnsureSchema} {
		if err := ensure(db); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func et(y int, m time.Month, d, hh, mm int) time.Time {
	loc, _ := time.LoadLocation("America/New_York")
	return time.Date(y, m, d, hh, mm, 0, 0, loc)
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func openTrade(t *testing.T, db *sql.DB, order string, fill time.Time) {
	t.Helper()
	if _, err := manage_exits.Open(db, manage_exits.OpenTradeParams{
		AccountNumber: "25474194", Symbol: "TSLL", StrategyName: strat, StrategyID: strat, EntryOrderID: order,
		EntryPrice: 10.15, TargetProfitPrice: 10.66, StopLossPrice: 8.12, Quantity: 1, EntryTime: fill, FilledAt: fill, MaxTradingDays: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func scanRun(t *testing.T, db *sql.DB, status, summary string, at time.Time) {
	t.Helper()
	if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: "scan_market", Status: status, Summary: summary, FinishedAt: at.UTC()}); err != nil {
		t.Fatal(err)
	}
}

func problems(s *Status, where, contains string) bool {
	for _, p := range s.Problems {
		if p.Where == where && strings.Contains(p.Detail, contains) {
			return true
		}
	}
	return false
}

// A scan that covered today and found nothing is NO_SIGNAL, not "never ran".
func TestSignalCheckDistinguishesNoSignalFromNotRun(t *testing.T) {
	db := newDB(t)
	now := et(2026, 10, 8, 18, 0)

	s, err := Build(db, strat, strat, now, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.SignalCheck.Result != "NOT_RUN" || s.Verdict != VerdictAttention {
		t.Fatalf("no scan on a trading day must need attention: %+v verdict %s", s.SignalCheck, s.Verdict)
	}

	sat, err := Build(db, strat, strat, et(2026, 10, 10, 12, 0), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sat.SignalCheck.Result != "NOT_RUN" || sat.Verdict != VerdictOK {
		t.Fatalf("a Saturday expects no scan: %+v verdict %s", sat.SignalCheck, sat.Verdict)
	}

	scanRun(t, db, job_runs.JobRunEmpty, "as_of=2026-10-08 next=2026-10-09 members=1 livescan_signals=0 buy_signals=0", now)
	s, _ = Build(db, strat, strat, now, 0, nil)
	if s.SignalCheck.Result != "NO_SIGNAL" || !s.SignalCheck.CoversDay {
		t.Fatalf("scan covered today with nothing: %+v", s.SignalCheck)
	}

	scanRun(t, db, job_runs.JobRunFailed, "livescan unavailable", now.Add(time.Minute))
	s, _ = Build(db, strat, strat, now, 0, nil)
	if s.SignalCheck.Result != "SCAN_FAILED" || s.Verdict != VerdictProblem {
		t.Fatalf("failed scan: %+v verdict %s", s.SignalCheck, s.Verdict)
	}
}

func TestTodaysSignalIsListedAndDismissalExplained(t *testing.T) {
	db := newDB(t)
	now := et(2026, 10, 8, 18, 0)
	scanRun(t, db, job_runs.JobRunOK, "as_of=2026-10-08 next=2026-10-09 members=1 livescan_signals=1 buy_signals=1", now)
	mustExec(t, db, `INSERT INTO scan_market_buy_signal (scan_run_id, strategy_id, symbol, as_of_date, next_session, buy_limit, take_profit, stop_loss, hold_days, status, notes, created_at)
		VALUES ('r1', ?, 'TSLL', '2026-10-08', '2026-10-09', 10.15, 10.66, 8.12, 1, 'DISMISSED', 'already holding TSLL', ?)`, strat, now.UTC())
	mustExec(t, db, `INSERT INTO scan_market_buy_signal (scan_run_id, strategy_id, symbol, as_of_date, next_session, status, created_at)
		VALUES ('r0', 'other-strategy', 'MARA', '2026-10-08', '2026-10-09', 'NEW', ?)`, now.UTC())

	s, err := Build(db, strat, strat, now, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.SignalCheck.Result != "SIGNAL" || len(s.SignalCheck.Signals) != 1 || !s.SignalCheck.Signals[0].ForToday {
		t.Fatalf("signals = %+v", s.SignalCheck)
	}
	if !problems(s, "stage", "already holding TSLL") {
		t.Errorf("a dismissed signal must say why: %+v", s.Problems)
	}
}

// The incident this endpoint exists for: an OPEN row the broker does not hold, a row
// closed by time expiry before its exit date, and an overdue exit are all PROBLEMs.
func TestPositionsAreCheckedAgainstTheBrokerAndTheirHoldWindow(t *testing.T) {
	db := newDB(t)
	mustExec(t, db, `CREATE TABLE account_state_position (account_number TEXT, symbol TEXT, long_quantity REAL, fetched_at DATETIME)`)
	scanRun(t, db, job_runs.JobRunOK, "as_of=2026-10-07 next=2026-10-08 members=1 livescan_signals=1 buy_signals=1", et(2026, 10, 7, 18, 0))
	fill := et(2026, 10, 8, 9, 30)
	openTrade(t, db, "1008217638981", fill)

	// 10:00 the same day: held 0 days, exits 10/09, broker holds the share.
	mustExec(t, db, `INSERT INTO account_state_position VALUES ('25474194','TSLL',1,?)`, et(2026, 10, 8, 9, 55).UTC())
	s, err := Build(db, strat, strat, et(2026, 10, 8, 10, 0), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Positions) != 1 {
		t.Fatalf("positions = %+v", s.Positions)
	}
	p := s.Positions[0]
	if p.ExitDate != "2026-10-09" || p.TradingDaysHeld != 0 || p.DaysRemaining != 1 || p.ExitOverdue {
		t.Errorf("hold window = %+v", p)
	}
	if p.ConfirmedAtBroker == nil || !*p.ConfirmedAtBroker || s.Verdict != VerdictOK || s.BrokerSnapshotAt == "" {
		t.Errorf("a held, in-window position is healthy: confirmed=%v verdict=%s %+v", p.ConfirmedAtBroker, s.Verdict, s.Problems)
	}

	// The broker no longer holds it.
	mustExec(t, db, `UPDATE account_state_position SET long_quantity = 0`)
	s, _ = Build(db, strat, strat, et(2026, 10, 8, 10, 0), 0, nil)
	if !problems(s, "position", "mirror broker read shows 0 held") || s.Verdict != VerdictProblem {
		t.Errorf("OPEN row with no broker position: %+v", s.Problems)
	}

	// Two sessions later it is overdue.
	s, _ = Build(db, strat, strat, et(2026, 10, 12, 10, 0), 0, nil)
	if !problems(s, "exit", "past its exit date 2026-10-09") {
		t.Errorf("overdue: %+v", s.Problems)
	}
}

func TestEarlyTimeExpiryCloseIsAProblem(t *testing.T) {
	db := newDB(t)
	fill := et(2026, 10, 8, 9, 30)
	openTrade(t, db, "1008217638981", fill)
	var id int64
	if err := db.QueryRow(`SELECT id FROM manage_exit`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.CloseActiveTrade(db, id, manage_exits.TradeStatusClosedTimeExpiry, "WINDOW_EXPIRED_MARKET_SELL", 9.9, et(2026, 10, 8, 9, 35)); err != nil {
		t.Fatal(err)
	}
	s, err := Build(db, strat, strat, et(2026, 10, 8, 11, 0), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Closed) != 1 || !problems(s, "exit", "before its exit date 2026-10-09") || s.Verdict != VerdictProblem {
		t.Fatalf("closed=%+v problems=%+v", s.Closed, s.Problems)
	}
}

func TestStagedAndSubmitFailuresAreReported(t *testing.T) {
	db := newDB(t)
	now := et(2026, 10, 8, 10, 0)
	mustExec(t, db, `INSERT INTO stage_order (account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price, status, reject_reason, staged_at)
		VALUES ('25474194', ?, 'TSLL', 1, 10.15, 10.66, 8.12, 'REJECTED', 'insufficient buying power', ?)`, strat, now.UTC())
	mustExec(t, db, `INSERT INTO submit_order (staged_order_id, symbol, quantity, status, detail, submitted_at) VALUES (1, 'TSLL', 1, 'FAILED_SUBMIT', 'HTTP 400', ?)`, now.UTC())
	mustExec(t, db, `INSERT INTO stage_order (account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price, status, staged_at)
		VALUES ('1', 'someone-else', 'MARA', 1, 1, 2, 0.5, 'REJECTED', ?)`, now.UTC())

	s, err := Build(db, strat, "", now, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Staged) != 1 || len(s.Attempts) != 1 {
		t.Fatalf("staged=%d attempts=%d (another strategy's rows leaked in?)", len(s.Staged), len(s.Attempts))
	}
	for _, want := range [][2]string{{"stage", "insufficient buying power"}, {"submit", "HTTP 400"}, {"scan", "not in STRATEGY_ALLOWLIST"}} {
		if !problems(s, want[0], want[1]) {
			t.Errorf("missing %v in %+v", want, s.Problems)
		}
	}
}

func TestInAllowlistMatchesStackMembers(t *testing.T) {
	list := "sig-voo-buy-tecl+mara_tree+pdd_tree, tsll-daily-one-share"
	for _, id := range []string{"mara_tree", "tsll-daily-one-share", "sig-voo-buy-tecl"} {
		if !inAllowlist(list, id) {
			t.Errorf("%s should be greenlit", id)
		}
	}
	if !inAllowlist(list, "sig-voo-buy-tecl+mara_tree+pdd_tree") {
		t.Error("the whole stack id should be greenlit")
	}
	if inAllowlist(list, "tsll") || inAllowlist("", "x") {
		t.Error("partial or empty matches must not count")
	}
}

func TestMembersSplitsStacksAndDeduplicates(t *testing.T) {
	got := Members("sig-voo-buy-tecl+mara_tree+pdd_tree, tsll-daily-one-share ,mara_tree,")
	want := []string{"sig-voo-buy-tecl", "mara_tree", "pdd_tree", "tsll-daily-one-share"}
	if len(got) != len(want) {
		t.Fatalf("members = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("members = %v, want %v", got, want)
		}
	}
	if len(Members("")) != 0 {
		t.Error("empty allowlist has no members")
	}
}

// The newest scan's reason for the session it evaluated is shown with the verdict.
func TestSignalCheckCarriesTheScansReason(t *testing.T) {
	db := newDB(t)
	now := et(2026, 10, 8, 18, 0)
	scanRun(t, db, job_runs.JobRunEmpty, "as_of=2026-10-08 next=2026-10-09 members=1 livescan_signals=0 buy_signals=0", now)
	if s, _ := Build(db, strat, strat, now, 0, nil); s.SignalCheck.Why != "" {
		t.Fatalf("a reason with no trace: %q", s.SignalCheck.Why)
	}
	for _, q := range []string{
		`INSERT INTO scan_market_trace (scan_run_id, strategy_id, session_date, is_asof, entry, reason, created_at) VALUES ('r1', '` + strat + `', '2026-10-07', 0, 0, 'older session', '2026-10-07T22:00:00Z')`,
		`INSERT INTO scan_market_trace (scan_run_id, strategy_id, session_date, is_asof, entry, reason, created_at) VALUES ('r1', '` + strat + `', '2026-10-08', 1, 0, 'no entry: state is sideways, the strategy needs bull', '2026-10-08T22:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := Build(db, strat, strat, now, 0, nil)
	if s.SignalCheck.Why != "no entry: state is sideways, the strategy needs bull" {
		t.Fatalf("why %q", s.SignalCheck.Why)
	}
}
