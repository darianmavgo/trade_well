package runner

import (
	"bufio"
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/options"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
	"github.com/olekukonko/tablewriter"
	_ "modernc.org/sqlite"
)

type RunResult struct {
	Strat       strategy.Strategy
	Report      models.PerformanceReport
	Trades      []models.Trade
	EquityCurve []models.DailyEquityPoint
	DbPath      string
	RunID       int64
	SignalCount int
	Err         error
	// Engine is the simulator that produced the run: storage.EngineHourly
	// for a strategy that uses a setting the hourly simulator does not model.
	Engine string
	// Suspect is why the run should not be ranked: a symbol it traded has an unadjusted split
	// (sessions.SplitJumps). Empty when nothing is wrong.
	Suspect string

	// Set for strategies simulated on total-return (dividend-adjusted) prices.
	// Buy-and-hold decomposition, before fees/slippage, entry signal → last bar.
	TotalReturn         bool
	Notes               []string // extra strategy-specific result lines (printed by PrintNotes)
	DividendsReinvested bool
	DividendCash        float64 // total cash dividends received (only when not reinvested)
	TotalMarginInterest float64 // total margin interest debited
	PriceReturnPct      float64 // capital gains only (raw closes)
	DividendReturnPct   float64 // reinvested dividends (total − price)
	TotalReturnPct      float64
	BreakdownSymbol     string
	BreakdownStart      string
	BreakdownEnd        string
}

