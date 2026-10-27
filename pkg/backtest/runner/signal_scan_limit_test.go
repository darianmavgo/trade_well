package runner

import (
	"math"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// limitStrategy is a strategy whose config sets an entry limit; its signals are given.
type limitStrategy struct {
	cfg strategy.StrategyConfig
}

func (s limitStrategy) ID() string                                              { return "limit-live" }
func (s limitStrategy) Name() string                                            { return "limit live" }
func (s limitStrategy) Description() string                                     { return "" }
func (s limitStrategy) DefaultConfig() strategy.StrategyConfig                  { return s.cfg }
func (s limitStrategy) Validate() error                                         { return nil }
func (s limitStrategy) SetDatabases(string, string)                             {}
func (s limitStrategy) GenerateSignals(map[string][]models.Bar) []models.Signal { return nil }

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLiveScanPricesAnEntryLimitStrategyAtThePercentOfTheClose(t *testing.T) {
	strat := limitStrategy{cfg: strategy.StrategyConfig{EntryLimitPct: 0.95, TakeProfitPct: 0.03, StopLossPct: 0.95}}
	sigs := []models.Signal{{Symbol: "ROM", Date: "2026-10-01", Close: 100, BuyLimit: 100, Entry: 1, Direction: "LONG"}}
	row, details := buildSignalScanRow(strat, sigs, "2026-10-01")
	if row.Status != "ENTER" || len(details) != 1 {
		t.Fatalf("row %+v details %+v", row, details)
	}
	d := details[0]
	if !approx(d.BuyLimit, 95) || !approx(d.Price, 95) || !approx(d.Close, 100) {
		t.Errorf("order is a buy limit at 95%% of the 100 close, got price %v limit %v close %v", d.Price, d.BuyLimit, d.Close)
	}
	if !approx(d.TakeProfit, 95*1.03) || !approx(d.StopLoss, 95*0.95) {
		t.Errorf("target and stop are anchored to the 95 limit: tp %v sl %v", d.TakeProfit, d.StopLoss)
	}
}

func TestLiveScanLeavesOtherStrategiesAlone(t *testing.T) {
	strat := limitStrategy{cfg: strategy.StrategyConfig{TakeProfitPct: 0.03, StopLossPct: 0.95}}
	sigs := []models.Signal{{Symbol: "ROM", Date: "2026-10-01", Close: 100, BuyLimit: 100, TakeProfit: 110, StopLoss: 90, Entry: 1}}
	_, details := buildSignalScanRow(strat, sigs, "2026-10-01")
	d := details[0]
	if d.BuyLimit != 100 || d.Price != 100 || d.TakeProfit != 110 || d.StopLoss != 90 {
		t.Errorf("a strategy without EntryLimitPct keeps its own prices: %+v", d)
	}
}

func TestLiveScanKeepsATargetTheSignalCarries(t *testing.T) {
	strat := limitStrategy{cfg: strategy.StrategyConfig{EntryLimitPct: 0.9, TakeProfitPct: 0.03}}
	sigs := []models.Signal{{Symbol: "ROM", Date: "2026-10-01", Close: 100, TakeProfit: 120, Entry: 1}}
	_, details := buildSignalScanRow(strat, sigs, "2026-10-01")
	if !approx(details[0].TakeProfit, 120) || details[0].StopLoss != 0 {
		t.Errorf("signal target kept, no stop configured: %+v", details[0])
	}
}
