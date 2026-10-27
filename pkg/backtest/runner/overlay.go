package runner

import (
	"fmt"
	"log"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/analytics"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/options"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// executeOverlay runs a covered-call overlay strategy: buy and hold the
// underlying over the trailing spec.WindowYears, sell a monthly call at the
// target OTM%, get called away and re-buy when exercised, and withdraw
// dividends as paid. Results are persisted like any other strategy's.
func executeOverlay(strat strategy.Strategy, spec strategy.OverlaySpec, cfg strategy.StrategyConfig,
	barsBySymbol map[string][]models.Bar, capital float64, results *storage.ResultsDB, marketDBPath string) RunResult {

	outDBPath := results.Path
	fail := func(err error) RunResult { return RunResult{Strat: strat, DbPath: outDBPath, Err: err} }

	bars := barsBySymbol[spec.Underlying]
	if len(bars) == 0 {
		return fail(fmt.Errorf("no %s bars loaded; run `market_history -symbols %s` first", spec.Underlying, spec.Underlying))
	}
	marketDB, err := storage.OpenSQLite(marketDBPath)
	if err != nil {
		return fail(err)
	}
	defer marketDB.Close()
	if err := storage.EnsureOptionTables(marketDB); err != nil {
		return fail(err)
	}
	chains, err := storage.FetchCallChains(marketDB, spec.Underlying)
	if err != nil {
		return fail(err)
	}
	if len(chains) == 0 {
		return fail(fmt.Errorf("no %s option history in %s; run `market_history -source polygon-options -symbols %s -otm %.0f` first",
			spec.Underlying, marketDBPath, spec.Underlying, spec.OTMPct))
	}

	start := ""
	if spec.WindowYears > 0 {
		if last, err := time.Parse("2006-01-02", bars[len(bars)-1].Date); err == nil {
			start = last.AddDate(-spec.WindowYears, 0, 0).Format("2006-01-02")
		}
	}
	res := options.SimulateCoveredCall(options.CoveredCallConfig{
		Underlying: spec.Underlying, Capital: capital, OTMPct: spec.OTMPct, Start: start,
		Commission: spec.OptionCommission, SlipPerShare: spec.OptionSlip,
		StockSlipPct: cfg.SlippagePct, StockCommission: cfg.CommissionPerShare,
		Dividends: options.DividendsFromAdjClose(bars),
	}, bars, chains)
	if len(res.Equity) == 0 {
		return fail(fmt.Errorf("no simulable window for %s since %s: option history missing or no roll had a tradable quote", spec.Underlying, start))
	}
	for i := range res.Trades {
		res.Trades[i].StrategyID = strat.ID()
	}

	report := analytics.CalculatePerformanceMetrics(capital, res.Trades, res.Equity)
	bh := analytics.CalculatePerformanceMetrics(capital, nil, res.BuyHoldEquity)

	var otmSum float64
	for _, t := range res.Trades {
		if spot := t.InvestedCapital / float64(t.Shares); spot > 0 {
			otmSum += t.TargetPrice/spot - 1
		}
	}
	avgOTM := 0.0
	if n := len(res.Trades); n > 0 {
		avgOTM = otmSum / float64(n) * 100
	}
	// Dividend income and option premium are part of TotalEquity; report them separately.
	notes := []string{
		fmt.Sprintf("📞 %s covered call: %.0f%% OTM target, monthly, %s ➔ %s. Dividends withdrawn as paid (never reinvested).", spec.Underlying, spec.OTMPct, report.StartDate, report.EndDate),
		fmt.Sprintf("   Calls sold %d (avg %.1f%% OTM at sale) · skipped, no tradable quote %d · exercised %d · re-bought %d times", len(res.Trades), avgOTM, res.Skipped, res.Assigned, res.Rebuys),
		fmt.Sprintf("   Option premium collected $%.0f · upside surrendered on exercised calls $%.0f", res.Premium, res.Settlement),
		fmt.Sprintf("   Dividends withdrawn $%.0f · account value at end $%.0f (tear-sheet equity = account + withdrawn dividends = $%.0f)", res.WithdrawnDividends, res.FinalAccountValue, report.FinalEquity),
		fmt.Sprintf("   Buy & hold, same start, dividends withdrawn: total return %.1f%% (max drawdown %.1f%%) vs overlay %.1f%% (max drawdown %.1f%%)",
			bh.TotalReturnPct*100, bh.MaxDrawdownPct*100, report.TotalReturnPct*100, report.MaxDrawdownPct*100),
		"   Option prices are Polygon last-trade EOD bars (not bids); far-OTM strikes trade rarely, so a roll can start days late or be skipped.",
	}

	runID, werr := results.WriteRun(storage.RunMeta{
		StrategyID: strat.ID(), Kind: "single",
		WindowStart: report.StartDate, WindowEnd: report.EndDate, Capital: capital, AllocPct: cfg.AllocationPct,
		MarketMaxDate: bars[len(bars)-1].Date,
	}, storage.RunPayload{
		Trades: res.Trades, Equity: res.Equity,
		Reports: []storage.NamedReport{{StrategyID: strat.ID(), Report: report}},
		Metrics: []storage.RunMetric{
			{Name: "calls_sold", Value: float64(len(res.Trades))}, {Name: "calls_skipped_no_quote", Value: float64(res.Skipped)},
			{Name: "exercised", Value: float64(res.Assigned)}, {Name: "rebuys", Value: float64(res.Rebuys)},
			{Name: "premium_collected", Value: res.Premium}, {Name: "upside_surrendered", Value: res.Settlement},
			{Name: "dividends_withdrawn", Value: res.WithdrawnDividends}, {Name: "account_value_end", Value: res.FinalAccountValue},
			{Name: "avg_otm_pct_at_sale", Value: avgOTM},
			{Name: "buyhold_total_return_pct", Value: bh.TotalReturnPct * 100}, {Name: "buyhold_max_drawdown_pct", Value: bh.MaxDrawdownPct * 100},
		},
	})
	if werr != nil {
		log.Printf("Warning: Failed to save overlay results for %s to %s: %v", strat.ID(), outDBPath, werr)
	}

	return RunResult{Strat: strat, Report: report, Trades: res.Trades, EquityCurve: res.Equity, DbPath: outDBPath, RunID: runID, Notes: notes}
}