func PrintPerformanceTearSheet(strategyName string, report models.PerformanceReport) {
	fmt.Printf("\n========================================================================================\n")
	fmt.Printf("📊 QUANTITATIVE PORTFOLIO TEAR SHEET: %s\n", strings.ToUpper(strategyName))
	fmt.Printf("📅 BACKTEST TIME WINDOW: %s ➔ %s (%.1f Years | %d Trading Days)\n",
		report.StartDate, report.EndDate, report.TotalCalendarYears, report.TotalTradingDays)
	fmt.Printf("========================================================================================\n")

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Metric", "Value", "Benchmark / Context"})
	table.SetBorder(true)
	table.SetAutoWrapText(false)

	// Time Window
	table.Append([]string{
		"Backtest Time Window",
		fmt.Sprintf("%s to %s", report.StartDate, report.EndDate),
		fmt.Sprintf("%d trading days (%.1f years)", report.TotalTradingDays, report.TotalCalendarYears),
	})
	if report.IdleKnown {
		pct := 0.0
		if report.TotalTradingDays > 0 {
			pct = float64(report.IdleDays) / float64(report.TotalTradingDays) * 100
		}
		table.Append([]string{
			"Idle Days",
			fmt.Sprintf("%d", report.IdleDays),
			fmt.Sprintf("%.1f%% of sessions with no open position", pct),
		})
	}

	// Capital & Returns
	table.Append([]string{"Initial Capital", fmt.Sprintf("$%.2f", report.InitialCapital), "Starting portfolio cash"})
	table.Append([]string{"Ending Total Equity", fmt.Sprintf("$%.2f", report.FinalEquity), "Cash + open positions"})
	table.Append([]string{"Net Realized Profit", fmt.Sprintf("$%.2f", report.NetProfit), fmt.Sprintf("%.2f%% total return", report.TotalReturnPct*100)})
	table.Append([]string{"CAGR (Annualized Return)", fmt.Sprintf("%.2f%%", report.CAGR*100), "Compound Annual Growth Rate"})
	table.Append([]string{"Sharpe Ratio (Annualized)", fmt.Sprintf("%.2f", report.SharpeRatio), "Risk-adjusted return vs. 0% Rf"})
	table.Append([]string{"Sortino Ratio (Annualized)", fmt.Sprintf("%.2f", report.SortinoRatio), "Downside volatility adjusted"})
	table.Append([]string{"Calmar Ratio", fmt.Sprintf("%.2f", report.CalmarRatio), "CAGR / Max Drawdown"})
	table.Append([]string{"Omega Ratio", fmt.Sprintf("%.2f", report.OmegaRatio), "Gain-to-loss probability ratio"})
	table.Append([]string{"Ulcer Index", fmt.Sprintf("%.2f", report.UlcerIndex), "Depth & duration of drawdowns"})

	if report.Beta != 0 || report.Alpha != 0 {
		table.Append([]string{"Alpha (vs. Benchmark)", fmt.Sprintf("%.2f%%", report.Alpha*100), "Excess return over benchmark"})
		table.Append([]string{"Beta (vs. Benchmark)", fmt.Sprintf("%.2f", report.Beta), "Systematic market volatility"})
	}

	// Highlighted Max Drawdown Details
	table.Append([]string{
		"🔴 MAX DRAWDOWN (MDD %)",
		fmt.Sprintf("%.2f%%", report.MaxDrawdownPct*100),
		fmt.Sprintf("Worst account decline from peak equity"),
	})
	table.Append([]string{
		"🔴 MAX DRAWDOWN ($ LOSS)",
		fmt.Sprintf("-$%.2f", report.MaxDrawdownDollars),
		fmt.Sprintf("Peak: $%.2f ➔ Trough: $%.2f", report.MaxDrawdownPeakEquity, report.MaxDrawdownTroughEquity),
	})
	table.Append([]string{
		"🔴 MAX DRAWDOWN DATES",
		fmt.Sprintf("%s ➔ %s", report.MaxDrawdownPeakDate, report.MaxDrawdownTroughDate),
		fmt.Sprintf("Peak equity to trough equity dates"),
	})
	table.Append([]string{
		"🔴 MAX DRAWDOWN DURATION",
		fmt.Sprintf("%d days", report.MaxDrawdownDuration),
		fmt.Sprintf("Longest underwater period without new ATH"),
	})

	// Trade-Level Performance
	table.Append([]string{"Total Completed Trades", fmt.Sprintf("%d", report.TotalTrades), fmt.Sprintf("%d Wins / %d Losses", report.WinningTrades, report.LosingTrades)})
	table.Append([]string{"Trade Win Rate", fmt.Sprintf("%.2f%%", report.WinRate*100), "Pct of closed trades in profit"})
	table.Append([]string{"Profit Factor", fmt.Sprintf("%.2f", report.ProfitFactor), "Gross Profits / Gross Losses"})
	table.Append([]string{"Win / Loss Payoff Ratio", fmt.Sprintf("%.2f", report.PayoffRatio), "Avg Win $ / Avg Loss $"})
	table.Append([]string{"Average Win", fmt.Sprintf("$%.2f", report.AvgWinAmount), "Per winning trade"})
	table.Append([]string{"Average Loss", fmt.Sprintf("$%.2f", report.AvgLossAmount), "Per losing trade"})
	table.Append([]string{"Average MAE (Drawdown)", fmt.Sprintf("%.2f%%", report.AvgMAE*100), "Max Adverse Excursion during trade"})
	table.Append([]string{"Average MFE (Runup)", fmt.Sprintf("%.2f%%", report.AvgMFE*100), "Max Favorable Excursion during trade"})
	table.Append([]string{"Average Holding Period", fmt.Sprintf("%.1f days", report.AvgHoldingDays), "Holding horizon"})
	table.Append([]string{"Total Commissions & Fees", fmt.Sprintf("$%.2f", report.TotalCommissionPaid), "Exchange / broker costs deducted"})

	table.Render()
}

// PrintStrategyList prints how many strategies each family holds, then asks on
// in whether to dump every strategy to out. Anything but y/yes (including EOF
// on a non-interactive stdin) skips the dump.
func PrintStrategyList(out io.Writer, in io.Reader) {
	code := strategy.List()
	fmt.Fprintf(out, "\n%d strategies defined by their own sql/strategies pipeline\n", len(code))
	strategy.PrintFamilyCounts(out)
	total := len(code)
	for _, fc := range strategy.FamilyCounts() {
		total += fc.Count
	}
	fmt.Fprintf(out, "\nDump all %d strategies to screen? [y/N] ", total)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		fmt.Fprintf(out, "\nRun any strategy with: ./bin/backtest -strategy <ID>\n\n")
		return
	}

	table := tablewriter.NewWriter(out)
	table.SetHeader([]string{"ID", "Strategy Name", "Default Target", "Default Stop", "Hold", "Description"})
	table.SetBorder(true)
	table.SetAutoWrapText(false)
	for _, s := range strategy.ListAll() {
		cfg := s.DefaultConfig()
		table.Append([]string{
			s.ID(),
			s.Name(),
			FormatTargetDisplay(cfg),
			FormatStopDisplay(cfg),
			fmt.Sprintf("%dd", cfg.HoldingWindow),
			s.Description(),
		})
	}
	table.Render()
	fmt.Fprintf(out, "\nRun any strategy with: ./bin/backtest -strategy <ID>\n\n")
}

