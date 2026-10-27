package exit_market

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
	_ "modernc.org/sqlite"
)

func position(sym, asset string, long, short float64) trader.Position {
	return trader.Position{LongQuantity: long, ShortQuantity: short, Instrument: trader.PositionInstrument{Symbol: sym, AssetType: asset}}
}

func account(positions ...trader.Position) *trader.ResolvedAccount {
	return &trader.ResolvedAccount{AccountNumber: "74234603", HashValue: "HASH", Details: trader.SecuritiesAccount{Positions: positions}}
}

func orders(t *testing.T, raw string) []schwab.Order {
	t.Helper()
	var o []schwab.Order
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// A filled bracket (its exits still working), a working entry for another symbol, and
// history that must not be touched.
const brokerOrders = `[
 {"orderId":1001,"status":"FILLED","orderStrategyType":"TRIGGER","orderLegCollection":[{"instruction":"BUY","instrument":{"symbol":"TSLL"}}],
  "childOrderStrategies":[{"orderStrategyType":"OCO","childOrderStrategies":[
   {"orderId":1002,"status":"WORKING","orderType":"LIMIT","orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]},
   {"orderId":1003,"status":"WORKING","orderType":"STOP","orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TSLL"}}]}]}]},
 {"orderId":2001,"status":"WORKING","orderType":"LIMIT","orderLegCollection":[{"instruction":"BUY","instrument":{"symbol":"MARA"}}]},
 {"orderId":3001,"status":"CANCELED","orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"TECL"}}]},
 {"orderId":3002,"status":"FILLED","orderLegCollection":[{"instruction":"SELL","instrument":{"symbol":"MARA"}}]}]`

func TestPlanCancelsEveryWorkingOrderAndSellsEveryLongPositionInWholeShares(t *testing.T) {
	acc := account(
		position("TSLL", "EQUITY", 1, 0),
		position("tecl", "COLLECTIVE_INVESTMENT", 83, 0),  // an ETF, lower case in the data
		position("MARA", "EQUITY", 113.5, 0),              // a fractional remainder
		position("VOO", "EQUITY", 0.4, 0),                 // only a fraction
		position("SQQQ", "EQUITY", 0, 10),                 // short
		position("AAPL  260918C00200000", "OPTION", 2, 0), // an option
		position("SWVXX", "MUTUAL_FUND", 500, 0),
		position("FLAT", "EQUITY", 0, 0), // nothing held
	)
	p := Plan(acc, orders(t, brokerOrders))

	var cancelIDs []int64
	for _, o := range p.Cancel {
		cancelIDs = append(cancelIDs, *o.OrderID)
	}
	if len(cancelIDs) != 3 || cancelIDs[0] != 1002 || cancelIDs[1] != 1003 || cancelIDs[2] != 2001 {
		t.Errorf("cancel = %v, want the two bracket exit legs and the working MARA buy (not filled/canceled history)", cancelIDs)
	}
	want := map[string]float64{"TSLL": 1, "TECL": 83, "MARA": 113}
	if len(p.Sells) != len(want) {
		t.Fatalf("sells = %+v", p.Sells)
	}
	for _, s := range p.Sells {
		if want[s.Symbol] != s.Qty {
			t.Errorf("sell %s qty %.0f, want %.0f", s.Symbol, s.Qty, want[s.Symbol])
		}
	}
	reasons := map[string]string{}
	for _, sk := range p.Skipped {
		reasons[sk.Symbol] += sk.Reason + "|"
	}
	for sym, frag := range map[string]string{"MARA": "fractional remainder", "VOO": "fractional", "SQQQ": "short", "AAPL  260918C00200000": "OPTION", "SWVXX": "MUTUAL_FUND"} {
		if !strings.Contains(reasons[sym], frag) {
			t.Errorf("%s should be skipped with %q, got %q", sym, frag, reasons[sym])
		}
	}
	if _, has := reasons["FLAT"]; has {
		t.Error("a zero position is not a skip, it is nothing")
	}
}

// A dry run reports the plan and touches nothing: no broker client is even needed.
func TestDryRunReportsThePlanWithoutABrokerOrADatabase(t *testing.T) {
	var out bytes.Buffer
	res, err := Run(context.Background(), &out, nil, nil, []*trader.ResolvedAccount{account(position("TSLL", "EQUITY", 1, 0), position("SQQQ", "EQUITY", 0, 5))}, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sold != 1 || res.Skipped != 1 || res.Failed != 0 {
		t.Errorf("result = %+v", res)
	}
	got := out.String()
	for _, want := range []string{"DRY RUN", "WOULD_SELL", "TSLL", "SKIPPED", "SQQQ"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, " SOLD ") {
		t.Error("a dry run must never report SOLD")
	}
}

func TestLiveRunWithoutABrokerClientRefuses(t *testing.T) {
	_, err := Run(context.Background(), &bytes.Buffer{}, nil, nil, []*trader.ResolvedAccount{account(position("TSLL", "EQUITY", 1, 0))}, Options{})
	if err == nil || !strings.Contains(err.Error(), "no live broker client") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRefusesWhileAnotherRunIsInProgress(t *testing.T) {
	running.Lock()
	defer running.Unlock()
	var out bytes.Buffer
	if _, err := Run(context.Background(), &out, nil, nil, nil, Options{DryRun: true}); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if out.Len() != 0 {
		t.Error("a refused run prints nothing")
	}
}

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, ensure := range []func(*sql.DB) error{manage_exits.EnsureSchema, stage_orders.EnsureSchema, job_runs.EnsureSchema} {
		if err := ensure(db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// After a liquidation nothing may be left that the pipeline would act on: the open
// trade is closed, the pending entry is cancelled, the staged order is rejected.
func TestSettleLeavesNothingForTheNextCronToRebuy(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	now := time.Date(2026, 9, 24, 15, 0, 0, 0, time.UTC)
	open := manage_exits.OpenTradeParams{AccountNumber: "74234603", Symbol: "TSLL", StrategyName: "tsll-daily-one-share", EntryOrderID: "1001",
		EntryPrice: 10.30, TargetProfitPrice: 10.82, Quantity: 1, EntryTime: now.AddDate(0, 0, -1), FilledAt: now.AddDate(0, 0, -1), MaxTradingDays: 1}
	if _, err := manage_exits.Open(db, open); err != nil {
		t.Fatal(err)
	}
	pend := open
	pend.Symbol, pend.EntryOrderID, pend.FilledAt = "MARA", "2001", time.Time{}
	if _, err := manage_exits.RegisterPending(db, pend); err != nil {
		t.Fatal(err)
	}
	other := open // a different account must be left alone
	other.AccountNumber, other.EntryOrderID = "25474194", "9001"
	if _, err := manage_exits.Open(db, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO stage_order (account_number, strategy_name, symbol, instruction, quantity, entry_price, take_profit_price, stop_loss_price, max_trading_days, status, staged_at)
		VALUES ('74234603','tsll-daily-one-share','TSLL','BUY',1,10.30,10.82,8.24,1,'PENDING','2026-09-23 22:15:00')`); err != nil {
		t.Fatal(err)
	}

	lines := Settle(db, "74234603", map[string]float64{"TSLL": 10.19}, now)
	if len(lines) != 3 {
		t.Errorf("lines = %v, want one per change", lines)
	}
	q := func(sql string, args ...any) string {
		var s string
		if err := db.QueryRow(sql, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return s
	}
	if got := q(`SELECT status || '/' || exit_reason || '/' || exit_price FROM manage_exit WHERE entry_order_id = '1001'`); got != "CLOSED_MANUAL/EXIT_MARKET/10.19" {
		t.Errorf("open trade = %s", got)
	}
	if got := q(`SELECT status FROM manage_exit WHERE entry_order_id = '2001'`); got != "CANCELLED" {
		t.Errorf("pending entry = %s", got)
	}
	if got := q(`SELECT status FROM stage_order WHERE symbol = 'TSLL'`); got != "REJECTED" {
		t.Errorf("staged order = %s", got)
	}
	if got := q(`SELECT status FROM manage_exit WHERE account_number = '25474194'`); got != "OPEN" {
		t.Errorf("another account's trade was touched: %s", got)
	}
}

func TestSettleFallsBackToTheEntryPriceWhenThereIsNoQuote(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	now := time.Now().UTC()
	if _, err := manage_exits.Open(db, manage_exits.OpenTradeParams{AccountNumber: "74234603", Symbol: "TSLL", StrategyName: "s", EntryOrderID: "1",
		EntryPrice: 10.30, TargetProfitPrice: 11, Quantity: 1, EntryTime: now, FilledAt: now, MaxTradingDays: 1}); err != nil {
		t.Fatal(err)
	}
	Settle(db, "74234603", map[string]float64{"TSLL": 0}, now)
	var price float64
	_ = db.QueryRow(`SELECT exit_price FROM manage_exit`).Scan(&price)
	if price != 10.30 {
		t.Errorf("exit price = %v, want the entry price 10.30", price)
	}
}
