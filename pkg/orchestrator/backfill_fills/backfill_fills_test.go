package backfill_fills

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_status"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	_ "modernc.org/sqlite"
)

const orders = `[
 {"orderId":1008284115171,"status":"FILLED","orderType":"LIMIT","price":271.76,"quantity":45,"closeTime":"2026-10-14T13:30:13+0000",
  "orderLegCollection":[{"instruction":"BUY","quantity":45,"instrument":{"symbol":"AMUU"}}],
  "orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":256.69,"quantity":45}]}]},
 {"orderId":1008290001,"status":"FILLED","orderType":"STOP","stopPrice":217.41,"quantity":45,"closeTime":"2026-10-15T13:30:04+0000",
  "orderLegCollection":[{"instruction":"SELL","quantity":45,"instrument":{"symbol":"AMUU"}}],
  "orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":214.10,"quantity":30},{"price":211.60,"quantity":15}]}]}
]`

// closedRow writes the row the system produced before fills were read: measured from the 271.76
// limit and the 217.41 trigger.
func closedRow(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/New_York")
	fill := time.Date(2026, 10, 14, 9, 30, 0, 0, loc)
	id, err := manage_exits.Open(db, manage_exits.OpenTradeParams{AccountNumber: "75124906", Symbol: "AMUU", StrategyName: "markov_model_amuu",
		EntryOrderID: "1008284115171", EntryPrice: 271.76, TargetProfitPrice: 285.35, StopLossPrice: 217.41, Quantity: 45, EntryTime: fill, FilledAt: fill, MaxTradingDays: 15})
	if err != nil {
		t.Fatal(err)
	}
	if err := manage_exits.CloseActiveTrade(db, id, manage_exits.TradeStatusClosedStopLoss, "STOP_LOSS_FILLED", 217.41, time.Date(2026, 10, 15, 9, 30, 4, 0, loc)); err != nil {
		t.Fatal(err)
	}
	return db
}

func live(t *testing.T, errs ...string) *strategy_status.Live {
	t.Helper()
	var o []schwab.Order
	if err := json.Unmarshal([]byte(orders), &o); err != nil {
		t.Fatal(err)
	}
	return &strategy_status.Live{Accounts: map[string]bool{"75124906": true}, Orders: map[string][]schwab.Order{"75124906": o}, Errors: errs}
}

func pct(t *testing.T, db *sql.DB) (entryFill, exitFill, pnlPct float64) {
	t.Helper()
	if err := db.QueryRow(`SELECT entry_fill_price, exit_fill_price, pnl_pct FROM manage_exit`).Scan(&entryFill, &exitFill, &pnlPct); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRunCorrectsAClosedRowFromTheBrokersOrders(t *testing.T) {
	db := closedRow(t)
	defer db.Close()
	if e, x, p := pct(t, db); e != 0 || x != 0 || p > -19.9 {
		t.Fatalf("row before = %v %v %v%%", e, x, p)
	}

	lines, err := Run(db, live(t), true)
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], "account ...4906") || !strings.Contains(lines[0], "$256.69") {
		t.Fatalf("dry run = %v, %v", lines, err)
	}
	if e, _, _ := pct(t, db); e != 0 {
		t.Fatal("a dry run wrote the row")
	}

	if lines, err = Run(db, live(t), false); err != nil || len(lines) != 1 {
		t.Fatalf("run = %v, %v", lines, err)
	}
	e, x, p := pct(t, db)
	if e != 256.69 || x < 213.2 || x > 213.3 || p < -17 || p > -16.5 {
		t.Errorf("after: entry fill %v, exit fill %v, pnl %.2f%%: want 256.69, about 213.27 and about -16.9%%", e, x, p)
	}
	if lines, _ = Run(db, live(t), false); len(lines) != 0 {
		t.Errorf("second run changed rows again: %v", lines)
	}
}

// An account Schwab could not be read for is reported and its rows are not touched.
func TestRunReportsUnreadAccountsAndTouchesNothingForThem(t *testing.T) {
	db := closedRow(t)
	defer db.Close()
	l := &strategy_status.Live{Accounts: map[string]bool{}, Orders: map[string][]schwab.Order{}, Errors: []string{"could not read live orders for account 75124906: timeout"}}
	lines, err := Run(db, l, false)
	if err != nil || len(lines) != 1 || !strings.HasPrefix(lines[0], "not read:") {
		t.Fatalf("lines = %v, err %v", lines, err)
	}
	if e, _, _ := pct(t, db); e != 0 {
		t.Error("a row was changed with no orders to support it")
	}
}
