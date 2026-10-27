package manage_exits

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func et(y int, m time.Month, d, hh, mm int) time.Time {
	loc, _ := time.LoadLocation("America/New_York")
	return time.Date(y, m, d, hh, mm, 0, 0, loc)
}

func params(entry time.Time, filled time.Time) OpenTradeParams {
	return OpenTradeParams{
		AccountNumber: "74234603", Symbol: "TSLL", StrategyName: "tsll-daily-one-share", EntryOrderID: "1001",
		EntryPrice: 10.30, TargetProfitPrice: 10.82, StopLossPrice: 8.24, Quantity: 1,
		EntryTime: entry, FilledAt: filled, MaxTradingDays: 1,
	}
}

func onlyTrade(t *testing.T, db *sql.DB, status string) Trade {
	t.Helper()
	rows, err := db.Query(`SELECT `+tradeColumns+` FROM manage_exit WHERE status = ?`, status)
	if err != nil {
		t.Fatal(err)
	}
	trades, err := scanTrades(rows)
	if err != nil || len(trades) != 1 {
		t.Fatalf("want exactly one %s trade, got %d (err %v)", status, len(trades), err)
	}
	return trades[0]
}

func TestExitDateIsMeasuredFromTheNewYorkFillDate(t *testing.T) {
	cases := []struct {
		name string
		fill time.Time
		days int
		want string
	}{
		{"morning fill, 1 day", et(2026, 9, 24, 9, 31), 1, "2026-09-25"},
		{"after-hours fill is still that New York day (it is already the next UTC day)", et(2026, 9, 23, 20, 0), 1, "2026-09-24"},
		{"Friday fill, 1 day, skips the weekend", et(2026, 9, 25, 15, 59), 1, "2026-09-28"},
		{"a longer window", et(2026, 9, 24, 10, 0), 8, "2026-10-06"},
	}
	for _, c := range cases {
		if got := ExitDateFor(c.fill, c.days); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestExpectedFillDate(t *testing.T) {
	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		{"before the open on a session day", et(2026, 9, 24, 9, 28), "2026-09-24"},
		{"during the session", et(2026, 9, 24, 12, 0), "2026-09-24"},
		{"after the close", et(2026, 9, 24, 17, 10), "2026-09-25"},
		{"Friday evening", et(2026, 9, 25, 17, 10), "2026-09-28"},
		{"Saturday", et(2026, 9, 26, 12, 0), "2026-09-28"},
	}
	for _, c := range cases {
		if got := ExpectedFillDate(c.at).Format(dateLayout); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The flow submit_orders drives: accepted (provisional date) then filled later, on a
// different day than expected (a gap up): the same row is corrected, not duplicated.
func TestProvisionalExitDateIsCorrectedFromTheRealFillDate(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	submitted := et(2026, 9, 24, 17, 10) // Thursday evening: expected to fill Friday
	id, err := RegisterPending(db, params(submitted, time.Time{}))
	if err != nil {
		t.Fatal(err)
	}
	p := onlyTrade(t, db, TradeStatusPendingFill)
	if p.ExitDate != "2026-09-28" || p.ExitDateSource != ExitDateProvisional || p.EntryFilledAt != nil {
		t.Fatalf("pending row = %+v", p)
	}
	if open, _ := GetOpenTrades(db, ""); len(open) != 0 {
		t.Fatal("a PENDING_FILL entry must not look like a held position")
	}
	if again, _ := RegisterPending(db, params(submitted, time.Time{})); again != id {
		t.Error("registering the same entry order twice must not create a second row")
	}

	filled := et(2026, 9, 28, 9, 31) // it actually filled Monday
	id2, err := Open(db, params(filled, filled))
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("the fill must promote the pending row (%d), not create another (%d)", id, id2)
	}
	o := onlyTrade(t, db, TradeStatusOpen)
	if o.ExitDate != "2026-09-29" || o.ExitDateSource != ExitDateFill || o.EntryFilledAt == nil || !o.EntryFilledAt.Equal(filled) {
		t.Errorf("corrected row = %+v, want exit 2026-09-29 from the Monday fill", o)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM manage_exit`).Scan(&n)
	if n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

func TestOpenWithoutABrokerFillTimeStaysProvisional(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	if _, err := Open(db, params(et(2026, 9, 24, 10, 0), time.Time{})); err != nil {
		t.Fatal(err)
	}
	o := onlyTrade(t, db, TradeStatusOpen)
	if o.ExitDateSource != ExitDateProvisional || o.EntryFilledAt != nil || o.ExitDate != "2026-09-25" {
		t.Errorf("row = %+v", o)
	}
}

func TestCancelPendingByOrder(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	if _, err := RegisterPending(db, params(et(2026, 9, 24, 9, 28), time.Time{})); err != nil {
		t.Fatal(err)
	}
	if ok, err := CancelPendingByOrder(db, "74234603", "1001", "entry canceled"); err != nil || !ok {
		t.Fatalf("cancel = %v, %v", ok, err)
	}
	if c := onlyTrade(t, db, TradeStatusCancelled); c.ExitReason != "entry canceled" {
		t.Errorf("reason = %q", c.ExitReason)
	}
	if ok, _ := CancelPendingByOrder(db, "74234603", "1001", "again"); ok {
		t.Error("a second cancel must be a no-op")
	}
}

// A database created before the exit-date columns existed keeps its trades and gains them.
func TestEnsureSchemaUpgradesAnOldManageExitTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE manage_exit (
		id INTEGER PRIMARY KEY AUTOINCREMENT, account_number TEXT NOT NULL, symbol TEXT NOT NULL, strategy_name TEXT NOT NULL,
		entry_order_id TEXT, exit_order_id TEXT, entry_price REAL NOT NULL, target_profit_price REAL NOT NULL, stop_loss_price REAL DEFAULT 0,
		quantity REAL NOT NULL, entry_time DATETIME NOT NULL, max_trading_days INTEGER NOT NULL DEFAULT 8,
		trading_days_held INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'OPEN', exit_reason TEXT, exit_time DATETIME,
		exit_price REAL DEFAULT 0, pnl REAL DEFAULT 0, pnl_pct REAL DEFAULT 0);
		INSERT INTO manage_exit (account_number, symbol, strategy_name, entry_order_id, entry_price, target_profit_price, quantity, entry_time, max_trading_days)
		VALUES ('74234603','MARA','mara_tree','555',13.35,14.02,113,'2026-09-21 13:36:00',1)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	trades, err := GetOpenTrades(db, "")
	if err != nil || len(trades) != 1 {
		t.Fatalf("trades = %d, err %v", len(trades), err)
	}
	if trades[0].Symbol != "MARA" || trades[0].ExitDate != "" || trades[0].ExitDateSource != "" {
		t.Errorf("legacy row = %+v", trades[0])
	}
}

func mustOrders(t *testing.T, raw string) []schwab.Order {
	t.Helper()
	var o []schwab.Order
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// Real Schwab order-list shapes: a bracket whose TRIGGER parent filled, with children.
const filledBracket = `[{"orderId":1001,"status":"FILLED","closeTime":"2026-09-28T13:31:02+0000","orderStrategyType":"TRIGGER",
 "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
 "childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
   {"orderId":1002,"status":"WORKING","orderType":"LIMIT","orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
   {"orderId":1003,"status":"WORKING","orderType":"STOP","orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]}]`

func TestEntryFillReadsTheBrokersFillTime(t *testing.T) {
	orders := mustOrders(t, filledBracket)
	st, at := EntryFill(orders, "1001")
	if st != "FILLED" || !at.Equal(time.Date(2026, 9, 28, 13, 31, 2, 0, time.UTC)) {
		t.Errorf("status %q fill %v", st, at)
	}
	if st, _ := EntryFill(orders, "9999"); st != "" {
		t.Errorf("an unknown order has no status, got %q", st)
	}
	if st, _ := EntryFill(orders, ""); st != "" {
		t.Errorf("no order id, got %q", st)
	}
	// Children are found too.
	if st, _ := EntryFill(orders, "1003"); st != "WORKING" {
		t.Errorf("child order status %q", st)
	}
}

func TestResolvePendingSettlesEntriesFromTheBrokersOrders(t *testing.T) {
	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	now := et(2026, 9, 28, 9, 45)

	t.Run("filled: OPEN with the exit date from the fill", func(t *testing.T) {
		db := newDB(t)
		defer db.Close()
		_, _ = RegisterPending(db, params(et(2026, 9, 24, 17, 10), time.Time{}))
		res := &Result{}
		resolvePending(db, acc, mustOrders(t, filledBracket), now, false, res)
		o := onlyTrade(t, db, TradeStatusOpen)
		if o.ExitDate != "2026-09-29" || o.ExitDateSource != ExitDateFill {
			t.Errorf("row = %+v", o)
		}
		if len(res.Rows) != 1 || res.Rows[0].Status != "ENTRY_FILLED" {
			t.Errorf("rows = %+v", res.Rows)
		}
	})
	t.Run("canceled: no position was ever opened", func(t *testing.T) {
		db := newDB(t)
		defer db.Close()
		_, _ = RegisterPending(db, params(et(2026, 9, 24, 17, 10), time.Time{}))
		res := &Result{}
		resolvePending(db, acc, mustOrders(t, `[{"orderId":1001,"status":"CANCELED"}]`), now, false, res)
		onlyTrade(t, db, TradeStatusCancelled)
		if res.Rows[0].Status != "ENTRY_CANCELED" {
			t.Errorf("rows = %+v", res.Rows)
		}
	})
	t.Run("still working: left pending, date stays provisional", func(t *testing.T) {
		db := newDB(t)
		defer db.Close()
		_, _ = RegisterPending(db, params(et(2026, 9, 24, 17, 10), time.Time{}))
		res := &Result{}
		resolvePending(db, acc, mustOrders(t, `[{"orderId":1001,"status":"WORKING"}]`), now, false, res)
		if p := onlyTrade(t, db, TradeStatusPendingFill); p.ExitDateSource != ExitDateProvisional {
			t.Errorf("row = %+v", p)
		}
		if res.Rows[0].Status != "PENDING_FILL" {
			t.Errorf("rows = %+v", res.Rows)
		}
	})
	t.Run("dry run writes nothing", func(t *testing.T) {
		db := newDB(t)
		defer db.Close()
		_, _ = RegisterPending(db, params(et(2026, 9, 24, 17, 10), time.Time{}))
		resolvePending(db, acc, mustOrders(t, filledBracket), now, true, &Result{})
		onlyTrade(t, db, TradeStatusPendingFill)
	})
}

// manage_exits itself corrects an OPEN row whose date is only an estimate.
func TestCorrectExitDateFixesAnEstimateFromTheEntryFill(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	// Confirmed by a job that noticed the fill late and stamped "now": Tuesday, wrong.
	if _, err := Open(db, params(et(2026, 9, 29, 9, 0), time.Time{})); err != nil {
		t.Fatal(err)
	}
	trades, _ := GetOpenTrades(db, "")
	tr := trades[0]
	if tr.ExitDate != "2026-09-30" {
		t.Fatalf("setup: estimate = %s", tr.ExitDate)
	}
	res := &Result{}
	correctExitDate(db, &tr, mustOrders(t, filledBracket), false, res) // it really filled Monday 09-28
	if tr.ExitDate != "2026-09-29" || tr.ExitDateSource != ExitDateFill {
		t.Errorf("in-memory trade = %s %s", tr.ExitDate, tr.ExitDateSource)
	}
	stored := onlyTrade(t, db, TradeStatusOpen)
	if stored.ExitDate != "2026-09-29" || stored.ExitDateSource != ExitDateFill {
		t.Errorf("stored trade = %+v", stored)
	}

	// A row already from the fill is left alone even if the orders disagree.
	before := stored
	correctExitDate(db, &stored, mustOrders(t, `[{"orderId":1001,"status":"FILLED","closeTime":"2026-10-05T13:31:02+0000"}]`), false, &Result{})
	if stored.ExitDate != before.ExitDate {
		t.Error("a fill-sourced exit date must not be recomputed")
	}
}

func TestCorrectExitDateGivesALegacyRowAProvisionalDate(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, entry_order_id, entry_price, target_profit_price, quantity, entry_time, max_trading_days)
		VALUES ('74234603','MARA','mara_tree','','13.35','14.02',113,'2026-09-21 13:36:00+00:00',1)`); err != nil {
		t.Fatal(err)
	}
	trades, _ := GetOpenTrades(db, "")
	tr := trades[0]
	correctExitDate(db, &tr, nil, false, &Result{})
	if tr.ExitDate != "2026-09-22" || tr.ExitDateSource != ExitDateProvisional {
		t.Errorf("legacy row = %s %s", tr.ExitDate, tr.ExitDateSource)
	}
	if s := onlyTrade(t, db, TradeStatusOpen); s.ExitDate != "2026-09-22" {
		t.Errorf("not persisted: %+v", s)
	}
}

func TestExitDue(t *testing.T) {
	tr := Trade{MaxTradingDays: 1, ExitDate: "2026-09-25"}
	for _, c := range []struct {
		name string
		now  time.Time
		tr   Trade
		want bool
	}{
		{"the day before", et(2026, 9, 24, 15, 50), tr, false},
		{"the exit date, at the morning run", et(2026, 9, 25, 9, 35), tr, true},
		{"after the exit date", et(2026, 9, 28, 9, 35), tr, true},
		{"no holding window", et(2026, 9, 28, 9, 35), Trade{ExitDate: "2026-09-25"}, false},
		{"no exit date yet", et(2026, 9, 28, 9, 35), Trade{MaxTradingDays: 1}, false},
		{"late evening New York is still that day, not the next UTC day", et(2026, 9, 24, 21, 0), tr, false},
	} {
		if got := exitDue(c.tr, c.now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWorkingOrdersSkipsTheFilledParent(t *testing.T) {
	if len(WorkingOrders(mustOrders(t, filledBracket))) != 2 {
		t.Error("the FILLED parent must not count as working; only the limit and stop legs do")
	}
}

// Schwab lists a filled bracket's exits as a WORKING OCO with no legs of its own.
// Cancelling only the two sell legs leaves that parent holding the shares, and the
// market sell is rejected, so the position is still open after its exit date.
func TestExitCancelSetIncludesTheOCOParent(t *testing.T) {
	const live = `[{"orderId":1001,"status":"FILLED","orderStrategyType":"TRIGGER",
 "orderLegCollection":[{"instruction":"BUY","instrument":{"symbol":"TSLL"}}],
 "childOrderStrategies":[{"orderId":5544,"status":"WORKING","orderStrategyType":"OCO","childOrderStrategies":[
   {"orderId":5545,"status":"WORKING","orderType":"LIMIT","orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
   {"orderId":5546,"status":"WORKING","orderType":"STOP","orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}
]}]}]`
	// Run hands ExitAtMarket the flattened list, which is how the broker's order
	// list arrives: the OCO is still a working order with its legs nested in it.
	for _, list := range [][]schwab.Order{mustOrders(t, live), schwab.FlattenOrders(mustOrders(t, live))} {
		var ids []int64
		for _, o := range ordersBlockingSale(list, "TSLL") {
			ids = append(ids, *o.OrderID)
		}
		if len(ids) != 3 || ids[0] != 5544 || ids[1] != 5545 || ids[2] != 5546 {
			t.Fatalf("cancel set = %v, want the OCO parent first, then both sell legs, and not the filled entry", ids)
		}
	}
	if len(ordersBlockingSale(mustOrders(t, live), "MARA")) != 0 {
		t.Error("another symbol's bracket must not be cancelled")
	}
}

func TestPlannedExitDatesPerAccountAndSymbol(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	fill := et(2026, 9, 24, 9, 31)

	// Filled Thursday, 1-day window: due Friday, from the real fill.
	if _, err := Open(db, params(fill, fill)); err != nil {
		t.Fatal(err)
	}
	// The same symbol in another account, a longer window.
	p := params(fill, fill)
	p.AccountNumber, p.EntryOrderID, p.MaxTradingDays = "25474194", "2002", 8
	if _, err := Open(db, p); err != nil {
		t.Fatal(err)
	}
	// Two OPEN trades on one symbol: the earlier date wins.
	dup := params(fill, fill)
	dup.EntryOrderID, dup.MaxTradingDays = "3003", 5
	if _, err := Open(db, dup); err != nil {
		t.Fatal(err)
	}
	// A row from before exit dates existed: the date is derived from entry_time and the window.
	if _, err := db.Exec(`INSERT INTO manage_exit (account_number, symbol, strategy_name, entry_order_id, entry_price, target_profit_price, quantity, entry_time, max_trading_days)
		VALUES ('74234603','mara','mara_tree','555',13.35,14.02,113,'2026-09-21 13:36:00+00:00',1)`); err != nil {
		t.Fatal(err)
	}
	// Not OPEN: no planned exit. A PENDING_FILL entry is not a held position yet.
	pend := params(fill, time.Time{})
	pend.Symbol, pend.EntryOrderID = "PDD", "4004"
	if _, err := RegisterPending(db, pend); err != nil {
		t.Fatal(err)
	}
	closed := params(fill, fill)
	closed.Symbol, closed.EntryOrderID = "VOO", "5005"
	id, _ := Open(db, closed)
	_ = CloseActiveTrade(db, id, TradeStatusClosedManual, "x", 1, fill)

	got, err := PlannedExitDates(db)
	if err != nil {
		t.Fatal(err)
	}
	if d := got["74234603"]["TSLL"]; d != "2026-09-25" {
		t.Errorf("TSLL 74234603 = %q, want 2026-09-25 (the earlier of the two OPEN trades)", d)
	}
	if d := got["25474194"]["TSLL"]; d != "2026-10-06" {
		t.Errorf("TSLL 25474194 = %q, want 2026-10-06", d)
	}
	if d := got["74234603"]["MARA"]; d != "2026-09-22" {
		t.Errorf("a legacy row's date is derived from its entry time: got %q, want 2026-09-22 (symbols are upper-cased)", d)
	}
	for _, sym := range []string{"PDD", "VOO"} {
		if d, ok := got["74234603"][sym]; ok {
			t.Errorf("%s has a planned exit %q but is not an OPEN trade", sym, d)
		}
	}
}
