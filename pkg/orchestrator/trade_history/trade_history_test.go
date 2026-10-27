package trade_history_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_winrate"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	. "github.com/darianmavgo/trade_well/pkg/orchestrator/trade_history"
	_ "modernc.org/sqlite"
)

func open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := account_state.MirrorStore().EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := live_vs_backtest_winrate.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// position inserts one manage_exit row. status, the two order ids and the two fills decide whether
// it is a trade that happened.
type position struct {
	strategy, account, symbol, entryDay, status string
	entryOrder, exitOrder                       string
	entryFill, exitFill                         float64
	pnl                                         float64
	closeDay                                    string // exit date, so closing order is explicit
}

func add(t *testing.T, db *sql.DB, p position) {
	t.Helper()
	if p.account == "" {
		p.account = "1"
	}
	if p.symbol == "" {
		p.symbol = "XYZ"
	}
	if p.status == "" {
		p.status = "CLOSED_PROFIT"
	}
	mustExec(t, db, `INSERT INTO manage_exit (account_number, symbol, strategy_name, strategy_id, entry_order_id, exit_order_id,
		entry_price, target_profit_price, quantity, entry_time, status, exit_time, pnl, pnl_pct,
		entry_fill_price, exit_fill_price, filled_qty, exit_reason_code)
		VALUES (?, ?, ?, ?, ?, ?, 10, 11, 1, ?, ?, ?, ?, ?, ?, ?, 1, 'TARGET')`,
		p.account, p.symbol, p.strategy, p.strategy, p.entryOrder, p.exitOrder,
		p.entryDay+" 13:30:00", p.status, p.closeDay+" 15:00:00", p.pnl, p.pnl, p.entryFill, p.exitFill)
}

var seq int

// trade adds a closed position that really traded, a win when pnl > 0, closing on the given day.
func trade(t *testing.T, db *sql.DB, strategy, symbol, day string, pnl float64) {
	t.Helper()
	seq++
	add(t, db, position{strategy: strategy, symbol: symbol, entryDay: day, closeDay: day, pnl: pnl,
		entryOrder: fmt.Sprintf("E%d", seq), exitOrder: fmt.Sprintf("X%d", seq), entryFill: 10, exitFill: 10 + pnl})
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// Only positions that really traded are captured: a cancelled, open, or never-filled row is not a
// trade, nor is one whose entry order the broker mirror says did not fill.
func TestCaptureTakesOnlyTradesThatReallyHappened(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO account_state_order (order_id, account_number, status, fetched_at) VALUES ('E-CANCELLED', '1', 'CANCELED', 'now'), ('E-FILLED', '1', 'FILLED', 'now')`)

	add(t, db, position{strategy: "s", entryDay: "2026-09-01", closeDay: "2026-09-03", pnl: 1, entryOrder: "E-FILLED", exitOrder: "X1", entryFill: 10, exitFill: 11})      // a trade; the mirror agrees
	add(t, db, position{strategy: "s", entryDay: "2026-09-02", closeDay: "2026-09-03", pnl: 1, entryOrder: "E-NOT-FETCHED", exitOrder: "X2", entryFill: 10, exitFill: 11}) // a trade; the mirror has not fetched the entry yet
	add(t, db, position{strategy: "s", entryDay: "2026-09-03", status: "CANCELLED", entryOrder: "E3", entryFill: 0, exitFill: 0})                                          // never filled
	add(t, db, position{strategy: "s", entryDay: "2026-09-04", status: "OPEN", entryOrder: "E4", entryFill: 10, exitFill: 0})                                              // still open
	add(t, db, position{strategy: "s", entryDay: "2026-09-05", closeDay: "2026-09-06", pnl: 1, entryOrder: "E5", exitOrder: "X5", entryFill: 10, exitFill: 0})             // closed, but no exit fill recorded
	add(t, db, position{strategy: "s", entryDay: "2026-09-06", closeDay: "2026-09-07", pnl: 1, entryOrder: "E-CANCELLED", exitOrder: "X6", entryFill: 10, exitFill: 11})   // the mirror says the entry order was cancelled
	add(t, db, position{strategy: "s", entryDay: "2026-09-07", closeDay: "2026-09-08", pnl: 1, entryOrder: "E7", exitOrder: "", entryFill: 10, exitFill: 11})              // no exit order id
	add(t, db, position{strategy: "", entryDay: "2026-09-08", closeDay: "2026-09-09", pnl: 1, entryOrder: "E8", exitOrder: "X8", entryFill: 10, exitFill: 11})             // attributed to nothing

	res, err := Capture(ctx, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.New != 2 || res.Total != 2 {
		t.Fatalf("captured %d, holds %d; want 2 and 2", res.New, res.Total)
	}
	var strategy, entry, exit string
	if err := db.QueryRow(`SELECT strategy_id, entry_order_id, exit_order_id FROM trade_history WHERE entry_order_id = 'E-FILLED'`).Scan(&strategy, &entry, &exit); err != nil {
		t.Fatal(err)
	}
	if strategy != "s" || exit != "X1" {
		t.Fatalf("captured strategy=%q entry=%q exit=%q", strategy, entry, exit)
	}
}

// Capturing again adds only the trades closed since; what is there is left as it was captured.
func TestCaptureAddsOnlyNewTradesOnARerun(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	trade(t, db, "s", "AAA", "2026-09-01", 1)
	trade(t, db, "s", "BBB", "2026-09-02", -1)
	first, err := Capture(ctx, db, false)
	if err != nil || first.New != 2 || first.Total != 2 {
		t.Fatalf("first: %+v %v", first, err)
	}
	mustExec(t, db, `UPDATE trade_history SET pnl = 99 WHERE symbol = 'AAA'`) // a captured row is not rewritten
	again, err := Capture(ctx, db, false)
	if err != nil || again.New != 0 || again.Total != 2 {
		t.Fatalf("rerun: %+v %v", again, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM trade_history WHERE symbol = 'AAA' AND pnl = 99`); n != 1 {
		t.Fatal("a rerun overwrote a captured trade")
	}
	trade(t, db, "s", "CCC", "2026-09-03", 2)
	third, err := Capture(ctx, db, false)
	if err != nil || third.New != 1 || third.Total != 3 {
		t.Fatalf("after one more trade closed: %+v %v", third, err)
	}
}

