package strategy

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// Real TSLL bars: one entry per bar, each priced from that bar's own close.
func TestTSLLDailyOneShareOnRealBars(t *testing.T) {
	s := &TSLLDailyOneShareStrategy{}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	market := realbars.Copy(t, "TSLL", "AAPL")
	db, err := storage.OpenSQLite(market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bars, _, err := storage.FetchBars(db, "backtest_start", []string{"TSLL", "AAPL"}, "2021-01-01", "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	sigs := s.GenerateSignals(bars)
	if len(sigs) != len(bars["TSLL"]) || len(sigs) < 500 {
		t.Fatalf("got %d signals for %d TSLL bars", len(sigs), len(bars["TSLL"]))
	}
	for i, sig := range sigs {
		b := bars["TSLL"][i]
		if sig.Symbol != "TSLL" || sig.Date != b.Date[:10] || sig.Entry != 1 || sig.HoldDaysOverride != 1 {
			t.Fatalf("signal %d: %+v for bar %+v", i, sig, b)
		}
		if math.Abs(sig.TakeProfit-b.Close*1.05) > 1e-6 || math.Abs(sig.StopLoss-b.Close*0.80) > 1e-6 {
			t.Fatalf("%s: TP/SL = %.4f/%.4f, want %.4f/%.4f", sig.Date, sig.TakeProfit, sig.StopLoss, b.Close*1.05, b.Close*0.80)
		}
	}
	cfg := s.DefaultConfig()
	if cfg.PositionSizing != "fixed_shares" || cfg.FixedShares != 1 {
		t.Errorf("sizing = %s/%d, want fixed_shares/1", cfg.PositionSizing, cfg.FixedShares)
	}
}
