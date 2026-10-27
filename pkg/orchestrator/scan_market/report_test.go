package scan_market

import (
	"strings"
	"testing"

	backtestmodels "github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
)

func TestResultStrategyScansListsFiredAndNotFired(t *testing.T) {
	res := &Result{
		Signals: []Signal{{StrategyID: "markov_model_amuu", Symbol: "AMUU", Price: 12.5, AllocPct: 0.25, Raw: backtestmodels.Signal{TakeProfit: 13.1, StopLoss: 11.9}}},
		NoBuy:   []NoBuyNote{{StrategyID: "markov_model_mull", Symbols: []string{"MULL"}, Detail: "no buy signal from livescan as of 2026-10-14."}},
	}
	rows := EmailRows(res.StrategyScans())
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	if r := rows[0]; r.Strategy != "markov_model_amuu" || r.Symbol != "AMUU" || r.Signal != ScanBuy || r.Status != "SIGNAL" || !strings.Contains(r.Detail, "1 BUY: AMUU @ 12.5000 TP $13.1000 SL $11.9000 alloc 25%") {
		t.Errorf("buy row = %+v", r)
	}
	if r := rows[1]; r.Strategy != "markov_model_mull" || r.Symbol != "MULL" || r.Signal != ScanNoBuy || r.Status != "NO_SIGNAL" {
		t.Errorf("no-buy row = %+v", r)
	}
}

func TestStrategyScansCoversEveryMember(t *testing.T) {
	status := map[string]runner.SignalScanRow{
		"fires": {StrategyID: "fires", Symbols: "VRTL"},
		"quiet": {StrategyID: "quiet", Symbols: "MULL", Trace: []runner.TraceRow{
			{Symbol: "MULL", Date: "2026-10-13", Reason: "old day"},
			{Symbol: "MULL", Date: "2026-10-14", Reason: "state is bear, not bull"},
		}},
	}
	written := []BuySignal{{StrategyID: "fires", Symbol: "VRTL", Price: 10, AllocationPct: 0.2, TakeProfit: 10.5, StopLoss: 9.5}}
	got := strategyScans([]string{"fires", "quiet", "missing"}, nil, status, written, "2026-10-14")
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Status != ScanBuy || got[0].Symbols != "VRTL" {
		t.Errorf("fires = %+v", got[0])
	}
	if got[1].Status != ScanNoBuy || got[1].Detail != "no entry as of 2026-10-14: state is bear, not bull" {
		t.Errorf("quiet = %+v", got[1])
	}
	if got[2].Status != ScanNotScanned || got[2].EmailRow().Status != "ERROR" {
		t.Errorf("missing = %+v", got[2])
	}
}

func TestBuyDetailCapsLongLists(t *testing.T) {
	var buys []Signal
	for _, s := range []string{"A", "B", "C", "D", "E", "F", "G"} {
		buys = append(buys, Signal{Symbol: s, Price: 1})
	}
	d := buyDetail(buys)
	if !strings.HasPrefix(d, "7 BUY: A @") || !strings.HasSuffix(d, "+2 more") || strings.Contains(d, "F @") {
		t.Errorf("detail = %s", d)
	}
}
