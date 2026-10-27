package manage_exits

import (
	"strings"
	"testing"
)

const (
	filledLimitSell = `[{"orderId":3001,"status":"FILLED","orderType":"LIMIT","price":10.69,"closeTime":"2026-09-25T13:30:05+0000",
 "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]`
	filledMarketSell = `[{"orderId":3002,"status":"FILLED","orderType":"MARKET","closeTime":"2026-09-25T14:07:00+0000",
 "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]`
	sellBeforeEntry = `[{"orderId":3003,"status":"FILLED","orderType":"LIMIT","price":11,"closeTime":"2026-09-20T13:30:05+0000",
 "orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]`
)

func openTSLL(t *testing.T) (Trade, func()) {
	t.Helper()
	db := newDB(t)
	fill := et(2026, 9, 24, 9, 30)
	if _, err := Open(db, params(fill, fill)); err != nil {
		t.Fatal(err)
	}
	tr := onlyTrade(t, db, TradeStatusOpen)
	return tr, func() { db.Close() }
}

func TestSettleOrphanClosesFromAFilledTakeProfitLimit(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	fill := et(2026, 9, 24, 9, 30)
	if _, err := Open(db, params(fill, fill)); err != nil {
		t.Fatal(err)
	}
	tr := onlyTrade(t, db, TradeStatusOpen)
	orders := mustOrders(t, filledLimitSell)

	sell := settlingSell(orders, tr, map[int64]bool{})
	if sell == nil {
		t.Fatal("no settling sell found")
	}
	res, row := &Result{}, &Row{}
	if !settleOrphan(db, tr, sell, false, res, row) {
		t.Fatalf("not settled: %+v", row)
	}
	got := onlyTrade(t, db, TradeStatusClosedProfit)
	if got.ExitPrice != 10.69 || got.ExitReason != "PROFIT_TAKER_FILLED" || got.ExitReasonCode != ReasonProfitTarget {
		t.Errorf("closed row = %+v", got)
	}
	var exitTime string
	if err := db.QueryRow(`SELECT exit_time FROM manage_exit WHERE id = ?`, tr.ID).Scan(&exitTime); err != nil || exitTime == "" {
		t.Errorf("exit_time = %q (err %v): want the broker's sell close time", exitTime, err)
	}
	if res.SettledNoPosition != 1 || row.Status != StatusSettledLate || !strings.Contains(row.Detail, "NOT A NEW TRADE") || !strings.Contains(row.Detail, "2026-09-25") {
		t.Errorf("result %+v row %+v", res, row)
	}
}

func TestSettleOrphanMarketSellIsManual(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	fill := et(2026, 9, 24, 9, 30)
	if _, err := Open(db, params(fill, fill)); err != nil {
		t.Fatal(err)
	}
	tr := onlyTrade(t, db, TradeStatusOpen)
	sell := settlingSell(mustOrders(t, filledMarketSell), tr, map[int64]bool{})
	if sell == nil || !settleOrphan(db, tr, sell, false, &Result{}, &Row{}) {
		t.Fatal("market sell not settled")
	}
	got := onlyTrade(t, db, TradeStatusClosedManual)
	if got.ExitReasonCode != ReasonManual || got.ExitPrice != tr.EntryPrice {
		t.Errorf("closed row = %+v", got)
	}
}

func TestSettlingSellNeedsRealEvidence(t *testing.T) {
	tr, done := openTSLL(t)
	defer done()
	if s := settlingSell(nil, tr, map[int64]bool{}); s != nil {
		t.Errorf("no orders: got %+v", s)
	}
	if s := settlingSell(mustOrders(t, sellBeforeEntry), tr, map[int64]bool{}); s != nil {
		t.Errorf("a sell before the entry fill must not settle the row: got %+v", s)
	}
	if s := settlingSell(mustOrders(t, filledLimitSell), tr, map[int64]bool{3001: true}); s != nil {
		t.Errorf("a sell already claimed by another row must not settle this one: got %+v", s)
	}
}

func TestSettleOrphanDryRunWritesNothing(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	fill := et(2026, 9, 24, 9, 30)
	if _, err := Open(db, params(fill, fill)); err != nil {
		t.Fatal(err)
	}
	tr := onlyTrade(t, db, TradeStatusOpen)
	sell := settlingSell(mustOrders(t, filledLimitSell), tr, map[int64]bool{})
	row := &Row{}
	if !settleOrphan(db, tr, sell, true, &Result{}, row) || row.Status != "SIMULATED" {
		t.Fatalf("row = %+v", row)
	}
	onlyTrade(t, db, TradeStatusOpen)
}
