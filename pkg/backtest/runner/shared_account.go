package runner

import (
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/analytics"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/sim"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/olekukonko/tablewriter"
)

// SharedRunResult holds the complete output of a multi-strategy shared account backtest.
type SharedRunResult struct {
	Primary            strategy.Strategy
	Secondaries        []strategy.Strategy
	CombinedID         string // e.g. "sig-voo-buy-tecl+mara_tree" — see SharedAccountID
	CombinedReport     models.PerformanceReport
	PerStrategyReports map[string]models.PerformanceReport
	Trades             []models.Trade
	EquityCurve        []models.DailyEquityPoint
	Signals            []models.Signal
	DbPath             string
	RunID              int64
	PreemptedCount     int
	Idle               analytics.IdleStats
	// AllocPct is the per-position equity fraction applied to every member.
	// 0 means each strategy kept its own DefaultConfig allocation.
	AllocPct float64
	// Default is set when leftover cash was parked in a symbol.
	Default sim.ParkResult
	// ParkContribution is ending equity minus starting capital minus sleeve net PnL.
	ParkContribution float64
	Err              error
	// Engine is the simulator that produced the run (storage.EngineHourly or storage.EngineDaily).
	Engine string
	// Suspect is why the run should not be ranked (see RunResult.Suspect).
	Suspect string
}

// SharedAccountID builds the combined-portfolio strategy_id used to persist and
// look up a shared account's consolidated rows (performance_summary, trades,
// equity_curve, signals). Previously this was the generic literal
// "SHARED_ACCOUNT" regardless of which strategies were actually combined — this
// makes the underlying strategy names visible directly in the ID, matching the
// output DB's own filename convention (shared_<primary>_<secondaries...>.db).
func SharedAccountID(primary strategy.Strategy, secondaries []strategy.Strategy) string {
	ids := []string{primary.ID()}
	for _, sec := range secondaries {
		ids = append(ids, sec.ID())
	}
	return strategy.StackID(ids...)
}

// StackRequest is a first-class multi-strategy run on one cash ledger.
// List order is fill order: Primary = 0 (only it may preempt subordinates
// for capital or a symbol), Secondaries[i] = i+1 (idle-cash utilization;
// they never evict each other).
type StackRequest struct {
	Primary      strategy.Strategy
	Secondaries  []strategy.Strategy
	BarsBySymbol map[string][]models.Bar
	SortedDates  []string
	Capital      float64
	SymbolFilter string
	OutDir       string
	MarketDBPath string
	// Persist writes a unique reports/shared_*.db. Stack-eval pairwise
	// sweeps set this false so they don't flood reports/.
	Persist bool
	// Signals, when non-nil, skips GenerateSignals (used to reuse a primary's
	// already-generated signals across overlay candidates).
	Signals []models.Signal
	// CalcDir holds isolated SQL-pipeline calc DBs. Defaults to OutDir.
	CalcDir string
	// Override replaces each member's DefaultConfig where a field is non-zero.
	// AllocPct 0.10 sizes every position at 10% of equity.
	Override ConfigOverride
	// DefaultAsset, when set, buys that symbol with leftover cash after each session.
	DefaultAsset string
}

