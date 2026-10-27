package manage_exits

import (
	"testing"
	"time"
)

func TestOpenStoresTheDecisionAndCloseSetsTheReason(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id, err := Open(db, OpenTradeParams{
		AccountNumber: "1", Symbol: "TSLL", StrategyName: "tsll-daily-one-share",
		StrategyID: "tsll-daily-one-share", AsOfDate: "2026-09-24", NextSession: "2026-09-25",
		SignalID: 7, BuySignalID: 9, StageOrderID: 3,
		EntryPrice: 10, TargetProfitPrice: 10.5, Quantity: 1,
		EntryTime:      time.Date(2026, 9, 25, 14, 0, 0, 0, time.UTC),
		MaxTradingDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var strategy, asOf, next, code string
	var signalID, buyID, stageID int64
	if err := db.QueryRow(`SELECT strategy_id, as_of_date, next_session, signal_id, buy_signal_id, stage_order_id, exit_reason_code FROM manage_exit WHERE id = ?`, id).
		Scan(&strategy, &asOf, &next, &signalID, &buyID, &stageID, &code); err != nil {
		t.Fatal(err)
	}
	if strategy != "tsll-daily-one-share" || asOf != "2026-09-24" || next != "2026-09-25" || signalID != 7 || buyID != 9 || stageID != 3 || code != "" {
		t.Fatalf("stored %s %s %s ids %d %d %d code %q", strategy, asOf, next, signalID, buyID, stageID, code)
	}
	if err := CloseActiveTrade(db, id, TradeStatusClosedTimeExpiry, "WINDOW_EXPIRED_MARKET_SELL", 10.2, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT exit_reason_code FROM manage_exit WHERE id = ?`, id).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != ReasonTimeUp {
		t.Fatalf("close code %q", code)
	}

	pend, err := RegisterPending(db, OpenTradeParams{
		AccountNumber: "1", Symbol: "TSLL", StrategyName: "tsll-daily-one-share",
		EntryOrderID: "55", EntryPrice: 10, TargetProfitPrice: 10.5, Quantity: 1,
		EntryTime: time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC), MaxTradingDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := CancelPending(db, pend, "entry canceled"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT exit_reason_code FROM manage_exit WHERE id = ?`, pend).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != ReasonUnfilled {
		t.Fatalf("cancel code %q", code)
	}
}
