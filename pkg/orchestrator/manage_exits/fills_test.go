package manage_exits

import (
	"database/sql"
	"math"
	"strings"
	"testing"
)

// The AMUU case of 2026-10-14/15 as Schwab listed it: a limit buy at 271.76 that filled at 256.69,
// and a STOP sell with trigger 217.41 that executed lower. The row must be measured from the fills.
const (
	amuuEntry = `{"orderId":1008284115171,"status":"FILLED","orderType":"LIMIT","price":271.76,"quantity":45,
 "closeTime":"2026-10-14T13:30:13+0000",
 "orderLegCollection":[{"instruction":"BUY","quantity":45,"instrument":{"symbol":"AMUU"}}],
 "orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":256.69,"quantity":45}]}]}`
	amuuStop = `{"orderId":1008290001,"status":"FILLED","orderType":"STOP","stopPrice":217.41,"quantity":45,
 "closeTime":"2026-10-15T13:30:04+0000",
 "orderLegCollection":[{"instruction":"SELL","quantity":45,"instrument":{"symbol":"AMUU"}}],
 "orderActivityCollection":[{"activityType":"EXECUTION","executionLegs":[{"price":214.10,"quantity":30},{"price":211.60,"quantity":15}]}]}`
	amuuStopFill = (214.10*30 + 211.60*15) / 45
)