// A dry run creates nothing and writes nothing: it says the table is missing, or counts what a
// real run would add.
func TestDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	trade(t, db, "s", "AAA", "2026-09-01", 1)
	res, err := Capture(ctx, db, true)
	if err != nil || res.Ready {
		t.Fatalf("dry run before the table exists: %+v %v", res, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'trade_history%'`); n != 0 {
		t.Fatalf("a dry run created %d objects", n)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	res, err = Capture(ctx, db, true)
	if err != nil || !res.Ready || res.New != 1 || res.Total != 0 {
		t.Fatalf("dry run with the table: %+v %v", res, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM trade_history`); n != 0 {
		t.Fatal("a dry run wrote a trade")
	}
}

// The ongoing win rate after each closed decision grows with the trades attributed to a strategy,
// and one decision filled in three accounts counts once.
func TestRunningWinRateGrowsAsTradesAreAttributed(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	trade(t, db, "grows", "AAA", "2026-09-01", 2)  // win
	trade(t, db, "grows", "BBB", "2026-09-02", -1) // loss
	trade(t, db, "grows", "CCC", "2026-09-03", 2)  // win
	// one decision, three accounts, all stopped out: one loss, not three
	for _, acct := range []string{"1", "2", "3"} {
		seq++
		add(t, db, position{strategy: "multi", account: acct, symbol: "SSS", entryDay: "2026-09-04", closeDay: "2026-09-05", status: "CLOSED_STOP_LOSS",
			pnl: -10, entryOrder: fmt.Sprintf("E%d", seq), exitOrder: fmt.Sprintf("X%d", seq), entryFill: 10, exitFill: 9})
	}
	if res, err := Capture(ctx, db, false); err != nil || res.New != 6 {
		t.Fatalf("capture: %+v %v", res, err)
	}

	type step struct {
		n, wins int
		rate    float64
	}
	var got []step
	rows, err := db.Query(`SELECT n, wins, win_rate FROM trade_history_running WHERE strategy_id = 'grows' ORDER BY n`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s step
		if err := rows.Scan(&s.n, &s.wins, &s.rate); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	want := []step{{1, 1, 1}, {2, 1, 0.5}, {3, 2, 2.0 / 3}}
	if len(got) != len(want) {
		t.Fatalf("running rows = %+v, want %d", got, len(want))
	}
	for i := range want {
		if got[i].n != want[i].n || got[i].wins != want[i].wins || got[i].rate < want[i].rate-1e-9 || got[i].rate > want[i].rate+1e-9 {
			t.Fatalf("after trade %d: %+v, want %+v", i+1, got[i], want[i])
		}
	}
	// the interval narrows as evidence accumulates for a steady rate
	var lo, hi float64
	if err := db.QueryRow(`SELECT ci_low, ci_high FROM trade_history_running WHERE strategy_id = 'grows' AND n = 3`).Scan(&lo, &hi); err != nil || lo >= 2.0/3 || hi <= 2.0/3 {
		t.Fatalf("interval at n=3 = %v-%v (%v), want it around 2/3", lo, hi, err)
	}

	var trades, wins int
	var rate float64
	if err := db.QueryRow(`SELECT trades, wins, win_rate FROM trade_history_win_rate WHERE strategy_id = 'multi'`).Scan(&trades, &wins, &rate); err != nil {
		t.Fatal(err)
	}
	if trades != 1 || wins != 0 || rate != 0 {
		t.Fatalf("three fills of one stop loss counted as trades=%d wins=%d rate=%v, want one loss", trades, wins, rate)
	}
	if err := db.QueryRow(`SELECT trades, wins FROM trade_history_win_rate WHERE strategy_id = 'grows'`).Scan(&trades, &wins); err != nil || trades != 3 || wins != 2 {
		t.Fatalf("grows now stands at %d trades, %d wins (%v)", trades, wins, err)
	}

	// another win arrives: the win rate view moves, and the earlier steps are unchanged
	trade(t, db, "grows", "DDD", "2026-09-06", 3)
	if _, err := Capture(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT trades, wins, win_rate FROM trade_history_win_rate WHERE strategy_id = 'grows'`).Scan(&trades, &wins, &rate); err != nil || trades != 4 || wins != 3 || rate != 0.75 {
		t.Fatalf("after a fourth trade: %d trades, %d wins, rate %v (%v)", trades, wins, rate, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM trade_history_running WHERE strategy_id = 'grows'`); n != 4 {
		t.Fatalf("running rows = %d, want 4", n)
	}
}

// live_vs_backtest gives the verdicts the win-rate comparison gives, from the same trades.
func TestLiveVsBacktestAgreesWithTheWinRateComparison(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	live := func(strategy string, n, wins int) {
		for i := 0; i < n; i++ {
			pnl := -1.0
			if i < wins {
				pnl = 2
			}
			trade(t, db, strategy, fmt.Sprintf("S%02d", i), fmt.Sprintf("2026-09-%02d", i+1), pnl)
		}
	}
	live("below", 20, 4)  // 20% against 65%
	live("inline", 12, 7) // 58% against 60%
	live("above", 20, 19) // 95% against 30%
	live("early", 3, 0)   // too few to judge
	live("nobt", 12, 6)   // no backtest row
	backtest := func(id string, is, oos float64) {
		mustExec(t, db, `INSERT INTO live_vs_backtest_winrate (strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate, synced_at) VALUES (?, 100, ?, 40, ?, 'now')`, id, is, oos)
	}
	backtest("below", 0.7, 0.65)
	backtest("inline", 0.6, 0.6)
	backtest("above", 0.5, 0.3)
	backtest("early", 0.6, 0.6)
	backtest("idle", 0.55, 0.57) // a backtest and no live trades yet
	if res, err := Capture(ctx, db, false); err != nil || res.New != 20+12+20+3+12 {
		t.Fatalf("capture: %+v %v", res, err)
	}

	rows, err := LiveVsBacktest(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	view := map[string]Row{}
	for _, r := range rows {
		view[r.Strategy] = r
	}
	wantVerdict := map[string]string{"below": Below, "inline": InLine, "above": Above, "early": TooEarly, "nobt": NoBacktest, "idle": TooEarly}
	for id, want := range wantVerdict {
		if got := view[id].Verdict; got != want {
			t.Errorf("%s: live_vs_backtest verdict = %q, want %q", id, got, want)
		}
	}
	if view["idle"].LiveTrades != 0 || view["idle"].LiveWinRate != nil {
		t.Errorf("a strategy with no live trades shows live data: %+v", view["idle"])
	}

	// the comparison, run over the same data, says the same
	compared, err := live_vs_backtest_winrate.Compare(ctx, db, live_vs_backtest_winrate.Options{IDs: []string{"below", "inline", "above", "early", "nobt", "idle"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range compared {
		v, ok := view[c.Strategy]
		if !ok {
			t.Errorf("%s is in the comparison but not in live_vs_backtest", c.Strategy)
			continue
		}
		if v.Verdict != c.Verdict {
			t.Errorf("%s: view says %s, comparison says %s", c.Strategy, v.Verdict, c.Verdict)
		}
		if (v.LiveWinRate == nil) != (c.LiveWinRate == nil) || v.LiveWinRate != nil && (*v.LiveWinRate-*c.LiveWinRate > 1e-9 || *c.LiveWinRate-*v.LiveWinRate > 1e-9) {
			t.Errorf("%s: live win rate differs between the view and the comparison", c.Strategy)
		}
		if v.LiveTrades != c.LiveDecisions {
			t.Errorf("%s: view has %d live trades, comparison %d decisions", c.Strategy, v.LiveTrades, c.LiveDecisions)
		}
	}
}

// A view whose stored definition differs from this build's is replaced; the table and its rows stay.
func TestEnsureSchemaRestoresAChangedView(t *testing.T) {
	db := open(t)
	trade(t, db, "s", "AAA", "2026-09-01", 1)
	if _, err := Capture(context.Background(), db, false); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `DROP VIEW trade_history_win_rate`)
	mustExec(t, db, `CREATE VIEW trade_history_win_rate AS SELECT 'old' AS strategy_id`)
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	var trades int
	if err := db.QueryRow(`SELECT trades FROM trade_history_win_rate WHERE strategy_id = 's'`).Scan(&trades); err != nil || trades != 1 {
		t.Fatalf("the changed view was not restored: trades=%d (%v)", trades, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM trade_history`); n != 1 {
		t.Fatalf("trade_history rows = %d after EnsureSchema, want 1", n)
	}
}
