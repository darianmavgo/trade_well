package live_vs_backtest_winrate

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_trades"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	_ "modernc.org/sqlite"
)

func open(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// addLive writes n closed live trades, wins of them with positive pnl, and the same n replayed
// by the backtest engine with modelWins winners.
func addLive(t *testing.T, db *sql.DB, id string, n, wins, modelWins int) {
	t.Helper()
	for i := 0; i < n; i++ {
		pnl, model := -1.0, -0.01
		if i < wins {
			pnl = 2
		}
		if i < modelWins {
			model = 0.02
		}
		mustExec(t, db, `INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, status, pnl, pnl_pct, exit_date, entry_price, target_profit_price, stop_loss_price, quantity, entry_time, max_trading_days)
			VALUES ('1', 'X', ?, ?, ?, 'CLOSED_TARGET', ?, ?, ?, 10, 11, 9, 1, ?, 1)`, id, id, fmt.Sprintf("%s-%d", id, i), pnl, pnl, fmt.Sprintf("2026-09-%02d 13:30:00 +0000 +0000", i+1), fmt.Sprintf("2026-09-%02d 13:30:00 +0000 +0000", i+1))
		mustExec(t, db, `INSERT INTO live_vs_backtest_trade (strategy_id, symbol, as_of_date, live_outcome, live_return_pct, model_outcome, model_return_pct, built_at)
			VALUES (?, 'X', ?, 'CLOSED', ?, 'CLOSED', ?, 'now')`, id, fmt.Sprintf("2026-09-%02d", i+1), pnl, model)
	}
}

func byID(rows []Row) map[string]Row {
	m := map[string]Row{}
	for _, r := range rows {
		m[r.Strategy] = r
	}
	return m
}

func TestRefreshThenCompare(t *testing.T) {
	ctx := context.Background()
	evals, orch := open(t, "evals.db"), open(t, "orch.db")
	mustExec(t, evals, `CREATE TABLE strategy_evals (id INTEGER PRIMARY KEY AUTOINCREMENT, strategy_id TEXT, is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT,
		is_trades INTEGER, is_win_rate REAL, oos_trades INTEGER, oos_win_rate REAL, created_at TEXT)`)
	ins := `INSERT INTO strategy_evals (strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate, created_at) VALUES (?, ?, ?, ?, ?, ?)`
	mustExec(t, evals, ins, "old", 100, 0.10, 50, 0.10, "2026-01-01") // superseded by the next row
	mustExec(t, evals, ins, "old", 100, 0.70, 50, 0.65, "2026-02-01")
	mustExec(t, evals, ins, "inline", 100, 0.60, 40, 0.60, "2026-02-01")
	mustExec(t, evals, ins, "early", 100, 0.60, 40, 0.60, "2026-02-01")
	mustExec(t, evals, ins, "above", 100, 0.50, 40, 0.30, "2026-02-01")
	mustExec(t, evals, ins, "idle", 100, 0.55, 40, 0.57, "2026-02-01")

	if err := live_vs_backtest_trades.EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	addLive(t, orch, "old", 20, 4, 11)    // 20% live vs 65% out-of-sample: BELOW
	addLive(t, orch, "inline", 12, 7, 7)  // 58% vs 60%: IN_LINE
	addLive(t, orch, "early", 3, 0, 0)    // too few trades to judge
	addLive(t, orch, "above", 20, 19, 18) // 95% vs 30%: ABOVE
	addLive(t, orch, "nobt", 12, 6, 6)    // no backtest row
	mustExec(t, orch, `INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, status, entry_price, target_profit_price, stop_loss_price, quantity, entry_time, max_trading_days) VALUES ('1', 'X', 'old', 'old', 'open-1', 'OPEN', 10, 11, 9, 1, 'now', 1)`)

	// Before any refresh the backtest side is empty and the live side still shows.
	rows, err := Compare(ctx, orch, Options{IDs: []string{"quiet"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := byID(rows)["old"]; got.Verdict != NoBacktest || got.LiveDecisions != 20 || got.OOSWinRate != nil {
		t.Fatalf("before refresh: %+v", got)
	}

	if n, err := Refresh(ctx, orch, Sources{EvalsDB: filepath.Join(t.TempDir(), "missing.db")}, false); err == nil {
		t.Fatalf("missing evals db accepted (n=%d)", n)
	}
	if n, err := Refresh(ctx, orch, Sources{EvalsDB: evalsPath(t, evals)}, true); err != nil || n != 5 {
		t.Fatalf("dry run = %d, %v", n, err)
	}
	if err := orch.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'live_vs_backtest_winrate'`).Scan(new(int)); err != nil {
		t.Fatal(err)
	}
	if _, err := orch.Exec(`SELECT 1 FROM live_vs_backtest_winrate`); err == nil {
		t.Fatal("dry run created live_vs_backtest_winrate")
	}
	if n, err := Refresh(ctx, orch, Sources{EvalsDB: evalsPath(t, evals)}, false); err != nil || n != 5 {
		t.Fatalf("refresh = %d, %v", n, err)
	}
	// Refreshing twice replaces rather than duplicates.
	if n, err := Refresh(ctx, orch, Sources{EvalsDB: evalsPath(t, evals)}, false); err != nil || n != 5 {
		t.Fatalf("second refresh = %d, %v", n, err)
	}

	rows, err = Compare(ctx, orch, Options{IDs: []string{"quiet"}})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(rows)
	if _, ok := got["idle"]; ok {
		t.Error("a strategy with a backtest but no live trades and not greenlit is listed")
	}
	want := map[string]string{"old": Below, "inline": InLine, "early": TooEarly, "above": Above, "nobt": NoBacktest, "quiet": NoBacktest}
	for id, v := range want {
		if got[id].Verdict != v {
			t.Errorf("%s verdict = %q, want %q (%+v)", id, got[id].Verdict, v, got[id])
		}
	}
	if q := got["quiet"]; !q.Greenlit || q.LiveDecisions != 0 || q.LiveWinRate != nil {
		t.Errorf("greenlit strategy with no data = %+v", q)
	}
	if got["old"].OpenPositions != 1 {
		t.Errorf("open positions = %d", got["old"].OpenPositions)
	}
	old := got["old"]
	if old.OOSWinRate == nil || *old.OOSWinRate != 0.65 || *old.ISWinRate != 0.70 || old.BaseKind != "oos" {
		t.Errorf("old backtest side = %+v", old)
	}
	if old.LiveDecisions != 20 || old.LiveWins != 4 || *old.LiveWinRate != 0.2 || *old.ModelWinRate != 0.55 {
		t.Errorf("old live side = %+v", old)
	}
	if *old.LiveCILow < 0.07 || *old.LiveCILow > 0.09 || *old.LiveCIHigh < 0.40 || *old.LiveCIHigh > 0.43 {
		t.Errorf("Wilson interval for 4/20 = %v..%v, want about 0.08..0.42", *old.LiveCILow, *old.LiveCIHigh)
	}
	if d := *old.LiveMinusBase; d > -0.449 || d < -0.451 {
		t.Errorf("live minus baseline = %v", d)
	}
	if len(old.Progress) != 20 || old.Progress[0].N != 1 || !old.Progress[0].Win || old.Progress[0].Rate != 1 ||
		old.Progress[19].Wins != 4 || old.Progress[19].Rate != 0.2 || old.Progress[19].CILow >= old.Progress[19].Rate {
		t.Errorf("progress = %+v", old.Progress)
	}
	if old.Progress[0].CIHigh != 1 || old.Progress[0].CILow > 0.3 {
		t.Errorf("first point interval = %v..%v, want wide", old.Progress[0].CILow, old.Progress[0].CIHigh)
	}
	if rows[0].Verdict != Below {
		t.Errorf("first row is %s, want BELOW first", rows[0].Verdict)
	}

	// Compare leaves no scratch table behind and writes nothing.
	var scratch int
	if err := orch.QueryRow(`SELECT COUNT(*) FROM sqlite_temp_master WHERE name LIKE 'winrate_%'`).Scan(&scratch); err != nil || scratch != 0 {
		t.Errorf("temp slices left behind: %d, %v", scratch, err)
	}

	var buf bytes.Buffer
	Format(&buf, rows)
	if s := buf.String(); !strings.Contains(s, "old") || !strings.Contains(s, "20% (n=20)") || !strings.Contains(s, "BELOW") {
		t.Errorf("table:\n%s", s)
	}
}

func evalsPath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var seq int
	var name, file string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestCompareOnEmptyDatabase(t *testing.T) {
	rows, err := Compare(context.Background(), open(t, "empty.db"), Options{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows = %v, err = %v", rows, err)
	}
}

// With nothing refreshed, a refdata strategies.db on this machine still supplies the
// whole-window backtest rate, and a greenlit strategy is baselined against it.
func TestCompareReadsLastBacktestFromRefdata(t *testing.T) {
	ctx := context.Background()
	ref, orch := open(t, "strategies.db"), open(t, "orch.db")
	mustExec(t, ref, `CREATE TABLE strategy_last_backtest (strategy_id TEXT PRIMARY KEY, result_db TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT '', win_rate REAL, total_trades INTEGER NOT NULL DEFAULT 0)`)
	mustExec(t, ref, `INSERT INTO strategy_last_backtest VALUES ('mull', 'r.db', '2026-10-09', 0.88, 86), ('other', 'r.db', '2026-10-09', 0.5, 9)`)
	refPath := evalsPath(t, ref)

	rows, err := Compare(ctx, orch, Options{IDs: []string{"mull"}, RefDB: refPath})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, err = %v", rows, err)
	}
	r := rows[0]
	if r.AllWinRate == nil || *r.AllWinRate != 0.88 || r.AllTrades != 86 || r.BaseKind != "all" || r.Verdict != TooEarly {
		t.Errorf("row = %+v", r)
	}

	// Refresh stores it, so a service without the file shows the same.
	if n, err := Refresh(ctx, orch, Sources{RefDB: refPath, IDs: []string{"mull", "other"}}, false); err != nil || n != 2 {
		t.Fatalf("refresh = %d, %v", n, err)
	}
	rows, err = Compare(ctx, orch, Options{IDs: []string{"mull"}})
	if err != nil || len(rows) != 1 || rows[0].AllWinRate == nil {
		t.Fatalf("after refresh rows = %+v, err = %v", rows, err)
	}
}

// A reports folder gives the in-sample and out-of-sample pass of each run; a newer run of the
// same strategy replaces an older one.
func TestRefreshFromReportsFolder(t *testing.T) {
	ctx := context.Background()
	reports, orch := t.TempDir(), open(t, "orch.db")
	mk := func(path, id string, trades int, rate float64) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		mustExec(t, db, `CREATE TABLE performance_summary (strategy_id TEXT, start_date TEXT, end_date TEXT, total_trades INTEGER, win_rate REAL, created_at TEXT)`)
		mustExec(t, db, `INSERT INTO performance_summary VALUES (?, '2021-01-01', '2025-10-12', ?, ?, 'x')`, id, trades, rate)
	}
	mk(filepath.Join(reports, "1", "markov.db"), "mull", 10, 0.5)
	mk(filepath.Join(reports, "2", "markov.db"), "mull", 33, 0.85)
	mk(filepath.Join(reports, "2", "oos", "markov.db"), "mull", 56, 0.89)
	mk(filepath.Join(reports, "2", "streak.db"), "shpu", 5, 1)
	mustExec(t, orch, `CREATE TABLE unrelated (x)`)

	if n, err := Refresh(ctx, orch, Sources{Reports: reports}, true); err != nil || n != 4 {
		t.Fatalf("dry run = %d, %v", n, err)
	}
	if _, err := Refresh(ctx, orch, Sources{Reports: reports}, false); err != nil {
		t.Fatal(err)
	}
	rows, err := Compare(ctx, orch, Options{IDs: []string{"mull", "shpu"}})
	if err != nil {
		t.Fatal(err)
	}
	got := byID(rows)
	m := got["mull"]
	if m.ISWinRate == nil || *m.ISWinRate != 0.85 || m.ISTrades != 33 || m.OOSWinRate == nil || *m.OOSWinRate != 0.89 || m.OOSTrades != 56 || m.BaseKind != "oos" {
		t.Errorf("mull = %+v", m)
	}
	if s := got["shpu"]; s.ISWinRate == nil || s.OOSWinRate != nil || s.BaseKind != "is" {
		t.Errorf("shpu = %+v", s)
	}
}

// Refresh given IDs copies only those strategies; refdata holds tens of thousands.
func TestRefreshCopiesOnlyTheRequestedStrategies(t *testing.T) {
	ctx := context.Background()
	ref, orch := open(t, "strategies.db"), open(t, "orch.db")
	mustExec(t, ref, `CREATE TABLE strategy_last_backtest (strategy_id TEXT PRIMARY KEY, result_db TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT '', win_rate REAL, total_trades INTEGER NOT NULL DEFAULT 0)`)
	mustExec(t, ref, `INSERT INTO strategy_last_backtest VALUES ('mull', 'r', 'd', 0.88, 86), ('other', 'r', 'd', 0.5, 9), ('third', 'r', 'd', 0.6, 9)`)
	if n, err := Refresh(ctx, orch, Sources{RefDB: evalsPath(t, ref), IDs: []string{"mull"}}, true); err != nil || n != 1 {
		t.Fatalf("dry run = %d, %v", n, err)
	}
	if n, err := Refresh(ctx, orch, Sources{RefDB: evalsPath(t, ref), IDs: []string{"mull"}}, false); err != nil || n != 1 {
		t.Fatalf("refresh = %d, %v", n, err)
	}
}

// A fresh database with nothing refreshed still shows the backtest rates compiled into the binary,
// and a stored row overrides them.
func TestCompareFallsBackToTheCompiledInBaseline(t *testing.T) {
	base, err := Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if len(base) == 0 {
		t.Fatal("baseline.json is empty: a fresh deploy would show no backtest rates")
	}
	ctx := context.Background()
	orch := open(t, "fresh.db")
	id := base[0].Strategy
	rows, err := Compare(ctx, orch, Options{IDs: []string{id}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, err = %v", rows, err)
	}
	r := rows[0]
	if r.Verdict == NoBacktest || r.BaseKind == "" || r.BacktestSource != "bundled with this deploy" {
		t.Errorf("fresh database row = %+v", r)
	}
	for _, b := range base {
		if b.ISWinRate == nil && b.OOSWinRate == nil && b.AllWinRate == nil {
			t.Errorf("baseline row %s has no win rate", b.Strategy)
		}
	}

	if err := EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	mustExec(t, orch, `INSERT INTO live_vs_backtest_winrate (strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate, synced_at) VALUES (?, 7, 0.123, 7, 0.456, '2026-10-16')`, id)
	rows, err = Compare(ctx, orch, Options{IDs: []string{id}})
	if err != nil || rows[0].OOSWinRate == nil || *rows[0].OOSWinRate != 0.456 || rows[0].BacktestSource != "refreshed 2026-10-16" {
		t.Errorf("stored row did not override the baseline: %+v, %v", rows, err)
	}
}

// Recent lists what closed today and yesterday, stop losses marked, and leaves out older closes,
// open positions and cancelled entries.
func TestRecentListsStopLossesFromYesterdayAndThisMorning(t *testing.T) {
	ctx := context.Background()
	db := open(t, "orch.db")
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	at := func(days int) string {
		return time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02") + " 13:30:22 +0000 +0000"
	}
	add := func(id, status, code, exit string, acct string, pnl float64) {
		mustExec(t, db, `INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, status, exit_reason_code, exit_time,
			entry_price, exit_price, quantity, pnl, pnl_pct, target_profit_price, stop_loss_price, entry_time, max_trading_days)
			VALUES (?, 'AMUU', 'amuu', 'amuu', ?, ?, ?, ?, 271.76, 217.41, 10, ?, -20, 300, 217, 'x', 1)`, acct, id, status, code, exit, pnl)
	}
	add("a", "CLOSED_STOP_LOSS", "STOP_LOSS", at(0), "111", -543)
	add("b", "CLOSED_PROFIT", "TARGET", at(1), "222", 120)
	add("c", "CLOSED_STOP_LOSS", "STOP_LOSS", at(10), "333", -50) // too old
	add("d", "CANCELLED", "ORDER_FAILED", at(0), "444", 0)        // never traded
	add("e", "OPEN", "", "", "555", 0)

	got, err := Recent(ctx, db, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].StopLoss || got[0].Account != "111" || got[0].PnL != -543 || got[1].StopLoss || got[1].Account != "222" {
		t.Fatalf("2 days = %+v", got)
	}
	if got, _ := Recent(ctx, db, 1, 0); len(got) != 1 || got[0].Account != "111" {
		t.Errorf("1 day = %+v", got)
	}
	if got, _ := Recent(ctx, db, 30, 0); len(got) != 3 {
		t.Errorf("30 days = %d trades, want 3", len(got))
	}
	if got, err := Recent(ctx, open(t, "empty.db"), 3, 0); err != nil || len(got) != 0 {
		t.Errorf("empty database = %v, %v", got, err)
	}
	var scratch int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_temp_master WHERE name LIKE 'winrate_%'`).Scan(&scratch); err != nil || scratch != 0 {
		t.Errorf("temp slices left behind: %d, %v", scratch, err)
	}
}

// One decision filled in three accounts is one live decision, not three: a stop loss in every
// account is 0 of 1, and a decision whose fills add up above zero is a win.
func TestLiveWinRateCountsDecisionsNotAccountFills(t *testing.T) {
	ctx := context.Background()
	db := open(t, "orch.db")
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	add := func(acct, symbol, entry string, pnl float64) {
		mustExec(t, db, `INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, status, exit_time, pnl, pnl_pct,
			entry_price, target_profit_price, stop_loss_price, quantity, entry_time, max_trading_days)
			VALUES (?, ?, 'amuu', 'amuu', ?, 'CLOSED_STOP_LOSS', ?, ?, -20, 10, 11, 9, 1, ?, 1)`,
			acct, symbol, acct+symbol+entry, entry+" 20:00:00 +0000 +0000", pnl, entry+" 13:30:13 +0000 +0000")
	}
	for _, a := range []string{"1", "2", "3"} { // one stop loss, three accounts
		add(a, "AMUU", "2026-10-14", -100)
	}
	rows, err := Compare(ctx, db, Options{IDs: []string{"amuu"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	r := rows[0]
	if r.LiveDecisions != 1 || r.LiveWins != 0 || r.LiveFills != 3 || r.LiveWinRate == nil || *r.LiveWinRate != 0 {
		t.Errorf("three fills of one stop loss = %+v", r)
	}
	if len(r.Progress) != 1 {
		t.Errorf("chart points = %d, want one per decision", len(r.Progress))
	}
	// A second decision that wins in total, though one of its fills lost.
	add("1", "TQQQ", "2026-10-15", -10)
	add("2", "TQQQ", "2026-10-15", 50)
	rows, _ = Compare(ctx, db, Options{IDs: []string{"amuu"}})
	if r := rows[0]; r.LiveDecisions != 2 || r.LiveWins != 1 || r.LiveFills != 5 {
		t.Errorf("after a winning decision = %+v", r)
	}
}

// A refdata strategies.db holds tens of thousands of strategies, so a refresh must be told which to copy.
func TestRefreshOfARefdataAssetNeedsStrategyIDs(t *testing.T) {
	ref := open(t, "strategies.db")
	mustExec(t, ref, `CREATE TABLE strategy_last_backtest (strategy_id TEXT PRIMARY KEY, result_db TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT '', win_rate REAL, total_trades INTEGER NOT NULL DEFAULT 0)`)
	if _, err := Refresh(context.Background(), open(t, "orch.db"), Sources{RefDB: evalsPath(t, ref)}, false); err == nil || !strings.Contains(err.Error(), "name the ones to copy") {
		t.Fatalf("want an error asking for strategy ids, got %v", err)
	}
}

// runTables counts the per-run slice tables of the comparison left in db.
func runTables(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'winrate\_%\_\_r%' ESCAPE '\'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The slice tables exist only while a calculation runs. After Compare and Recent return, none is
// left, and live_vs_backtest_winrate is the one win-rate table in the database.
func TestCompareAndRecentLeaveNoRunTablesBehind(t *testing.T) {
	ctx := context.Background()
	orch := open(t, "orch.db")
	if err := live_vs_backtest_trades.EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	addLive(t, orch, "leaves-nothing", 12, 6, 6)
	rows, err := Compare(ctx, orch, Options{IDs: []string{"leaves-nothing"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("Compare = %v, %v", rows, err)
	}
	if _, err := Recent(ctx, orch, 2, 0); err != nil {
		t.Fatal(err)
	}
	if n := runTables(t, orch); n != 0 {
		t.Fatalf("%d run tables left behind", n)
	}
	var winrate int
	if err := orch.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE '%winrate%'`).Scan(&winrate); err != nil {
		t.Fatal(err)
	}
	if winrate != 0 {
		// live_vs_backtest_winrate is created by Refresh/EnsureSchema, not by Compare, so it is absent here
		t.Fatalf("Compare created %d win-rate tables of its own", winrate)
	}
}

// A calculation that fails half way still drops what it built.
func TestCompareDropsItsTablesWhenItFails(t *testing.T) {
	orch := open(t, "orch.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compare(ctx, orch, Options{IDs: []string{"fails"}}); err == nil {
		t.Fatal("Compare with a cancelled context succeeded")
	}
	if n := runTables(t, orch); n != 0 {
		t.Fatalf("a failed Compare left %d run tables", n)
	}
}

// Tables left by an earlier process (older runs, including the kind Turso still holds from before
// this change) are swept by the next calculation; a run that is in progress, and the stored
// backtest rates, are not touched.
func TestCompareSweepsTablesLeftByEarlierRuns(t *testing.T) {
	ctx := context.Background()
	orch := open(t, "orch.db")
	if err := EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	mustExec(t, orch, `INSERT INTO live_vs_backtest_winrate (strategy_id, is_trades, is_win_rate, synced_at) VALUES ('stored', 10, 0.5, 'now')`)
	inFlight := "winrate_compare__r" + time.Now().UTC().Format("20060102t150405") + "001"
	for _, name := range []string{
		"winrate_compare__r20261026t233551001_kdf2d8bfc53", // a keyed run from before, as in Turso today
		"winrate_progress__r20261026t220910001",
		"winrate_baseline__r20260101t000000001",
		"winrate_done__r20261026t233551001_kdf2d8bfc53", // the marker older versions left per run
		inFlight, // another request's run that began just now
	} {
		mustExec(t, orch, `CREATE TABLE "`+name+`" (v INTEGER)`)
	}
	if _, err := Compare(ctx, orch, Options{IDs: []string{"sweeps"}}); err != nil {
		t.Fatal(err)
	}
	if n := runTables(t, orch); n != 1 {
		t.Fatalf("%d run tables left, want only the run in progress", n)
	}
	var left int
	if err := orch.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, inFlight).Scan(&left); err != nil || left != 1 {
		t.Fatalf("the run in progress was dropped (%v)", err)
	}
	var stored int
	if err := orch.QueryRow(`SELECT COUNT(*) FROM live_vs_backtest_winrate WHERE strategy_id = 'stored'`).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("the stored backtest rates were touched (%v)", err)
	}
}

// Asked the same question within MaxAge, Compare answers from this process's memory and does not
// calculate again; with no MaxAge, or a different question, it calculates.
func TestCompareAnswersFromMemoryWithinMaxAge(t *testing.T) {
	ctx := context.Background()
	orch := open(t, "orch.db")
	if err := live_vs_backtest_trades.EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(orch); err != nil {
		t.Fatal(err)
	}
	addLive(t, orch, "remembered", 12, 6, 6)
	opt := Options{IDs: []string{"remembered"}, MaxAge: time.Minute}
	first, err := Compare(ctx, orch, opt)
	if err != nil || byID(first)["remembered"].LiveDecisions != 12 {
		t.Fatalf("first answer: %v %v", first, err)
	}
	// Change the data underneath: only a new calculation could notice.
	mustExec(t, orch, `DELETE FROM manage_exit`)
	mustExec(t, orch, `DELETE FROM live_vs_backtest_trade`)
	again, err := Compare(ctx, orch, opt)
	if err != nil || byID(again)["remembered"].LiveDecisions != 12 {
		t.Fatalf("a repeat within MaxAge recalculated: %v %v", again, err)
	}
	// A caller that changes the rows it was given does not change what the next caller gets.
	again[0].Strategy = "changed by the caller"
	third, err := Compare(ctx, orch, opt)
	if err != nil || byID(third)["remembered"].LiveDecisions != 12 {
		t.Fatalf("the remembered rows were changed through a caller's copy: %v %v", third, err)
	}
	fresh, err := Compare(ctx, orch, Options{IDs: []string{"remembered"}})
	if err != nil || byID(fresh)["remembered"].LiveDecisions != 0 {
		t.Fatalf("a call with no MaxAge did not recalculate: %v %v", fresh, err)
	}
}

// A remembered answer keeps the database it was asked of reachable, so the address in the cache key
// cannot be handed to a later database (a freed database's address can be reused, which once let one
// test's answer leak into another's).
func TestRememberedAnswerHoldsItsDatabase(t *testing.T) {
	ctx := context.Background()
	orch := open(t, "orch.db")
	opt := Options{IDs: []string{"holds-its-database"}, MaxAge: time.Minute}
	if _, err := Compare(ctx, orch, opt); err != nil {
		t.Fatal(err)
	}
	results.Lock()
	entry, ok := results.m[cacheKey(orch, "compare:"+compareKey(opt))]
	results.Unlock()
	if !ok || entry.db != orch {
		t.Fatalf("the remembered answer does not hold its database: %+v", entry)
	}
}