// FormatTargetDisplay and FormatStopDisplay render a strategy's baseline
// target/stop for a -list table, honoring the same TargetPct (multiplier,
// legacy) / TakeProfitPct (offset, preferred) and StopLossPct (multiplier)
// conventions as pkg/sim. A plain (cfg.TargetPct-1)*100 or
// (1-cfg.StopLossPct)*100 renders garbage ("+99800.0%", "-100.0%") for any
// strategy that sets only TakeProfitPct or has StopLossPct == 0
// (intentionally "no stop") — which covers most strategies.
func FormatTargetDisplay(cfg strategy.StrategyConfig) string {
	if cfg.TargetPct > 1.0 {
		return fmt.Sprintf("+%.1f%%", (cfg.TargetPct-1)*100)
	}
	if cfg.TakeProfitPct > 0 {
		return fmt.Sprintf("+%.1f%%", cfg.TakeProfitPct*100)
	}
	return "n/a (per-signal)"
}

func FormatStopDisplay(cfg strategy.StrategyConfig) string {
	if cfg.StopLossPct <= 0 {
		return "none"
	}
	if cfg.StopLossPct < 1.0 {
		return fmt.Sprintf("-%.1f%%", (1-cfg.StopLossPct)*100)
	}
	return fmt.Sprintf("-%.1f%%", cfg.StopLossPct*100)
}

func PrintTradesTable(trades []models.Trade, symbolFilter string) {
	if len(trades) == 0 {
		return
	}
	fmt.Printf("\nCompleted Trades for Simulation (Total: %d):\n", len(trades))
	tTable := tablewriter.NewWriter(os.Stdout)
	tTable.SetHeader([]string{"ID", "Symbol", "Entry Date", "Entry $", "Exit Date", "Exit $", "Hold Days", "Exit Reason", "Net PnL", "Return %"})
	tTable.SetBorder(true)

	startIdx := 0
	if symbolFilter == "" && len(trades) > 20 {
		startIdx = len(trades) - 20
	}

	for _, t := range trades[startIdx:] {
		tTable.Append([]string{
			fmt.Sprintf("%d", t.ID),
			t.Symbol,
			t.EntryDate,
			fmt.Sprintf("$%.2f", t.EntryPrice),
			t.ExitDate,
			fmt.Sprintf("$%.2f", t.ExitPrice),
			fmt.Sprintf("%d", t.HoldDays),
			string(t.ExitReason),
			fmt.Sprintf("$%.2f", t.NetPnL),
			fmt.Sprintf("%.2f%%", t.ReturnPct*100),
		})
	}
	tTable.Render()
}

// ComparisonTableRows is the most rows PrintComparisonTable prints.
const ComparisonTableRows = 100