func openAMUU(t *testing.T) (Trade, *sql.DB, func()) {
	t.Helper()
	db := newDB(t)
	fill := et(2026, 10, 14, 9, 30)
	p := params(fill, fill)
	p.AccountNumber, p.Symbol, p.StrategyName, p.EntryOrderID = "75124906", "AMUU", "markov_model_amuu", "1008284115171"
	p.EntryPrice, p.Quantity, p.TargetProfitPrice, p.StopLossPrice, p.MaxTradingDays = 271.76, 45, 285.35, 217.41, 15
	if _, err := Open(db, p); err != nil {
		t.Fatal(err)
	}
	return onlyTrade(t, db, TradeStatusOpen), db, func() { db.Close() }
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestSettledStopLossIsMeasuredFromTheFillsNotTheLimitAndTrigger(t *testing.T) {
	tr, db, done := openAMUU(t)
	defer done()
	orders := mustOrders(t, "["+amuuEntry+","+amuuStop+"]")

	recordEntryFill(db, orders, &tr, false)
	if tr.EntryFillPrice != 256.69 {
		t.Fatalf("entry fill on the trade = %v, want 256.69", tr.EntryFillPrice)
	}
	sell := settlingSell(orders, tr, map[int64]bool{})
	if sell == nil {
		t.Fatal("no settling sell")
	}
	row, res := &Row{}, &Result{}
	if !settleOrphan(db, tr, sell, false, res, row) {
		t.Fatalf("not settled: %+v", row)
	}
	got := onlyTrade(t, db, TradeStatusClosedStopLoss)
	if got.EntryFillPrice != 256.69 || got.EntryPrice != 271.76 {
		t.Errorf("entry fill %v limit %v", got.EntryFillPrice, got.EntryPrice)
	}
	if !near(got.ExitFillPrice, amuuStopFill) || !near(got.ExitPrice, amuuStopFill) {
		t.Errorf("exit price %v fill %v, want the fill %v (the stop trigger was 217.41)", got.ExitPrice, got.ExitFillPrice, amuuStopFill)
	}
	wantPnl := (amuuStopFill - 256.69) * 45
	wantPct := (amuuStopFill - 256.69) / 256.69 * 100
	if !near(got.PnL, wantPnl) || !near(got.PnLPct, wantPct) {
		t.Errorf("pnl %.4f (%.4f%%), want %.4f (%.4f%%): measured from the entry fill and the exit fill", got.PnL, got.PnLPct, wantPnl, wantPct)
	}
	if got.ExitReason != "STOP_LOSS_FILLED" || got.ExitReasonCode != ReasonStopLoss || got.ExitOrderID != "1008290001" {
		t.Errorf("reason %q code %q exit order %q", got.ExitReason, got.ExitReasonCode, got.ExitOrderID)
	}
	if !strings.Contains(row.Detail, "broker's fill price") {
		t.Errorf("detail does not say the price is the fill: %s", row.Detail)
	}
}

// A sell whose list entry shows no execution is still recorded, and says its price is the trigger.
func TestSettledStopWithNoExecutionLegsFallsBackToTheTriggerAndSaysSo(t *testing.T) {
	tr, db, done := openAMUU(t)
	defer done()
	stop := strings.Replace(amuuStop, `"orderActivityCollection"`, `"ignored"`, 1)
	orders := mustOrders(t, "["+amuuEntry+","+stop+"]")
	recordEntryFill(db, orders, &tr, false)
	row := &Row{}
	if !settleOrphan(db, tr, settlingSell(orders, tr, map[int64]bool{}), false, &Result{}, row) {
		t.Fatal("not settled")
	}
	got := onlyTrade(t, db, TradeStatusClosedStopLoss)
	if got.ExitPrice != 217.41 || got.ExitFillPrice != 0 {
		t.Errorf("exit price %v fill %v, want the trigger 217.41 and no fill", got.ExitPrice, got.ExitFillPrice)
	}
	if !strings.Contains(row.Detail, "not the fill") {
		t.Errorf("detail should say the price is not the fill: %s", row.Detail)
	}
}

// Rows closed before fills were read (the three AMUU rows of 2026-10-15) are corrected from the
// broker's orders, once, and a dry run changes nothing.
func TestBackfillCorrectsRowsClosedFromTheLimitAndTrigger(t *testing.T) {
	tr, db, done := openAMUU(t)
	defer done()
	// Close it the way the system did before: from the stop's trigger, against the limit.
	if err := CloseActiveTrade(db, tr.ID, TradeStatusClosedStopLoss, "STOP_LOSS_FILLED", 217.41, et(2026, 10, 15, 9, 30)); err != nil {
		t.Fatal(err)
	}
	before := onlyTrade(t, db, TradeStatusClosedStopLoss)
	if !near(before.PnLPct, (217.41-271.76)/271.76*100) || before.EntryFillPrice != 0 {
		t.Fatalf("row before = %+v", before)
	}
	orders := mustOrders(t, "["+amuuEntry+","+amuuStop+"]")

	if lines := BackfillFills(db, "75124906", orders, true); len(lines) != 1 || !strings.Contains(lines[0], "$256.69") {
		t.Errorf("dry run lines = %v", lines)
	}
	if after := onlyTrade(t, db, TradeStatusClosedStopLoss); after.EntryFillPrice != 0 || after.PnL != before.PnL {
		t.Error("a dry run changed the row")
	}

	if lines := BackfillFills(db, "75124906", orders, false); len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	got := onlyTrade(t, db, TradeStatusClosedStopLoss)
	wantPct := (amuuStopFill - 256.69) / 256.69 * 100
	if got.EntryFillPrice != 256.69 || !near(got.ExitFillPrice, amuuStopFill) || !near(got.PnLPct, wantPct) || !near(got.PnL, (amuuStopFill-256.69)*45) {
		t.Errorf("row after = entry fill %v exit fill %v pnl %v (%.4f%%), want %.4f%%", got.EntryFillPrice, got.ExitFillPrice, got.PnL, got.PnLPct, wantPct)
	}
	if got.PnLPct <= -19 || got.PnLPct >= -10 {
		t.Errorf("loss %.2f%% should be the true ~-16.8%%, not the -20%% measured from the limit", got.PnLPct)
	}
	// Once corrected, a second pass has nothing to do.
	if lines := BackfillFills(db, "75124906", orders, false); len(lines) != 0 {
		t.Errorf("second pass changed rows again: %v", lines)
	}
	// An account with no matching orders is left alone.
	if lines := BackfillFills(db, "00000000", orders, false); len(lines) != 0 {
		t.Errorf("another account's rows were touched: %v", lines)
	}
}

func TestCloseActiveTradeMeasuresFromTheEntryFillWhenKnown(t *testing.T) {
	tr, db, done := openAMUU(t)
	defer done()
	if err := SetEntryFillPrice(db, tr.ID, 256.69); err != nil {
		t.Fatal(err)
	}
	if err := CloseActiveTrade(db, tr.ID, TradeStatusClosedProfit, "PROFIT_TAKER_FILLED", 285.35, et(2026, 10, 16, 10, 0)); err != nil {
		t.Fatal(err)
	}
	got := onlyTrade(t, db, TradeStatusClosedProfit)
	if !near(got.PnL, (285.35-256.69)*45) {
		t.Errorf("pnl %v, want it measured from the 256.69 fill, not the 271.76 limit", got.PnL)
	}
}

func TestSetEntryFillPriceIgnoresZero(t *testing.T) {
	tr, db, done := openAMUU(t)
	defer done()
	if err := SetEntryFillPrice(db, tr.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got := onlyTrade(t, db, TradeStatusOpen); got.EntryFillPrice != 0 {
		t.Errorf("entry fill = %v after a zero", got.EntryFillPrice)
	}
}