// ExecuteStack runs existing strategies as a priority stack on one cash ledger.
// No new pkg/strategy types are created — stacking is an engine/runner concern.
func ExecuteStack(req StackRequest) SharedRunResult {
	if req.Primary == nil {
		return SharedRunResult{Err: fmt.Errorf("primary strategy cannot be nil")}
	}

	// A park is passed as DefaultAsset (see strategy.SplitResidual), never as a sleeve.
	if _, _, err := strategy.SplitResidual(append([]strategy.Strategy{req.Primary}, req.Secondaries...)); err != nil {
		return SharedRunResult{Err: err}
	}
	for _, sec := range req.Secondaries {
		if _, isPark := sec.(strategy.ResidualProvider); isPark {
			return SharedRunResult{Err: fmt.Errorf("%s is a park: pass its symbol as DefaultAsset", sec.ID())}
		}
	}

	outDir := req.OutDir
	if outDir == "" {
		outDir = appenv.Reports()
	}
	calcDirPath := req.CalcDir
	if calcDirPath == "" {
		var cleanupCalc func()
		calcDirPath, cleanupCalc = calcDir(outDir)
		defer cleanupCalc()
	}
	defaultAsset := strings.ToUpper(strings.TrimSpace(req.DefaultAsset))

	var results *storage.ResultsDB
	var outDBPath string
	if req.Persist {
		var err error
		results, err = storage.ResultsFor(outDir, "stack")
		if err != nil {
			return SharedRunResult{Err: fmt.Errorf("failed to open results database for shared account: %w", err)}
		}
		outDBPath = results.Path
	}

	allSignals := req.Signals
	if allSignals == nil {
		if err := os.MkdirAll(calcDirPath, 0755); err != nil {
			return SharedRunResult{Err: fmt.Errorf("failed to create calc dir %s: %w", calcDirPath, err)}
		}
		req.Primary.SetDatabases(req.MarketDBPath, freshCalcFile(filepath.Join(calcDirPath, fmt.Sprintf("calc_%s.db", req.Primary.ID()))))
		primSignals := req.Primary.GenerateSignals(req.BarsBySymbol)
		for i := range primSignals {
			primSignals[i].StrategyID = req.Primary.ID()
			primSignals[i].Priority = 0
		}
		allSignals = append(allSignals, primSignals...)

		for secIdx, sec := range req.Secondaries {
			sec.SetDatabases(req.MarketDBPath, freshCalcFile(filepath.Join(calcDirPath, fmt.Sprintf("calc_%s.db", sec.ID()))))
			secSignals := sec.GenerateSignals(req.BarsBySymbol)
			for i := range secSignals {
				secSignals[i].StrategyID = sec.ID()
				secSignals[i].Priority = secIdx + 1
			}
			allSignals = append(allSignals, secSignals...)
		}
	}

	symUpper := strings.ToUpper(strings.TrimSpace(req.SymbolFilter))
	if symUpper != "" {
		var filtered []models.Signal
		for _, s := range allSignals {
			if strings.ToUpper(s.Symbol) == symUpper {
				filtered = append(filtered, s)
			}
		}
		allSignals = filtered
	}

	entries := []StrategyPriorityEntry{
		{Strategy: req.Primary, Priority: 0, Config: req.Override.Apply(req.Primary.DefaultConfig())},
	}
	for secIdx, sec := range req.Secondaries {
		entries = append(entries, StrategyPriorityEntry{
			Strategy: sec,
			Priority: secIdx + 1,
			Config:   req.Override.Apply(sec.DefaultConfig()),
		})
	}

	bmSymbol := req.Primary.DefaultConfig().Benchmark
	if bmSymbol == "" {
		bmSymbol = "SPY"
	}
	var bmMap map[string]models.Bar
	if bBars, hasBm := req.BarsBySymbol[bmSymbol]; hasBm {
		bmMap = make(map[string]models.Bar)
		for _, b := range bBars {
			bmMap[b.Date] = b
		}
	}

	suspect := splitSuspect(req.MarketDBPath, signalSymbols(allSignals), req.SortedDates)
	ran, serr := simulateStack(entries, allSignals, req.BarsBySymbol, req.SortedDates, req.Capital, req.MarketDBPath, defaultAsset, bmMap)
	if serr != nil {
		return SharedRunResult{Err: serr}
	}
	combinedReport, perStratReports, trades, equityCurve := ran.combined, ran.perStrategy, ran.trades, ran.curve
	combinedID := SharedAccountID(req.Primary, req.Secondaries)
	idle := analytics.CalculateIdleStats(equityCurve)
	parked := ran.parked
	var sleeveNet float64
	for _, t := range trades {
		sleeveNet += t.NetPnL
	}
	parkContribution := combinedReport.FinalEquity - combinedReport.InitialCapital - sleeveNet

	var runID int64
	if req.Persist && results != nil {
		reports := []storage.NamedReport{{StrategyID: combinedID, Report: combinedReport}}
		for sID, rep := range perStratReports {
			reports = append(reports, storage.NamedReport{StrategyID: sID, Report: rep})
		}
		audit := storage.SharedAccountAudit{
			CombinedID:          combinedID,
			AccountModel:        "Shared Cash Ledger with Dynamic Priority Preemption",
			PrimaryStrategyID:   req.Primary.ID(),
			PrimaryStrategyName: req.Primary.Name(),
			PositionSizePct:     req.Override.AllocPct,
			PreemptedTrades:     ran.preempted,
			AvgIdleCashPct:      idle.AvgCashPct,
			FullyIdlePct:        idle.FullyIdlePct,
			AvgDeployedPct:      idle.AvgDeployedPct,
			ResultsDatabase:     outDBPath,
			DefaultAsset:        parked.Symbol,
			AvgDefaultPct:       parked.AvgWeight,
			DefaultDividends:    parked.Dividends,
			DaysUnparked:        parked.DaysUnparked,
		}
		priorities := []storage.SharedAccountPriority{{
			CombinedID: combinedID, StrategyID: req.Primary.ID(), StrategyName: req.Primary.Name(),
			Priority: 0, Role: "PRIMARY (P0)",
		}}
		for i, sec := range req.Secondaries {
			priorities = append(priorities, storage.SharedAccountPriority{
				CombinedID: combinedID, StrategyID: sec.ID(), StrategyName: sec.Name(),
				Priority: i + 1, Role: "SECONDARY (P1+)",
			})
		}
		var err error
		runID, err = results.WriteRun(storage.RunMeta{
			StrategyID: combinedID, Kind: "stack",
			WindowStart: combinedReport.StartDate, WindowEnd: combinedReport.EndDate,
			Capital: req.Capital, AllocPct: req.Override.AllocPct, DefaultAsset: parked.Symbol,
			MarketMaxDate: lastDate(req.SortedDates), Engine: ran.engine, Suspect: suspect,
		}, storage.RunPayload{
			Signals: allSignals, Trades: trades, Equity: equityCurve, Reports: reports,
			Audit: &audit, Priorities: priorities,
		})
		if err != nil {
			log.Printf("Warning: Failed to save shared account results to %s: %v", outDBPath, err)
		}
	}

	return SharedRunResult{
		Primary:            req.Primary,
		Secondaries:        req.Secondaries,
		CombinedID:         combinedID,
		CombinedReport:     combinedReport,
		PerStrategyReports: perStratReports,
		Trades:             trades,
		EquityCurve:        equityCurve,
		Signals:            allSignals,
		DbPath:             outDBPath,
		RunID:              runID,
		PreemptedCount:     ran.preempted,
		Idle:               idle,
		AllocPct:           req.Override.AllocPct,
		Default:            parked,
		ParkContribution:   parkContribution,
		Engine:             ran.engine,
		Suspect:            suspect,
	}
}