func PrintComparisonTable(results []RunResult) {
	// Sort by CAGR descending
	sort.Slice(results, func(i, j int) bool {
		if results[i].Err != nil {
			return false
		}
		if results[j].Err != nil {
			return true
		}
		return results[i].Report.CAGR > results[j].Report.CAGR
	})
	fmt.Printf("\n========================================================================================================================\n")
	fmt.Printf("📊 MULTI-STRATEGY CONCURRENT BENCHMARK COMPARISON\n")
	fmt.Printf("========================================================================================================================\n")

	// A table of tens of thousands of rows is not readable. Print the best
	// ComparisonTableRows by CAGR, the rest are in the result databases.
	if len(results) > ComparisonTableRows {
		fmt.Printf("Showing the best %d of %d strategies by CAGR. All of them are in the run's result databases.\n", ComparisonTableRows, len(results))
		results = results[:ComparisonTableRows]
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Strategy ID", "Name", "Total Return", "CAGR", "Sharpe", "Max DD %", "🔴 DD Duration", "Win Rate", "Trades", "Idle Days", "SQLite Results File"})
	table.SetBorder(true)
	table.SetAutoWrapText(false)

	for _, r := range results {
		if r.Err != nil {
			table.Append([]string{r.Strat.ID(), r.Strat.Name(), "ERROR", "ERROR", "ERROR", "ERROR", "ERROR", "ERROR", "0", "—", "N/A"})
			continue
		}
		table.Append([]string{
			r.Strat.ID(),
			r.Strat.Name(),
			fmt.Sprintf("%.2f%%", r.Report.TotalReturnPct*100),
			fmt.Sprintf("%.2f%%", r.Report.CAGR*100),
			fmt.Sprintf("%.2f", r.Report.SharpeRatio),
			fmt.Sprintf("%.2f%%", r.Report.MaxDrawdownPct*100),
			fmt.Sprintf("%d days", r.Report.MaxDrawdownDuration),
			fmt.Sprintf("%.2f%%", r.Report.WinRate*100),
			fmt.Sprintf("%d", r.Report.TotalTrades),
			formatIdleDays(r.Report),
			r.DbPath,
		})
	}
	table.Render()
}

func formatIdleDays(report models.PerformanceReport) string {
	if !report.IdleKnown {
		return "—"
	}
	return fmt.Sprintf("%d", report.IdleDays)
}

// ConfigOverride replaces fields of a strategy's DefaultConfig. Zero values
// keep the strategy's own setting. AllocPct is a fraction of equity per
// position (0.10 = 10%). When set, sizing is fixed_pct so that fraction is
// what the fill uses, including strategies whose default is fixed shares, and a
// signal's own allocation (the Markov strength ladder) no longer applies.
type ConfigOverride struct {
	StopLoss     float64
	Target       float64
	Hold         int
	MaxPositions int
	AllocPct     float64
}

// Apply returns cfg with every non-zero override written in.
func (o ConfigOverride) Apply(cfg strategy.StrategyConfig) strategy.StrategyConfig {
	if o.StopLoss > 0 {
		cfg.StopLossPct = o.StopLoss
	}
	if o.Target > 0 {
		cfg.TargetPct = o.Target
	}
	if o.Hold > 0 {
		cfg.HoldingWindow = o.Hold
	}
	if o.MaxPositions > 0 {
		cfg.PositionCap = o.MaxPositions
	}
	if o.AllocPct > 0 {
		cfg.AllocationPct = o.AllocPct
		cfg.PositionSizing = "fixed_pct"
		cfg.FlatAllocation = true
	}
	return cfg
}

func BuildConfig(s strategy.Strategy, stopLoss, profitTarget float64, holdWindow, maxPositions int) strategy.StrategyConfig {
	return ConfigOverride{
		StopLoss:     stopLoss,
		Target:       profitTarget,
		Hold:         holdWindow,
		MaxPositions: maxPositions,
	}.Apply(s.DefaultConfig())
}

// scopeDatesToStrategy restricts a global, DB-wide sorted date list down to the
// dates covered by a strategy's own required symbols (+ benchmark), so CAGR and
// drawdown-duration metrics reflect that strategy's actual trading window instead
// of being diluted by unrelated long-history symbols that happen to share the DB.
func scopeDatesToStrategy(
	strat strategy.Strategy,
	cfg strategy.StrategyConfig,
	barsBySymbol map[string][]models.Bar,
	sortedDates []string,
) []string {
	relevantSymbols := make(map[string]struct{})
	if reqProvider, ok := strat.(strategy.RequiredSymbolsProvider); ok {
		for _, sym := range reqProvider.RequiredSymbols() {
			if sym = strings.ToUpper(strings.TrimSpace(sym)); sym != "" {
				relevantSymbols[sym] = struct{}{}
			}
		}
	}
	if bm := strings.ToUpper(strings.TrimSpace(cfg.Benchmark)); bm != "" {
		relevantSymbols[bm] = struct{}{}
	}
	if len(relevantSymbols) == 0 {
		return sortedDates // no symbol info available; fall back to the full range
	}

	relevantDates := make(map[string]struct{})
	for sym, bars := range barsBySymbol {
		if _, ok := relevantSymbols[strings.ToUpper(sym)]; !ok {
			continue
		}
		for _, b := range bars {
			relevantDates[b.Date] = struct{}{}
		}
	}
	if len(relevantDates) == 0 {
		return sortedDates
	}

	scoped := make([]string, 0, len(relevantDates))
	for _, d := range sortedDates {
		if _, ok := relevantDates[d]; ok {
			scoped = append(scoped, d)
		}
	}
	return scoped
}

// ExecuteStrategy runs one strategy with dividends reinvested (the default for
// total-return strategies); see ExecuteStrategyWithDividends.
func ExecuteStrategy(
	strat strategy.Strategy,
	cfg strategy.StrategyConfig,
	barsBySymbol map[string][]models.Bar,
	sortedDates []string,
	capital float64,
	symbolFilter string,
	outDir string,
	marketDBPath string,
) RunResult {
	return ExecuteStrategyWithDividends(strat, cfg, barsBySymbol, sortedDates, capital, symbolFilter, outDir, marketDBPath, true)
}

// ExecuteStrategyWithDividends is ExecuteStrategy with an explicit dividend
// treatment for total-return strategies (see strategy.TotalReturnProvider).
// reinvestDividends=true: simulate on dividend-adjusted prices, i.e. dividends
// reinvested. false: simulate on raw prices and pay dividends into cash,
// uninvested.
func ExecuteStrategyWithDividends(
	strat strategy.Strategy,
	cfg strategy.StrategyConfig,
	barsBySymbol map[string][]models.Bar,
	sortedDates []string,
	capital float64,
	symbolFilter string,
	outDir string,
	marketDBPath string,
	reinvestDividends bool,
) RunResult {
	if why := RowProblem(cfg); why != "" {
		return RunResult{Strat: strat, Err: fmt.Errorf("%s cannot run: %s", strat.ID(), why)}
	}

	// 1. Results go to the strategy family's database in the run directory; SQL pipelines get a scratch calc
	// database of their own (deleted when the run ends unless KeepCalc).
	results, err := storage.ResultsFor(outDir, strategy.FamilyOf(strat))
	if err != nil {
		return RunResult{Strat: strat, Err: fmt.Errorf("failed to open results database for %s: %w", strat.ID(), err)}
	}
	outDBPath := results.Path
	calcPath, cleanupCalc := calcDBPath(outDir, strat.ID())
	defer cleanupCalc()

	// 2. Set DB routing for ALL strategies
	strat.SetDatabases(marketDBPath, calcPath)

	// 2a. Covered-call overlays have their own simulator (pkg/options).
	if ov, ok := strat.(strategy.OptionOverlayProvider); ok {
		return executeOverlay(strat, ov.OverlaySpec(), cfg, barsBySymbol, capital, results, marketDBPath)
	}

	// 2b. Dividend-inclusive (total-return) strategies were played on the daily simulator, which is
	// gone: the hourly simulator pays no dividends. No row uses it (hold_strategy.total_return is 0
	// everywhere), so refuse it with the reason instead of running something that would mislead.
	var tr *totalReturnInfo
	if p, ok := strat.(strategy.TotalReturnProvider); ok && p.UsesTotalReturn() {
		return RunResult{Strat: strat, Err: fmt.Errorf("%s: total-return (dividend) backtests are not supported: the hourly simulator pays no dividends", strat.ID())}
	}

	// 3. Generate signals (calculations will now write natively into outDBPath if applicable)
	signals := strat.GenerateSignals(barsBySymbol)
	symUpper := strings.ToUpper(symbolFilter)
	if symUpper != "" {
		var filtered []models.Signal
		for _, s := range signals {
			if strings.ToUpper(s.Symbol) == symUpper {
				filtered = append(filtered, s)
			}
		}
		signals = filtered
	}

	// 2. Simulate portfolio. Scope the simulation clock to dates relevant to this
	// strategy's own symbols rather than the full DB-wide date list — otherwise a
	// strategy trading a young ticker (e.g. 2021+) gets its CAGR diluted by decades
	// of idle time from unrelated long-history symbols sharing the same database.
	scopedDates := scopeDatesToStrategy(strat, cfg, barsBySymbol, sortedDates)
	suspect := splitSuspect(marketDBPath, signalSymbols(signals), scopedDates)
	engine, report, trades, equityCurve, err := simulateRun(strat.ID(), cfg, signals, barsBySymbol, scopedDates, capital, marketDBPath)
	if err != nil {
		return RunResult{Strat: strat, Err: err}
	}

	// 5. Persist the whole run in one transaction.
	savedCurve := equityCurve
	if cfg.SkipEquityCurve {
		savedCurve = nil
	}
	payload := storage.RunPayload{
		Signals: signals, Trades: trades, Equity: savedCurve,
		Reports: []storage.NamedReport{{StrategyID: strat.ID(), Report: report}},
	}
	var bd *totalReturnBreakdown
	if tr != nil && len(signals) > 0 {
		if b, ok := tr.breakdown(signals[0].Symbol, signals[0].Date); ok {
			bd = &totalReturnBreakdown{sym: signals[0].Symbol, b: b}
			payload.Breakdown = &storage.Breakdown{Symbol: bd.sym, Start: b.start, End: b.end, Reinvested: tr.reinvest,
				Price: b.price, Dividend: b.total - b.price, Total: b.total}
		}
	}
	runID, werr := results.WriteRun(storage.RunMeta{
		StrategyID: strat.ID(), Kind: "single",
		WindowStart: report.StartDate, WindowEnd: report.EndDate,
		Capital: capital, AllocPct: cfg.AllocationPct, MarketMaxDate: lastDate(sortedDates), Engine: engine, Suspect: suspect,
	}, payload)
	if werr != nil {
		log.Printf("Warning: Failed to save results for %s to %s: %v", strat.ID(), outDBPath, werr)
	}

	rr := RunResult{
		Strat:       strat,
		Report:      report,
		Trades:      trades,
		EquityCurve: equityCurve,
		DbPath:      outDBPath,
		RunID:       runID,
		SignalCount: len(signals),
		Engine:      engine,
		Suspect:     suspect,
	}
	if bd != nil {
		b := bd.b
		rr.DividendsReinvested = tr.reinvest
		rr.TotalReturn, rr.PriceReturnPct, rr.DividendReturnPct, rr.TotalReturnPct = true, b.price, b.total-b.price, b.total
		rr.BreakdownSymbol, rr.BreakdownStart, rr.BreakdownEnd = bd.sym, b.start, b.end
	}
	return rr
}

// PrintNotes prints strategy-specific result lines (e.g. covered-call income).
func PrintNotes(res RunResult) {
	if len(res.Notes) == 0 {
		return
	}
	fmt.Println()
	for _, n := range res.Notes {
		fmt.Println(n)
	}
}

// PrintReturnBreakdown prints the capital-gain vs dividend split for total-return strategies.
func PrintReturnBreakdown(res RunResult) {
	if !res.TotalReturn {
		return
	}
	mode := "dividends reinvested"
	if !res.DividendsReinvested {
		mode = "dividends NOT reinvested"
	}
	fmt.Printf("\n💰 %s buy & hold return split (%s ➔ %s, before fees/slippage, %s):\n", res.BreakdownSymbol, res.BreakdownStart, res.BreakdownEnd, mode)
	fmt.Printf("   Capital gains: %+.2f%%   Dividends: %+.2f%%   Total: %+.2f%%\n", res.PriceReturnPct*100, res.DividendReturnPct*100, res.TotalReturnPct*100)
	if res.DividendsReinvested {
		fmt.Printf("   (Simulated on dividend-adjusted prices; tear-sheet returns already include dividends, net of costs.)\n")
	} else {
		fmt.Printf("   (Simulated on raw prices; $%.2f of dividends paid into cash balance; tear-sheet equity includes that cash.)\n", res.DividendCash)
	}
	if res.TotalMarginInterest > 0 {
		fmt.Printf("💸 Margin Borrowing Cash Flow:\n")
		fmt.Printf("   Total Margin Interest Paid: -$%.2f debited from cash account\n", res.TotalMarginInterest)
		netCF := res.DividendCash - res.TotalMarginInterest
		fmt.Printf("   Net Cash Flow (Dividends Received − Margin Interest Paid): %s$%.2f\n", sign(netCF), math.Abs(netCF))
	}
}

// totalReturnInfo keeps the raw (price-only) bars of a strategy whose bars were adjusted.
type totalReturnInfo struct {
	raw      map[string][]models.Bar
	divs     map[string]map[string]float64 // symbol → ex-date → $/share (non-reinvest mode)
	reinvest bool
}

type splitReturn struct {
	price, total float64
	start, end   string
}

// toTotalReturnBars returns a copy of barsBySymbol where the strategy's required
// symbols and benchmark have OHLC scaled by AdjClose/Close. Other symbols and the
// caller's slices are left untouched (strategies may run concurrently on one map).
func toTotalReturnBars(strat strategy.Strategy, cfg strategy.StrategyConfig, in map[string][]models.Bar, reinvest bool) (map[string][]models.Bar, *totalReturnInfo) {
	if m, ok := strat.(strategy.DividendModeSetter); ok {
		m.SetReinvestDividends(reinvest)
	}
	need := map[string]bool{strings.ToUpper(strings.TrimSpace(cfg.Benchmark)): true}
	if rp, ok := strat.(strategy.RequiredSymbolsProvider); ok {
		for _, s := range rp.RequiredSymbols() {
			need[strings.ToUpper(strings.TrimSpace(s))] = true
		}
	}
	out := make(map[string][]models.Bar, len(in))
	info := &totalReturnInfo{raw: map[string][]models.Bar{}, reinvest: reinvest}
	for sym, bars := range in {
		if !need[strings.ToUpper(sym)] {
			out[sym] = bars
			continue
		}
		info.raw[sym] = bars
		if !reinvest {
			// Keep raw prices; recover cash dividends from steps in AdjClose/Close.
			info.divs = ensureDivs(info.divs)
			m := options.DividendsFromAdjClose(bars)
			info.divs[sym] = m
			out[sym] = bars
			continue
		}
		adj := make([]models.Bar, len(bars))
		for i, b := range bars {
			if b.AdjClose > 0 && b.Close > 0 {
				f := b.AdjClose / b.Close
				b.Open, b.High, b.Low, b.Close = b.Open*f, b.High*f, b.Low*f, b.Close*f
			}
			adj[i] = b
		}
		out[sym] = adj
	}
	return out, info
}

func ensureDivs(m map[string]map[string]float64) map[string]map[string]float64 {
	if m == nil {
		return map[string]map[string]float64{}
	}
	return m
}

func (t *totalReturnInfo) breakdown(symbol, fromDate string) (splitReturn, bool) {
	var first, last *models.Bar
	bars := t.raw[symbol]
	for i := range bars {
		if bars[i].Date < fromDate {
			continue
		}
		if first == nil {
			first = &bars[i]
		}
		last = &bars[i]
	}
	if first == nil || first.Close <= 0 || last.Close <= 0 || first.AdjClose <= 0 || last.AdjClose <= 0 {
		return splitReturn{}, false
	}
	price := last.Close/first.Close - 1
	total := last.AdjClose/first.AdjClose - 1
	if !t.reinvest { // simple (uncompounded) dividend yield on the entry price
		var sum float64
		for d, v := range t.divs[symbol] {
			if d > first.Date && d <= last.Date {
				sum += v
			}
		}
		total = price + sum/first.Close
	}
	return splitReturn{price: price, total: total, start: first.Date, end: last.Date}, true
}

// RequiredSymbolsFor computes the union of every symbol a set of strategies
// actually needs — each strategy's RequiredSymbols() (if implemented) plus its
// DefaultConfig().Benchmark — optionally including an extra symbolFilter. Used
// to scope a bar fetch to only what's needed instead of loading an entire
// multi-thousand-symbol database for a strategy that trades one symbol.
func RequiredSymbolsFor(strategies []strategy.Strategy, symbolFilter string) []string {
	requiredSet := make(map[string]struct{})

	if sym := strings.ToUpper(strings.TrimSpace(symbolFilter)); sym != "" {
		requiredSet[sym] = struct{}{}
	}
	for _, s := range strategies {
		if reqProvider, ok := s.(strategy.RequiredSymbolsProvider); ok {
			for _, sym := range reqProvider.RequiredSymbols() {
				if sym = strings.ToUpper(strings.TrimSpace(sym)); sym != "" {
					requiredSet[sym] = struct{}{}
				}
			}
		}
		cfg := s.DefaultConfig()
		if bm := strings.ToUpper(strings.TrimSpace(cfg.Benchmark)); bm != "" {
			requiredSet[bm] = struct{}{}
		}
	}

	out := make([]string, 0, len(requiredSet))
	for sym := range requiredSet {
		out = append(out, sym)
	}
	sort.Strings(out)
	return out
}

// ScopedSymbols returns RequiredSymbolsFor(strategies, symbolFilter) and true
// when every strategy declares the symbols it needs (RequiredSymbols is
// implemented and non-empty). If any strategy does not (for example a
// universe-wide SQL pipeline), the bar set cannot be narrowed and it returns
// nil, false: the caller must load every symbol.
func ScopedSymbols(strategies []strategy.Strategy, symbolFilter string) ([]string, bool) {
	for _, s := range strategies {
		p, ok := s.(strategy.RequiredSymbolsProvider)
		if !ok || len(p.RequiredSymbols()) == 0 {
			return nil, false
		}
	}
	return RequiredSymbolsFor(strategies, symbolFilter), true
}

// missingFrom returns, sorted, the symbols of set that have no bar at all in table: an indexed
// existence check per symbol.
func missingFrom(db *sqlx.DB, table string, set map[string]struct{}) []string {
	var missing []string
	for sym := range set {
		var have int
		if err := db.Get(&have, fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE symbol = ?)", table), sym); err != nil || have == 0 {
			missing = append(missing, sym)
		}
	}
	sort.Strings(missing)
	return missing
}

