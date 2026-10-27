package backtest

import (
	"fmt"

	"github.com/darianmavgo/trade_well/pkg/backtest/analytics"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/options"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// runCoveredCallCommand backtests buy-and-hold + a monthly short call against
// plain buy-and-hold, using option history stored by
// `market_history -source polygon-options`.
func runCoveredCallCommand(dbPath, table, underlying string, capital, otmPct float64, start string, commission, slip float64) error {
	if underlying == "" {
		underlying = "VOO"
	}
	db, err := storage.OpenSQLite(dbPath)
	if err != nil {
		return fmt.Errorf("open %s: %v", dbPath, err)
	}
	defer db.Close()
	if err := storage.EnsureOptionTables(db); err != nil {
		return fmt.Errorf("option tables: %v", err)
	}
	bySym, _, err := storage.FetchBars(db, table, []string{underlying}, "1900-01-01", "2100-01-01")
	if err != nil || len(bySym[underlying]) == 0 {
		return fmt.Errorf("no %s bars in %s (err=%v); run `market_history -symbols %s` first", underlying, table, err, underlying)
	}
	chains, err := storage.FetchCallChains(db, underlying)
	if err != nil {
		return fmt.Errorf("load option chains: %v", err)
	}
	if len(chains) == 0 {
		return fmt.Errorf("no %s option history in %s; run `market_history -source polygon-options -symbols %s` first", underlying, dbPath, underlying)
	}

	res := options.SimulateCoveredCall(options.CoveredCallConfig{
		Underlying: underlying, Capital: capital, OTMPct: otmPct, Start: start,
		Commission: commission, SlipPerShare: slip,
	}, bySym[underlying], chains)
	if len(res.Equity) == 0 {
		return fmt.Errorf("no simulable window: option data starts after the underlying's last bar or every roll lacked quotes")
	}

	cc := analytics.CalculatePerformanceMetrics(capital, res.Trades, res.Equity)
	bh := analytics.CalculatePerformanceMetrics(capital, nil, res.BuyHoldEquity)
	fmt.Printf("\n📞 %s covered call: hold + sell 1 call/100 shares monthly, ~%.1f%% OTM (%d option expiries stored)\n", underlying, otmPct, len(chains))
	runner.PrintPerformanceTearSheet(underlying+" + monthly covered call", cc)
	runner.PrintPerformanceTearSheet(underlying+" buy & hold (same shares)", bh)

	fmt.Printf("\n%-10s %-10s %-22s %8s %8s %10s %s\n", "SOLD", "EXPIRY", "CONTRACT", "PREMIUM", "SETTLE", "NET $", "RESULT")
	for _, t := range res.Trades {
		result := "expired worthless"
		if t.ExitReason == models.ExitReasonEndBacktest {
			result = "still open at last bar"
		} else if t.ExitPrice > 0 {
			result = "in the money (upside capped)"
		}
		fmt.Printf("%-10s %-10s %-22s %8.2f %8.2f %10.0f %s\n", t.EntryDate, t.ExitDate, t.Symbol, t.EntryPrice, t.ExitPrice, t.NetPnL, result)
	}
	fmt.Printf("\nCycles %d (no trade near roll date → held stock only: %d) · ITM at expiry %d · premium collected $%.0f · paid at settlement $%.0f\n",
		res.Cycles, res.Skipped, res.Assigned, res.Premium, res.Settlement)
	fmt.Printf("Return %.1f%% vs buy&hold %.1f%% · max drawdown %.1f%% vs %.1f%%\n",
		cc.TotalReturnPct*100, bh.TotalReturnPct*100, cc.MaxDrawdownPct*100, bh.MaxDrawdownPct*100)
	fmt.Println("Caveats: option prices are Polygon last-trade EOD bars (carried forward on no-trade days, floored at intrinsic), price = last trade − slippage, not a bid; dividends ignored for both legs; ITM expiry is modeled as cash settlement with shares kept.")

	return nil
}