// PrintSharedAccountTearSheet displays the consolidated portfolio and strategy attribution tables.
func PrintSharedAccountTearSheet(res SharedRunResult) {
	fmt.Printf("\n========================================================================================================================\n")
	fmt.Printf("🏛️ SHARED-ACCOUNT MULTI-STRATEGY QUANTITATIVE AUDIT\n")
	fmt.Printf("   Account Model:      Shared Cash Ledger with Dynamic Priority Preemption\n")
	fmt.Printf("   Primary Strategy:   %s (ID: %s) [Priority 0 - Capital Precedence]\n", res.Primary.Name(), res.Primary.ID())
	for i, sec := range res.Secondaries {
		fmt.Printf("   Secondary Strategy: %s (ID: %s) [Priority %d - Idle Cash Utilization]\n", sec.Name(), sec.ID(), i+1)
	}
	if res.AllocPct > 0 {
		fmt.Printf("   Position Size:      %.0f%% of equity per position\n", res.AllocPct*100)
	}
	fmt.Printf("   Preempted Trades:   %d secondary positions liquidated to obey primary signals\n", res.PreemptedCount)
	fmt.Printf("   Idle Cash:          avg %.1f%% of equity (%d idle days, fully flat %.1f%% of days, deployed %.1f%%)\n",
		res.Idle.AvgCashPct*100, res.Idle.DaysFullyIdle, res.Idle.FullyIdlePct*100, res.Idle.AvgDeployedPct*100)
	if res.Default.Symbol != "" {
		fmt.Printf("   Default Asset:      %s, avg %.1f%% of equity, dividends $%.2f, %d days with no park shares\n",
			res.Default.Symbol, res.Default.AvgWeight*100, res.Default.Dividends, res.Default.DaysUnparked)
		fmt.Printf("   Park Contribution:  $%.2f (ending equity minus start minus sleeve net PnL)\n", res.ParkContribution)
	}
	fmt.Printf("   Results Database:   %s\n", res.DbPath)
	fmt.Printf("========================================================================================================================\n")

	// Print Consolidated Tear Sheet
	PrintPerformanceTearSheet("Portfolio", res.CombinedReport)

	// Strategy Attribution Breakdown Table
	fmt.Printf("\n========================================================================================================================\n")
	fmt.Printf("📊 STRATEGY CONTRIBUTION & PREEMPTION BREAKDOWN\n")
	fmt.Printf("========================================================================================================================\n")

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Role", "Strategy ID", "Total Trades", "Wins / Losses", "Win Rate", "Preempted", "Net Realized PnL", "CAGR", "Sharpe", "Max DD", "DD Duration"})
	table.SetBorder(true)
	table.SetAutoWrapText(false)

	// Primary
	primID := res.Primary.ID()
	primRep := res.PerStrategyReports[primID]
	table.Append([]string{
		"PRIMARY (P0)",
		primID,
		fmt.Sprintf("%d", primRep.TotalTrades),
		fmt.Sprintf("%d / %d", primRep.WinningTrades, primRep.LosingTrades),
		fmt.Sprintf("%.1f%%", primRep.WinRate*100),
		"0 (Never)",
		fmt.Sprintf("$%.2f", primRep.NetProfit),
		fmt.Sprintf("%.2f%%", primRep.CAGR*100),
		fmt.Sprintf("%.2f", primRep.SharpeRatio),
		fmt.Sprintf("%.2f%%", primRep.MaxDrawdownPct*100),
		fmt.Sprintf("%dd", primRep.MaxDrawdownDuration),
	})

	// Secondaries
	for _, sec := range res.Secondaries {
		secID := sec.ID()
		secRep := res.PerStrategyReports[secID]

		secPreempted := 0
		for _, t := range res.Trades {
			if t.StrategyID == secID && t.ExitReason == models.ExitReasonPreempted {
				secPreempted++
			}
		}

		table.Append([]string{
			"SECONDARY (P1+)",
			secID,
			fmt.Sprintf("%d", secRep.TotalTrades),
			fmt.Sprintf("%d / %d", secRep.WinningTrades, secRep.LosingTrades),
			fmt.Sprintf("%.1f%%", secRep.WinRate*100),
			fmt.Sprintf("%d Trades", secPreempted),
			fmt.Sprintf("$%.2f", secRep.NetProfit),
			fmt.Sprintf("%.2f%%", secRep.CAGR*100),
			fmt.Sprintf("%.2f", secRep.SharpeRatio),
			fmt.Sprintf("%.2f%%", secRep.MaxDrawdownPct*100),
			fmt.Sprintf("%dd", secRep.MaxDrawdownDuration),
		})
	}

	table.Render()

	// Print Preemption Audit Table if any positions were preempted
	var preemptedTrades []models.Trade
	for _, t := range res.Trades {
		if t.ExitReason == models.ExitReasonPreempted {
			preemptedTrades = append(preemptedTrades, t)
		}
	}

	if len(preemptedTrades) > 0 {
		fmt.Printf("\n⚡ PREEMPTED POSITIONS AUDIT (Liquidated at Market to Fund Primary Signals):\n")
		pTable := tablewriter.NewWriter(os.Stdout)
		pTable.SetHeader([]string{"Trade ID", "Strategy", "Symbol", "Entry Date", "Entry Price", "Exit Date", "Exit Price", "Hold Days", "Preemption PnL", "Return %"})
		pTable.SetBorder(true)

		for _, t := range preemptedTrades {
			pTable.Append([]string{
				fmt.Sprintf("%d", t.ID),
				t.StrategyID,
				t.Symbol,
				t.EntryDate,
				fmt.Sprintf("$%.2f", t.EntryPrice),
				t.ExitDate,
				fmt.Sprintf("$%.2f", t.ExitPrice),
				fmt.Sprintf("%d d", t.HoldDays),
				fmt.Sprintf("$%.2f", t.NetPnL),
				fmt.Sprintf("%.2f%%", t.ReturnPct*100),
			})
		}
		pTable.Render()
	}

	// Print Sample Recent Trades
	PrintTradesTable(res.Trades, "")
}