// SplitByData separates the strategies whose symbols all have bars in table from those that need
// a symbol with none. A bulk run uses it to skip what it cannot test instead of failing as a whole.
// missing lists the symbols with no bars, sorted. A strategy that names no symbols is kept.
func SplitByData(targetDb, table string, strategies []strategy.Strategy, symbolFilter string) (have, lacking []strategy.Strategy, missing []string, err error) {
	db, err := storage.OpenSQLite(targetDb)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to open database %s: %w", targetDb, err)
	}
	defer db.Close()
	set := map[string]struct{}{}
	for _, sym := range RequiredSymbolsFor(strategies, symbolFilter) {
		set[sym] = struct{}{}
	}
	missing = missingFrom(db, table, set)
	gone := make(map[string]bool, len(missing))
	for _, sym := range missing {
		gone[sym] = true
	}
	for _, s := range strategies {
		lack := false
		for _, sym := range RequiredSymbolsFor([]strategy.Strategy{s}, symbolFilter) {
			if gone[sym] {
				lack = true
				break
			}
		}
		if lack {
			lacking = append(lacking, s)
		} else {
			have = append(have, s)
		}
	}
	return have, lacking, missing, nil
}

func DetectAndDownloadMissingData(
	targetDb string,
	tableName string,
	strategies []strategy.Strategy,
	symbolFilter string,
	autoDownload bool,
	downloadYears int,
) error {
	requiredSet := make(map[string]struct{})
	for _, sym := range RequiredSymbolsFor(strategies, symbolFilter) {
		requiredSet[sym] = struct{}{}
	}

	// 3. Open DB to check table and coverage
	db, err := storage.OpenSQLite(targetDb)
	if err != nil {
		return fmt.Errorf("failed to open database %s: %w", targetDb, err)
	}

	if err := storage.EnsureBarTable(db, tableName); err != nil {
		db.Close()
		return fmt.Errorf("failed to ensure bar table %s: %w", tableName, err)
	}

	// If no specific symbols required and table is completely empty, populate baseline universe.
	// The table is only counted then: a count scans every bar of the market database.
	if len(requiredSet) == 0 {
		var totalBars int
		_ = db.Get(&totalBars, fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName))
		if totalBars == 0 {
			for _, sym := range []string{"SPY", "QQQ", "VOO", "TECL", "SOXL", "TQQQ"} {
				requiredSet[sym] = struct{}{}
			}
		}
	}

	missingSymbols := missingFrom(db, tableName, requiredSet)

	db.Close() // Close before spawning download process

	if len(missingSymbols) == 0 {
		return nil
	}

	if !autoDownload {
		return fmt.Errorf("missing market data for symbol(s) %v in %s (%s). Run with -auto-download or run: ./bin/market_history -symbols %s",
			missingSymbols, targetDb, tableName, strings.Join(missingSymbols, ","))
	}

	fmt.Printf("\n🔍 Detected missing market data for %d symbol(s) in %s (%s): %v\n",
		len(missingSymbols), targetDb, tableName, missingSymbols)
	fmt.Printf("📥 Running download to fetch missing historical bars (%d years)...\n\n", downloadYears)

	if err := RunDownload(targetDb, tableName, missingSymbols, downloadYears); err != nil {
		return fmt.Errorf("download command failed for symbols %v: %w", missingSymbols, err)
	}

	fmt.Printf("\n✅ Successfully updated market data for %v in %s (%s).\n", missingSymbols, targetDb, tableName)
	return nil
}

func sign(v float64) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}

// totalReturnBreakdown pairs a breakdown with the symbol it was computed for.
type totalReturnBreakdown struct {
	sym string
	b   splitReturn
}

func lastDate(dates []string) string {
	if len(dates) == 0 {
		return ""
	}
	return dates[len(dates)-1]
}
