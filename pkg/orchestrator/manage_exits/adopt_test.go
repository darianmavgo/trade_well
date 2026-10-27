package manage_exits

import (
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

const filledTeclBuy = `[{"orderId":2001,"status":"FILLED","closeTime":"2026-09-24T13:30:01+0000","orderStrategyType":"TRIGGER",
 "orderLegCollection":[{"instruction":"BUY","quantity":1,"instrument":{"symbol":"TECL"}}]}]`

// bundledStrategies resolves real strategies the way the jobs do, from the bundled rows.
func bundledStrategies(t *testing.T, ids ...string) []strategy.Strategy {
	t.Helper()
	t.Setenv("APP_FOLDER", t.TempDir())
	got, err := strategies.ResolveGreenlit(nil, ids)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func held(sym string, qty float64) trader.Position {
	p := trader.Position{LongQuantity: qty}
	p.Instrument.Symbol = sym
	return p
}

// A held position with no manage_exit row (entered from another run's database)
// gets one, with the exit date measured from the broker's fill.
func TestAdoptUntrackedGivesAHeldPositionAnExitDate(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	acc.Details.Positions = []trader.Position{held("TECL", 1), held("XYZ", 5)}
	strats := bundledStrategies(t, "streak-voo-buy-tecl", "markov_model_amd")
	hold := strats[0].DefaultConfig().HoldingWindow
	if hold <= 0 {
		t.Fatalf("streak-voo-buy-tecl has no holding window")
	}

	res := &Result{}
	adoptUntracked(db, acc, strats, mustOrders(t, filledTeclBuy), et(2026, 9, 25, 9, 0), false, res)

	tr := onlyTrade(t, db, TradeStatusOpen)
	if tr.Symbol != "TECL" || tr.StrategyName != "streak-voo-buy-tecl" || tr.ExitDateSource != ExitDateFill || tr.EntryOrderID != "2001" {
		t.Errorf("adopted = %+v", tr)
	}
	if want := ExitDateFor(et(2026, 9, 24, 9, 30), hold); tr.ExitDate != want {
		t.Errorf("exit date %s, want %s", tr.ExitDate, want)
	}
	var adopted, skipped int
	for _, r := range res.Rows {
		switch {
		case r.Symbol == "TECL" && r.Status == "ADOPTED":
			adopted++
		case r.Symbol == "XYZ" && r.Status == "SKIPPED":
			skipped++
		}
	}
	if adopted != 1 || skipped != 1 {
		t.Errorf("rows = %+v", res.Rows)
	}

	// A rerun and a dry run write nothing more.
	adoptUntracked(db, acc, strats, mustOrders(t, filledTeclBuy), et(2026, 9, 25, 9, 5), false, &Result{})
	onlyTrade(t, db, TradeStatusOpen)
}

func TestAdoptUntrackedDryRunWritesNothing(t *testing.T) {
	db := newDB(t)
	defer db.Close()
	acc := &trader.ResolvedAccount{AccountNumber: "74234603"}
	acc.Details.Positions = []trader.Position{held("TECL", 1)}
	res := &Result{}
	adoptUntracked(db, acc, bundledStrategies(t, "streak-voo-buy-tecl"), mustOrders(t, filledTeclBuy), et(2026, 9, 25, 9, 0), true, res)
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM manage_exit`).Scan(&n)
	if n != 0 || len(res.Rows) != 1 || res.Rows[0].Status != "SIMULATED" {
		t.Errorf("rows=%d %+v", n, res.Rows)
	}
}
