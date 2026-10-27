package strategy_status

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
)

const (
	legTSLL = `"orderLegCollection":[{"instruction":"%s","quantity":1,"instrument":{"symbol":"TSLL"}}]`
	// Entry 1008217638981 filled with its bracket resting.
	bracketWorking = `[{"orderId":1008217638981,"status":"FILLED","closeTime":"2026-10-08T13:30:00+0000","orderStrategyType":"TRIGGER",
 "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
 "childOrderStrategies":[{"orderStrategyType":"OCO","status":"WORKING","childOrderStrategies":[
  {"orderId":11,"status":"WORKING","orderType":"LIMIT","price":10.66,"orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
  {"orderId":12,"status":"WORKING","orderType":"STOP","stopPrice":8.12,"orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]}]`
	// This morning: both legs cancelled at 9:35 and a separate market sell filled.
	bracketCancelledThenSold = `[{"orderId":1008217638981,"status":"FILLED","closeTime":"2026-10-08T13:30:00+0000","orderStrategyType":"TRIGGER",
 "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
 "childOrderStrategies":[{"orderStrategyType":"OCO","status":"CANCELED","childOrderStrategies":[
  {"orderId":11,"status":"CANCELED","orderType":"LIMIT","price":10.66,"orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
  {"orderId":12,"status":"CANCELED","orderType":"STOP","stopPrice":8.12,"orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]},
 {"orderId":99,"status":"FILLED","orderType":"MARKET","closeTime":"2026-10-08T13:35:00+0000","orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]`
	// The exit leg itself filled.
	bracketTakeProfitFilled = `[{"orderId":1008217638981,"status":"FILLED","closeTime":"2026-10-08T13:30:00+0000","orderStrategyType":"TRIGGER",
 "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}],
 "childOrderStrategies":[{"orderStrategyType":"OCO","status":"FILLED","childOrderStrategies":[
  {"orderId":11,"status":"FILLED","orderType":"LIMIT","price":10.66,"closeTime":"2026-10-08T14:00:00+0000","orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]},
  {"orderId":12,"status":"CANCELED","orderType":"STOP","stopPrice":8.12,"orderLegCollection":[{"instruction":"SELL","quantity":1,"instrument":{"symbol":"TSLL"}}]}]}]}]`
)

func liveFor(t *testing.T, held float64, orders string) *Live {
	t.Helper()
	var o []schwab.Order
	if err := json.Unmarshal([]byte(orders), &o); err != nil {
		t.Fatal(err)
	}
	l := &Live{FetchedAt: time.Now(), Accounts: map[string]bool{"25474194": true}, Held: map[string]float64{}, Orders: map[string][]schwab.Order{"25474194": o}}
	if held > 0 {
		l.Held["25474194|TSLL"] = held
	}
	return l
}

func TestLiveHealthyPositionShowsItsRestingExits(t *testing.T) {
	db := newDB(t)
	scanRun(t, db, job_runs.JobRunOK, "as_of=2026-10-07 next=2026-10-08 members=1 livescan_signals=1 buy_signals=1", et(2026, 10, 7, 18, 0))
	openTrade(t, db, "1008217638981", et(2026, 10, 8, 9, 30))
	s, err := Build(db, strat, strat, et(2026, 10, 8, 10, 0), 0, liveFor(t, 1, bracketWorking))
	if err != nil {
		t.Fatal(err)
	}
	p := s.Positions[0]
	if s.BrokerSource != "live" || p.BrokerEntryStatus != "FILLED" || p.RestingTakeProfit == nil || *p.RestingTakeProfit != 10.66 ||
		p.RestingStop == nil || *p.RestingStop != 8.12 || p.ConfirmedAtBroker == nil || !*p.ConfirmedAtBroker {
		t.Fatalf("position = %+v source %s", p, s.BrokerSource)
	}
	if s.Verdict != VerdictOK || len(s.BrokerWorking) != 2 {
		t.Errorf("verdict %s problems %+v working %+v", s.Verdict, s.Problems, s.BrokerWorking)
	}
}

// This morning: the row is OPEN, the bracket was cancelled and the share sold by a
// separate market order. The live read must say the broker holds nothing.
func TestLiveShowsAnOpenRowTheBrokerNoLongerHolds(t *testing.T) {
	db := newDB(t)
	openTrade(t, db, "1008217638981", et(2026, 10, 8, 9, 30))
	s, _ := Build(db, strat, strat, et(2026, 10, 8, 10, 0), 0, liveFor(t, 0, bracketCancelledThenSold))
	if s.Verdict != VerdictProblem || !problems(s, "position", "broker read shows 0 held") {
		t.Fatalf("verdict %s problems %+v", s.Verdict, s.Problems)
	}
	if len(s.BrokerWorking) != 0 {
		t.Errorf("cancelled legs listed as working: %+v", s.BrokerWorking)
	}
}

func TestLiveHeldButUnprotectedAndStaleRows(t *testing.T) {
	db := newDB(t)
	openTrade(t, db, "1008217638981", et(2026, 10, 8, 9, 30))

	s, _ := Build(db, strat, strat, et(2026, 10, 8, 10, 0), 0, liveFor(t, 1, bracketCancelledThenSold))
	if !problems(s, "position", "no resting take-profit or stop") {
		t.Errorf("held with cancelled exits is unprotected: %+v", s.Problems)
	}

	s, _ = Build(db, strat, strat, et(2026, 10, 8, 15, 0), 0, liveFor(t, 1, bracketTakeProfitFilled))
	if !problems(s, "position", "exit order 11 filled: the row is stale") || s.Positions[0].FilledExitOrderID != "11" {
		t.Errorf("filled exit leg: %+v", s.Problems)
	}
}

func TestLiveUntrackedSharesAndUnreadAccounts(t *testing.T) {
	db := newDB(t)
	openTrade(t, db, "1008217638981", et(2026, 10, 8, 9, 30))
	s, _ := Build(db, strat, strat, et(2026, 10, 8, 10, 0), 0, liveFor(t, 3, bracketWorking))
	if len(s.UntrackedShares) != 1 || s.UntrackedShares[0].Held != 3 || s.UntrackedShares[0].Tracked != 1 {
		t.Fatalf("untracked = %+v", s.UntrackedShares)
	}

	// An account whose read failed is unknown, never "holds nothing".
	l := liveFor(t, 0, bracketWorking)
	l.Accounts = map[string]bool{}
	l.Errors = []string{"could not read live orders for account 25474194: timeout"}
	s, _ = Build(db, strat, strat, et(2026, 10, 8, 10, 0), 0, l)
	if s.BrokerSource == "live" || problems(s, "position", "broker read shows") || !problems(s, "broker", "timeout") {
		t.Errorf("source %s problems %+v", s.BrokerSource, s.Problems)
	}
}

func TestLiveStagedOrderBrokerStatus(t *testing.T) {
	db := newDB(t)
	now := et(2026, 10, 8, 10, 0)
	mustExec(t, db, `INSERT INTO stage_order (account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price, status, order_id, staged_at)
		VALUES ('25474194', ?, 'TSLL', 1, 10.15, 10.66, 8.12, 'SUBMITTED', '5555', ?)`, strat, now.UTC())
	orders := `[{"orderId":5555,"status":"CANCELED",` + `"orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TSLL"}}]}]`
	s, _ := Build(db, strat, strat, now, 0, liveFor(t, 0, orders))
	if s.Staged[0].BrokerStatus != "CANCELED" || !problems(s, "submit", "never filled") {
		t.Errorf("staged = %+v problems %+v", s.Staged, s.Problems)
	}
}
