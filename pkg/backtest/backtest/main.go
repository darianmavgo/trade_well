package backtest

import (
	"flag"
	"fmt"
	"github.com/darianmavgo/trade_well/pkg/backtest/export_views"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/lastbacktest"

	"github.com/darianmavgo/trade_well/pkg/backtest/analytics"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/reliability"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	_ "modernc.org/sqlite"
)

// backtestStart is the -start flag value: the default backtest window start.
var backtestStart string

// backtestEnd is the -end flag value: the last bar date to load ("" = latest).
var backtestEnd string

// Config holds the settings of a run.
type Config struct {
	Db                  string   // -db
	Table               string   // -table
	Strategy            string   // -strategy
	SharedAccount       bool     // -shared-account
	Primary             string   // -primary
	Secondary           string   // -secondary
	StrategiesDb        string   // -strategies-db (lastrun: the database that gets strategy_last_backtest)
	OutDir              string   // -out-dir (the reports root: each run gets a numbered folder in it)
	RunID               int      // -run-id
	CPUProfile          string   // -cpuprofile
	List                bool     // -strategylist
	Symbol              string   // -symbol
	Capital             float64  // -capital
	MaxPositions        int      // -max-positions
	Stoploss            float64  // -stoploss
	Target              float64  // -target
	Hold                int      // -hold
	Alloc               float64  // -alloc
	Html                string   // -html
	AutoDownload        bool     // -auto-download
	DownloadYears       int      // -download-years
	Concurrency         int      // -concurrency
	Serial              bool     // -serial
	KeepCalc            bool     // -keep-calc
	Force               bool     // -force
	GridsearchDb        string   // -gridsearch-db
	IncludeUniverse     bool     // -include-universe
	StackDepth          int      // -stack-depth
	PersistBest         bool     // -persist-best
	Start               string   // -start
	End                 string   // -end
	HoldoutMonths       int      // -holdout-months
	SignalsOnly         bool     // -signals-only
	Bars                int      // -bars
	Otm                 float64  // -otm
	Commission          float64  // -commission
	OptSlip             float64  // -opt-slip
	NoReinvestDividends bool     // -no-reinvest-dividends
	DefaultAsset        string   // -default-asset
	Mode                string   // subcommand (empty = default)
	Args                []string // positional arguments

	outcome *stackEvalOutcome         // set by runOnce for stack-eval
	phase   string                    // "in-sample" or "out-of-sample" for the two holdout passes, "" for one pass
	prior   *models.PerformanceReport // the in-sample report, set for the out-of-sample pass
}

// DefaultConfig returns the CLI defaults.
func DefaultConfig() Config {
	return Config{
		Db:                  cliutils.GetDefaultMarketDB(),
		Table:               "backtest_start",
		Strategy:            "",
		SharedAccount:       false,
		Primary:             "",
		Secondary:           "",
		OutDir:              appenv.Reports(),
		StrategiesDb:        appenv.RefDB(),
		List:                false,
		Symbol:              "",
		Capital:             100000.0,
		MaxPositions:        0,
		Stoploss:            0.0,
		Target:              0.0,
		Hold:                0,
		Alloc:               0,
		Html:                appenv.ReportFile("backtest_report.html"),
		AutoDownload:        true,
		DownloadYears:       5,
		Concurrency:         runtime.NumCPU(),
		Force:               false,
		GridsearchDb:        "",
		IncludeUniverse:     false,
		StackDepth:          3,
		PersistBest:         true,
		Start:               storage.DefaultStartDate,
		End:                 "",
		HoldoutMonths:       DefaultHoldoutMonths,
		SignalsOnly:         false,
		Bars:                0,
		Otm:                 2.0,
		Commission:          0.65,
		OptSlip:             0.05,
		NoReinvestDividends: false,
		DefaultAsset:        "",
	}
}

// runOverride is the CLI's optional replacement for each strategy's DefaultConfig.
// Alloc 0.10 sizes every position at 10% of equity.
func runOverride(conf Config) runner.ConfigOverride {
	return runner.ConfigOverride{
		StopLoss:     conf.Stoploss,
		Target:       conf.Target,
		Hold:         conf.Hold,
		MaxPositions: conf.MaxPositions,
		AllocPct:     conf.Alloc,
	}
}

// runSettings collects the flags a report states up front. The window falls
// back to the first and last loaded bar when -start / -end were not given.
func runSettings(conf Config, command string, dates []string) runner.RunSettings {
	start, end := conf.Start, conf.End
	if len(dates) > 0 {
		if start == "" {
			start = dates[0]
		}
		if end == "" {
			end = dates[len(dates)-1]
		}
	}
	return runner.RunSettings{
		Command:      command,
		MarketDB:     conf.Db,
		Table:        conf.Table,
		Symbols:      conf.Symbol,
		Start:        start,
		End:          end,
		HoldoutMonth: conf.HoldoutMonths,
		Capital:      conf.Capital,
		DefaultAsset: conf.DefaultAsset,
		Override:     runOverride(conf),
	}
}

// validateAlloc rejects values the sizer would silently rewrite. AllocationPct
// is a fraction of equity: 10% per position is 0.10. A value above 1 (such as
// 10) is not 10%; FixedPctSizer would replace it with 20%.
func validateAlloc(alloc float64) error {
	if alloc == 0 {
		return nil
	}
	if alloc <= 0 || alloc > 1 {
		return fmt.Errorf("-alloc must be a fraction of equity in (0, 1]; 10%% per position is -alloc 0.10 (got %v)", alloc)
	}
	return nil
}

// validateDefaultAsset rejects a park symbol on a run that has no shared ledger.
func validateDefaultAsset(asset string, shared bool) error {
	if strings.TrimSpace(asset) == "" {
		return nil
	}
	if !shared {
		return fmt.Errorf("-default-asset %s applies to a shared-account run (-primary and -secondary)", strings.ToUpper(strings.TrimSpace(asset)))
	}
	return nil
}

// Main is the CLI entry point.
func Main() {
	// `backtest export_views` has its own flags; dispatch before this command registers its own.
	if len(os.Args) > 1 && os.Args[1] == "export_views" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
		export_views.Main()
		return
	}
	// Subcommand dispatch:
	//   backtest export_views -> write the indicator calculations as SQL views over
	//                          backtest_start (a .sql script, optionally in a copy of the market DB)
	//   backtest stale      -> assess which strategies' cached reports/*.db
	//                          results are stale (unregistered strategy,
	//                          newer market data, or an edited SQL pipeline
	//                          since the result was made) and exit — no
	//                          backtests run
	//   backtest covered-call -> hold -symbol (default VOO) and sell a monthly
	//                          call; needs `market_history -source polygon-options`
	//   backtest stack      -> rank existing strategies as idle-cash overlays
	//                          on one primary, one shared cash ledger
	conf := DefaultConfig()
	d := conf
	flag.StringVar(&conf.Db, "db", d.Db, "Path to source SQLite DB containing historical market bars")
	flag.StringVar(&conf.Table, "table", d.Table, "Table name containing historical bars")
	flag.StringVar(&conf.Strategy, "strategy", d.Strategy, "Strategy ID to run, comma-separated list, 'all', or 'strat1+strat2' for shared account")
	var family string
	flag.StringVar(&family, "family", "", "Run every strategy of one row-backed family (streak, hold, tree, markov, hmm, rotation); same as -strategy <family>")
	flag.BoolVar(&conf.SharedAccount, "shared-account", d.SharedAccount, "Run strategies in a single shared cash account with priority preemption")
	flag.StringVar(&conf.Primary, "primary", d.Primary, "Primary strategy ID for shared-account execution (has capital priority)")
	flag.StringVar(&conf.Secondary, "secondary", d.Secondary, "Secondary strategy ID(s) for shared-account execution (comma-separated)")
	flag.StringVar(&conf.StrategiesDb, "strategies-db", d.StrategiesDb, "(lastrun) strategies database that receives the strategy_last_backtest table")
	flag.StringVar(&conf.OutDir, "out-dir", d.OutDir, "Reports root. Each backtest run writes to a new numbered folder here, <out-dir>/<run_id>/<family>.db, and held-out results to <run_id>/oos/")
	flag.StringVar(&conf.CPUProfile, "cpuprofile", d.CPUProfile, "Write a CPU profile of the run to this file (read it with go tool pprof)")
	flag.IntVar(&conf.RunID, "run-id", d.RunID, "Use this existing run folder instead of starting a new one: finish an interrupted run (strategies already done in it are skipped unless -force) or read it (stale)")
	flag.BoolVar(&conf.List, "strategylist", d.List, "Print strategy counts per family, then ask whether to dump every strategy")
	flag.StringVar(&conf.Symbol, "symbol", d.Symbol, "Optional: Filter backtest to a specific symbol (e.g. DFEN, SOXL)")
	flag.Float64Var(&conf.Capital, "capital", d.Capital, "Starting portfolio capital for simulation")
	flag.IntVar(&conf.MaxPositions, "max-positions", d.MaxPositions, "Optional override: Maximum concurrent open positions allowed")
	flag.Float64Var(&conf.Stoploss, "stoploss", d.Stoploss, "Optional override: Stop-loss floor multiplier (e.g. 0.93 for -7%)")
	flag.Float64Var(&conf.Target, "target", d.Target, "Optional override: Take-profit multiplier (e.g. 1.18 for +18%)")
	flag.IntVar(&conf.Hold, "hold", d.Hold, "Optional override: Max holding days window")
	flag.Float64Var(&conf.Alloc, "alloc", d.Alloc, "Fraction of equity per position (0.10 = 10%). 0 keeps each strategy's own allocation. Applies to standalone runs, shared-account stacks, and stack-eval.")
	flag.StringVar(&conf.Html, "html", d.Html, "Path to export interactive HTML dashboard report")
	flag.BoolVar(&conf.AutoDownload, "auto-download", d.AutoDownload, "Automatically detect missing market data and run download")
	flag.IntVar(&conf.DownloadYears, "download-years", d.DownloadYears, "Number of years of history to fetch when downloading missing data")
	flag.IntVar(&conf.Concurrency, "concurrency", d.Concurrency, "Max concurrent strategies when running more than one (defaults to all CPU cores; bounds memory use for large -strategy all runs)")
	flag.BoolVar(&conf.Serial, "serial", d.Serial, "Run one strategy at a time instead of -concurrency workers writing their family result databases in parallel")
	flag.BoolVar(&conf.KeepCalc, "keep-calc", d.KeepCalc, "Keep each SQL strategy's calculation database (slice tables) under <out-dir>/calc/ instead of deleting it after the run")
	flag.BoolVar(&conf.Force, "force", d.Force, "(multi-strategy runs only) with -run-id, redo every strategy even if it already has a usable result in that run")
	flag.StringVar(&conf.GridsearchDb, "gridsearch-db", d.GridsearchDb, "(optimized subcommand only) SQLite DB of gridsearch results to read best configs from")
	flag.BoolVar(&conf.IncludeUniverse, "include-universe", d.IncludeUniverse, "(stack-eval) also try full-universe overlays (bb-capitulation, rsi2, ...)")
	flag.IntVar(&conf.StackDepth, "stack-depth", d.StackDepth, "(stack-eval) greedy complementary overlays to combine after pairwise ranking")
	flag.BoolVar(&conf.PersistBest, "persist-best", d.PersistBest, "(stack-eval) write a shared_*.db for the greedy N-way stack")
	flag.StringVar(&conf.Start, "start", d.Start, "Earliest bar date (YYYY-MM-DD) to simulate; earlier bars are only used for SMA warmup. Empty = full history")
	flag.StringVar(&conf.End, "end", d.End, "Last bar date (YYYY-MM-DD) to simulate. Empty = latest bar")
	flag.IntVar(&conf.HoldoutMonths, "holdout-months", d.HoldoutMonths, "Months at the end of history kept out of the main run and reported separately as out-of-sample (0 = use all history). Applies to plain runs and stack-eval")
	flag.BoolVar(&conf.SignalsOnly, "signals-only", d.SignalsOnly, "Skip portfolio simulation; run the same GenerateSignals live window as cmd/livescan (tip bar → next session)")
	flag.IntVar(&conf.Bars, "bars", d.Bars, "(with -signals-only) recent bars per symbol; 0 = strategy MinHistoryBars")
	flag.Float64Var(&conf.Otm, "otm", d.Otm, "(covered-call) target call strike as % above spot at each monthly roll")
	flag.Float64Var(&conf.Commission, "commission", d.Commission, "(covered-call) $ per option contract sold (IBKR tiered ≈ $0.65)")
	flag.Float64Var(&conf.OptSlip, "opt-slip", d.OptSlip, "(covered-call) $ per share given up vs the last-trade option price when selling")
	flag.BoolVar(&conf.NoReinvestDividends, "no-reinvest-dividends", d.NoReinvestDividends, "Total-return strategies (e.g. schd-buy-hold): take dividends as idle cash instead of reinvesting them")
	flag.StringVar(&conf.DefaultAsset, "default-asset", d.DefaultAsset, "Shared-account only: symbol that leftover cash is held in after each session (e.g. GOOGL)")
	conf.Mode = cliutils.PopSubcommand(map[string]string{"covered-call": "covered-call", "stale": "stale", "stack": "stack-eval", "newrun": "newrun", "lastrun": "lastrun"})
	flag.Parse()
	conf.Args = flag.Args()
	if family != "" {
		if conf.Strategy != "" {
			log.Fatal("-family and -strategy are exclusive")
		}
		conf.Strategy = family
	}
	defer storage.CloseSharedResults()
	if err := appenv.RequireHourly(conf.Db); err != nil {
		log.Fatal(err)
	}
	if err := Run(conf); err != nil {
		log.Fatal(err)
	}
}

// runOnce executes one pass over [conf.Start, conf.End]. For stack-eval it
// stores the stack it found in conf.outcome so the caller can test it out of
// sample.
func runOnce(conf Config) error {
	var reliabilityGroups []models.ParamGroup // shown first in the HTML report
	backtestStart = conf.Start
	backtestEnd = conf.End
	reinvestDividends := !conf.NoReinvestDividends
	if err := validateAlloc(conf.Alloc); err != nil {
		return err
	}

	if conf.Mode == "covered-call" {
		ccStart := conf.Start
		if ccStart == storage.DefaultStartDate {
			ccStart = "" // option history is only ~2y; begin at the first rollable month
		}
		if err := runCoveredCallCommand(conf.Db, conf.Table, strings.ToUpper(conf.Symbol), conf.Capital, conf.Otm, ccStart, conf.Commission, conf.OptSlip); err != nil {
			return err
		}
		return nil
	}

	if conf.Mode == "lastrun" {
		// Record, per strategy, the result DB holding its most recent backtest.
		_, err := lastbacktest.Run(lastbacktest.Config{
			StrategiesDB: conf.StrategiesDb, ReportsDir: conf.OutDir, BaseDir: appenv.Folder(),
			Workers: conf.Concurrency, Out: os.Stdout,
		})
		return err
	}

	if conf.Mode == "newrun" {
		// Start a run folder and print only its number, for a script to pass to -run-id.
		id, _, err := storage.NewRun(appenv.Reports())
		if err != nil {
			return err
		}
		fmt.Println(id)
		return nil
	}

	// Ensure HTML reports land in reports/ directory
	conf.Html = appenv.ReportFile(conf.Html)

	// Auto-discover the strategy pipelines in sql/strategies/
	stratreg.RegisterAll(appenv.Folder(), conf.Db)

	if !conf.List {
		runner.PrintMarketSources(os.Stdout, conf.Db, conf.Table)
	}

	if conf.Mode == "stale" {
		if err := runStaleCommand(conf.OutDir, conf.Db, conf.Concurrency); err != nil {
			return err
		}
		return nil
	}

	if conf.Mode == "optimized" {
		if conf.GridsearchDb == "" {
			// This run's own folder is brand new unless -run-id named an existing one,
			// so the sweep to read must be named: -run-id of the run that holds it.
			if conf.RunID == 0 {
				return fmt.Errorf("optimized: pass -run-id (the run folder holding gridsearch.db) or -gridsearch-db")
			}
			conf.GridsearchDb = filepath.Join(conf.OutDir, "gridsearch.db")
		}
		optArg := strings.TrimSpace(conf.Strategy)
		if optArg == "" && len(conf.Args) > 0 {
			optArg = strings.Join(conf.Args, ",")
		}
		if err := runOptimizedCommand(optArg, conf.Db, conf.Table, conf.OutDir, conf.GridsearchDb, conf.Capital, conf.Symbol, conf.AutoDownload, conf.DownloadYears, conf.Concurrency, !conf.NoReinvestDividends, conf.Alloc); err != nil {
			return err
		}
		return nil
	}

	if conf.Mode == "stack-eval" {
		primaryID := strings.TrimSpace(conf.Primary)
		if primaryID == "" {
			primaryID = strings.TrimSpace(conf.Strategy)
		}
		if primaryID == "" && len(conf.Args) > 0 {
			primaryID = strings.TrimSpace(conf.Args[0])
		}
		out, err := runStackEvalCommand(
			primaryID,
			parseSecondaryList(conf.Secondary),
			conf.IncludeUniverse,
			conf.StackDepth, conf.Concurrency,
			conf.Db, conf.Table, conf.OutDir,
			conf.Capital, conf.Symbol,
			conf.AutoDownload, conf.DownloadYears,
			conf.PersistBest,
			runOverride(conf),
		)
		if err != nil {
			return err
		}
		if conf.outcome != nil {
			*conf.outcome = out
		}
		return nil
	}

	if conf.List {
		runner.PrintStrategyList(os.Stdout, os.Stdin)
		return nil
	}

	// Resolve strategies from flags or positional arguments
	stratArg := strings.TrimSpace(conf.Strategy)
	posArgs := conf.Args
	if stratArg == "" && len(posArgs) > 0 {
		stratArg = strings.Join(posArgs, ",")
	}

	// -signals-only: same path as cmd/livescan (wrapper around RunSignalScan).
	if conf.SignalsOnly {
		if conf.SharedAccount || conf.Primary != "" || conf.Secondary != "" || strategy.IsStack(stratArg) {
			return fmt.Errorf("-signals-only does not support shared-account / + stacks; use livescan or plain -strategy lists")
		}
		selected, err := runner.ResolveStrategies(stratArg, "bb-capitulation")
		if err != nil {
			return fmt.Errorf("%v", err)
		}
		fmt.Printf("\n📡 backtest -signals-only → runner.RunSignalScan (same as livescan)\n")
		res, err := runner.RunSignalScan(runner.SignalScanOptions{
			Out:           os.Stdout,
			MarketDB:      conf.Db,
			Table:         conf.Table,
			Strategies:    selected,
			SymbolFilter:  conf.Symbol,
			BarsLimit:     conf.Bars,
			AutoDownload:  conf.AutoDownload,
			DownloadYears: conf.DownloadYears,
			Concurrency:   conf.Concurrency,
			OutDir:        conf.OutDir,
		})
		if err != nil {
			return fmt.Errorf("signals-only: %v", err)
		}
		fmt.Printf("📅 Tip/as-of %s → next session %s (%d symbols)\n\n", res.AsOf, res.NextSession, res.SymbolsLoaded)
		entering := 0
		for _, r := range res.Rows {
			mark := "NO_SIGNAL"
			if r.Status == "ENTER" {
				mark = "ENTER"
				entering++
			}
			fmt.Printf("  %-28s %-10s %s\n", r.StrategyID, mark, r.Symbols)
		}
		fmt.Printf("\n%d/%d ENTER · wrote %s\n", entering, len(res.Rows), res.OutDBPath)
		return nil
	}

	// Detect if user requested Shared Account Mode
	isSharedAccount := conf.SharedAccount || conf.Primary != "" || conf.Secondary != "" || strategy.IsStack(stratArg)
	defaultAsset := strings.ToUpper(strings.TrimSpace(conf.DefaultAsset))
	if err := validateDefaultAsset(defaultAsset, isSharedAccount); err != nil {
		return err
	}

	if isSharedAccount {
		var primaryStrat strategy.Strategy
		var secondaryStrats []strategy.Strategy

		if conf.Primary != "" {
			s, exists := strategy.Get(conf.Primary)
			if !exists {
				return fmt.Errorf("Primary strategy '%s' not found in registry.", conf.Primary)
			}
			primaryStrat = s

			if conf.Secondary != "" {
				secTokens := strings.FieldsFunc(conf.Secondary, func(r rune) bool { return r == ',' || r == ' ' })
				for _, tok := range secTokens {
					sec, exists := strategy.Get(strings.TrimSpace(tok))
					if !exists {
						return fmt.Errorf("Secondary strategy '%s' not found in registry.", tok)
					}
					secondaryStrats = append(secondaryStrats, sec)
				}
			}
		} else if strategy.IsStack(stratArg) {
			parts := strings.Split(stratArg, "+")
			pID := strings.TrimSpace(parts[0])
			pStrat, exists := strategy.Get(pID)
			if !exists {
				return fmt.Errorf("Primary strategy '%s' not found in registry.", pID)
			}
			primaryStrat = pStrat

			for _, p := range parts[1:] {
				sID := strings.TrimSpace(p)
				sStrat, exists := strategy.Get(sID)
				if !exists {
					return fmt.Errorf("Secondary strategy '%s' not found in registry.", sID)
				}
				secondaryStrats = append(secondaryStrats, sStrat)
			}
		} else {
			// Spliced from -strategy comma-separated list
			tokens := strings.FieldsFunc(stratArg, func(r rune) bool { return r == ',' || r == ' ' })
			if len(tokens) < 2 {
				return fmt.Errorf("Shared account mode requires at least 2 strategies (primary + secondary).")
			}
			pStrat, exists := strategy.Get(strings.TrimSpace(tokens[0]))
			if !exists {
				return fmt.Errorf("Primary strategy '%s' not found in registry.", tokens[0])
			}
			primaryStrat = pStrat

			for _, tok := range tokens[1:] {
				sStrat, exists := strategy.Get(strings.TrimSpace(tok))
				if !exists {
					return fmt.Errorf("Secondary strategy '%s' not found in registry.", tok)
				}
				secondaryStrats = append(secondaryStrats, sStrat)
			}
		}

		// A park-<symbol> member is the residual book, not a sleeve.
		sleeves, parkSym, err := strategy.SplitResidual(append([]strategy.Strategy{primaryStrat}, secondaryStrats...))
		if err != nil {
			return err
		}
		secondaryStrats = sleeves[1:]
		if parkSym != "" {
			if defaultAsset != "" && defaultAsset != parkSym {
				return fmt.Errorf("-default-asset %s conflicts with stack member park-%s", defaultAsset, strings.ToLower(parkSym))
			}
			defaultAsset = parkSym
		}
		allStrats := append([]strategy.Strategy{primaryStrat}, secondaryStrats...)

		// Detect missing market data and download
		if err := runner.DetectAndDownloadMissingData(conf.Db, conf.Table, allStrats, conf.Symbol, conf.AutoDownload, conf.DownloadYears); err != nil {
			return fmt.Errorf("Market data resolution error: %v", err)
		}

		db, err := storage.OpenSQLite(conf.Db)
		if err != nil {
			return fmt.Errorf("Failed to open source DB %s: %v", conf.Db, err)
		}
		defer db.Close()

		// Always include SPY: ExecuteStack falls back to it as the
		// benchmark when a strategy's own DefaultConfig().Benchmark is empty,
		// and RequiredSymbolsFor only picks up non-empty benchmarks.
		reqSymbols := append(runner.RequiredSymbolsFor(allStrats, conf.Symbol), "SPY")
		if defaultAsset != "" {
			reqSymbols = append(reqSymbols, defaultAsset)
		}
		fmt.Printf("\n⚙️ Loading bars for %v from table '%s' for Shared-Account Simulation (Starting Capital: $%.2f)...\n", reqSymbols, conf.Table, conf.Capital)
		if conf.Alloc > 0 {
			fmt.Printf("   Allocation: %.0f%% of equity per position\n", conf.Alloc*100)
		}
		barsBySymbol, sortedDates, err := storage.FetchBars(db, conf.Table, reqSymbols, backtestStart, backtestEnd)
		if err != nil {
			return fmt.Errorf("Error loading historical bars for simulation: %v", err)
		}
		if defaultAsset != "" && len(barsBySymbol[defaultAsset]) == 0 {
			return fmt.Errorf("-default-asset %s has no daily bars in %s", defaultAsset, conf.Db)
		}

		sharedRes := runner.ExecuteStack(runner.StackRequest{
			Primary:      primaryStrat,
			Secondaries:  secondaryStrats,
			BarsBySymbol: barsBySymbol,
			SortedDates:  sortedDates,
			Capital:      conf.Capital,
			SymbolFilter: conf.Symbol,
			OutDir:       conf.OutDir,
			MarketDBPath: conf.Db,
			Persist:      true,
			Override:     runOverride(conf),
			DefaultAsset: defaultAsset,
		})
		if sharedRes.Err != nil {
			return fmt.Errorf("Shared account backtest failed: %v", sharedRes.Err)
		}

		runner.PrintSharedAccountTearSheet(sharedRes)

		// Export HTML Report for Shared Account
		if conf.Html != "" {
			symUpper := strings.ToUpper(conf.Symbol)
			reportTitle := fmt.Sprintf("Shared Account Multi-Strategy Report: %s + %s", primaryStrat.Name(), secondaryStrats[0].Name())

			var stratReports []analytics.StrategyReportData
			eqCurves := make(map[string][]float64)
			ddCurves := make(map[string][]float64)

			// 1. Shared Account Combined
			stratReports = append(stratReports, analytics.StrategyReportData{
				ID:     sharedRes.CombinedID,
				Name:   fmt.Sprintf("Consolidated Shared Account (%s + %s)", primaryStrat.Name(), secondaryStrats[0].Name()),
				Type:   "Portfolio",
				Report: sharedRes.CombinedReport,
				Trades: sharedRes.Trades,
			})
			var eqSeries, ddSeries []float64
			for _, pt := range sharedRes.EquityCurve {
				eqSeries = append(eqSeries, pt.TotalEquity)
				ddSeries = append(ddSeries, pt.DrawdownPct)
			}
			eqCurves[sharedRes.CombinedID] = eqSeries
			ddCurves[sharedRes.CombinedID] = ddSeries

			// 2. Primary Strategy Attribution
			pRep := sharedRes.PerStrategyReports[primaryStrat.ID()]
			var pTrades []models.Trade
			for _, t := range sharedRes.Trades {
				if t.StrategyID == primaryStrat.ID() {
					pTrades = append(pTrades, t)
				}
			}
			stratReports = append(stratReports, analytics.StrategyReportData{
				ID:     primaryStrat.ID(),
				Name:   primaryStrat.Name() + " (Primary)",
				Type:   "Primary",
				Report: pRep,
				Trades: pTrades,
			})

			// 3. Secondary Strategies Attribution
			for _, sec := range secondaryStrats {
				sRep := sharedRes.PerStrategyReports[sec.ID()]
				var sTrades []models.Trade
				for _, t := range sharedRes.Trades {
					if t.StrategyID == sec.ID() {
						sTrades = append(sTrades, t)
					}
				}
				stratReports = append(stratReports, analytics.StrategyReportData{
					ID:     sec.ID(),
					Name:   sec.Name() + " (Secondary)",
					Type:   "Secondary",
					Report: sRep,
					Trades: sTrades,
				})
			}

			stackSettings := runSettings(conf, "backtest stack", sortedDates)
			stackSettings.DefaultAsset = defaultAsset
			htmlData := analytics.MultiStrategyHTMLData{
				Title:          reportTitle,
				GeneratedAt:    time.Now().Format("2006-01-02 15:04:05 MST"),
				Symbol:         symUpper,
				StartDate:      sharedRes.CombinedReport.StartDate,
				EndDate:        sharedRes.CombinedReport.EndDate,
				TotalDays:      sharedRes.CombinedReport.TotalTradingDays,
				TotalYears:     sharedRes.CombinedReport.TotalCalendarYears,
				InitialCap:     conf.Capital,
				Strategies:     stratReports,
				AllDates:       sortedDates,
				EquityCurves:   eqCurves,
				DrawdownCurves: ddCurves,
				Params:         runner.DescribeRun(stackSettings, allStrats),
			}

			if err := analytics.GenerateComparisonHTML(conf.Html, htmlData); err != nil {
				log.Printf("Warning: Failed to generate HTML report %s: %v", conf.Html, err)
			} else {
				fmt.Printf("\n✨ Interactive HTML Report generated: %s\n\n", conf.Html)
			}
		}
		return nil
	}

	selectedStrategies, err := runner.ResolveStrategies(stratArg, "bb-capitulation")
	if err != nil {
		return fmt.Errorf("%v. Run with -strategylist to view available strategies.", err)
	}

	// For bulk runs (-strategy all, or a multi-symbol comma list), avoid duplicate
	// work by default: skip any strategy that already has a usable result in
	// -out-dir (same highest-increment-first, skip-if-compromised check used by
	// cmd/scoreboard). A single explicitly-named strategy always runs — that's
	// direct intent, not a bulk sweep. Pass -force to redo everything anyway.
	existing := map[string]runner.CompiledResult{}
	toRun := selectedStrategies
	if len(selectedStrategies) > 1 && !conf.Force {
		fmt.Println("🔎 Checking", conf.OutDir, "for strategies that already have a usable result...")
		existing, _, _, _, _ = runner.ScanAndValidate(conf.OutDir, conf.Concurrency)
		if n := runner.DropStale(existing); n > 0 {
			fmt.Printf("   %d earlier results were made by the daily simulator and are redone on the hourly one.\n", n)
		}
		toRun = nil
		for _, s := range selectedStrategies {
			if _, ok := existing[s.ID()]; !ok {
				toRun = append(toRun, s)
			}
		}
		fmt.Printf("   %d/%d strategies already have a usable result and will be skipped; %d need backtesting.\n\n",
			len(selectedStrategies)-len(toRun), len(selectedStrategies), len(toRun))
	}

	var results []runner.RunResult

	if len(toRun) == 0 {
		// Every selected strategy already has a usable result — nothing to
		// simulate, just report what's already there.
		fmt.Println("✅ Nothing to backtest — every selected strategy already has a usable result. (Use -force to redo everything.)")
		for _, s := range selectedStrategies {
			if c, ok := existing[s.ID()]; ok {
				results = append(results, runner.RunResult{Strat: s, Report: c.Report, DbPath: c.DbPath, Engine: c.Engine, Suspect: c.Suspect})
			}
		}
		runner.PrintComparisonTable(results)
		return nil
	}

	// Detect missing market data and download before running backtest (scoped to
	// what we're actually about to run).
	if err := runner.DetectAndDownloadMissingData(conf.Db, conf.Table, toRun, conf.Symbol, conf.AutoDownload, conf.DownloadYears); err != nil {
		return fmt.Errorf("Market data resolution error: %v", err)
	}

	// Open read-only historical bars from source DB
	db, err := storage.OpenSQLite(conf.Db)
	if err != nil {
		return fmt.Errorf("Failed to open source DB %s: %v", conf.Db, err)
	}
	defer db.Close()

	var barsBySymbol map[string][]models.Bar
	var sortedDates []string
	if len(selectedStrategies) == 1 {
		// Single strategy: fetch only the symbols it actually needs instead of
		// the entire multi-thousand-symbol database. Loading everything here
		// was a fixed ~20-30s tax paid on every single-strategy invocation
		// regardless of that strategy's own data footprint — e.g. `dt_fxr`
		// looked slow/wasteful because of its 19-year history, but the real
		// cost was this unconditional full-DB load, not FXR's own (correctly
		// scoped, 4866-row) simulation.
		reqSymbols := runner.RequiredSymbolsFor([]strategy.Strategy{toRun[0]}, conf.Symbol)
		fmt.Printf("\n⚙️ Loading bars for %v from table '%s' for Portfolio Simulation (Starting Capital: $%.2f)...\n", reqSymbols, conf.Table, conf.Capital)
		var fetchErr error
		barsBySymbol, sortedDates, fetchErr = storage.FetchBars(db, conf.Table, reqSymbols, backtestStart, backtestEnd)
		if fetchErr != nil {
			return fmt.Errorf("Error loading historical bars for simulation: %v", fetchErr)
		}
	}

	if len(selectedStrategies) == 1 {
		// A single explicitly-named strategy always takes this detailed path
		// (toRun == selectedStrategies here since skip-filtering only applies
		// when more than one strategy was selected).
		strat := toRun[0]
		cfg := runOverride(conf).Apply(strat.DefaultConfig())

		fmt.Printf("\n========================================================================================\n")
		fmt.Printf("🎯 STRATEGY SELECTED: %s (ID: %s)\n", strat.Name(), strat.ID())
		fmt.Printf("   Description:  %s\n", strat.Description())
		tpPct := cfg.TakeProfitPct * 100
		if tpPct == 0 && cfg.TargetPct > 1.0 {
			tpPct = (cfg.TargetPct - 1.0) * 100
		}
		slPct := cfg.StopLossPct * 100
		if slPct > 50 {
			slPct = (1.0 - cfg.StopLossPct) * 100
		}
		fmt.Printf("   Target:       +%.1f%% | Stop-Loss: -%.1f%% | Max Hold: %d days | Max Positions: %d | Allocation: %.0f%%\n",
			tpPct, slPct, cfg.HoldingWindow, cfg.PositionCap, cfg.AllocationPct*100)
		fmt.Printf("========================================================================================\n")

		res := runner.ExecuteStrategyWithDividends(strat, cfg, barsBySymbol, sortedDates, conf.Capital, conf.Symbol, conf.OutDir, conf.Db, reinvestDividends)
		if res.Err != nil {
			return fmt.Errorf("Backtest failed: %v", res.Err)
		}
		results = []runner.RunResult{res}

		findings := reliability.Assess(reliability.Input{
			MarketDB: conf.Db, Table: conf.Table, Strategy: strat, Config: cfg, Trades: res.Trades, Report: res.Report,
			HoldoutMonths: conf.HoldoutMonths, Phase: conf.phase, Prior: conf.prior,
		})
		reliability.Print(os.Stdout, reliabilityTitle(conf, strat), findings)
		reliabilityGroups = reliability.Groups(findings)
		if conf.outcome != nil {
			conf.outcome.Report = &res.Report
		}

		fmt.Printf("Generated %d entry signals.\n", res.SignalCount)
		fmt.Printf("💾 Persisted %d generated entry signals to SQLite 'signals' table.\n", res.SignalCount)
		fmt.Printf("💾 Persisted %d completed trades to SQLite 'trades' table.\n", len(res.Trades))
		fmt.Printf("💾 Persisted %d daily equity points to SQLite 'equity_curve' table.\n", len(res.EquityCurve))
		fmt.Printf("💾 Persisted quantitative performance summary to SQLite 'performance_summary' table.\n")

		runner.PrintPerformanceTearSheet(strat.Name(), res.Report)
		runner.PrintReturnBreakdown(res)
		runner.PrintNotes(res)
		runner.PrintTradesTable(res.Trades, conf.Symbol)

		fmt.Printf("\n💾 Results saved to %s (run %d)\n", res.DbPath, res.RunID)
	} else {
		fmt.Printf("\n========================================================================================\n")
		fmt.Printf("🚀 CONCURRENT STRATEGY BACKTESTING (%d STRATEGIES TO RUN, %d TOTAL SELECTED)\n", len(toRun), len(selectedStrategies))
		fmt.Printf("   Market Data:  %s, loaded per batch of up to %d strategies (only the symbols each batch needs)\n", conf.Db, runner.DefaultBatchSize)
		fmt.Printf("   Output Dir:   %s/ (each strategy writes to an isolated, uniquely suffixed SQLite DB)\n", conf.OutDir)
		if conf.Alloc > 0 {
			fmt.Printf("   Allocation:   %.0f%% of equity per position\n", conf.Alloc*100)
		}
		fmt.Printf("========================================================================================\n\n")

		// Bounded worker pool, one bar load per batch (see runner.RunBatched):
		// each worker holds the batch's working set in flight (simulator,
		// signals, equity curve), and some strategies shell out to Python, so
		// neither goroutines nor loaded symbols are unbounded.
		workers := conf.Concurrency
		if workers < 1 {
			workers = 1
		}
		fmt.Printf("   Concurrency:  %d workers\n\n", workers)

		detail := runner.NewTopDetail(runner.DetailKept)
		freshResults, err := runner.RunBatched(runner.BatchOptions{
			DB: db, Table: conf.Table, Start: backtestStart, End: backtestEnd,
			SymbolFilter: conf.Symbol, Workers: workers,
		}, toRun, func(s strategy.Strategy, bars map[string][]models.Bar, dates []string) runner.RunResult {
			cfg := runOverride(conf).Apply(s.DefaultConfig())
			res := runner.ExecuteStrategyWithDividends(s, cfg, bars, dates, conf.Capital, conf.Symbol, conf.OutDir, conf.Db, reinvestDividends)
			if res.Err != nil {
				log.Printf("❌ [%s] Error: %v\n", s.ID(), res.Err)
			} else {
				fmt.Printf("✅ [%s] Completed: %d signals, %d trades, Return: %+.2f%%, Sharpe: %.2f ➔ %s\n",
					s.ID(), res.SignalCount, len(res.Trades), res.Report.TotalReturnPct*100, res.Report.SharpeRatio, res.DbPath)
			}
			return detail.Offer(res)
		})
		if err != nil {
			return fmt.Errorf("Error loading historical bars for simulation: %v", err)
		}

		// Merge freshly-run results with whatever already had a usable result so
		// the printed table/HTML export covers every selected strategy, not just
		// the ones we actually had to run.
		freshByID := make(map[string]runner.RunResult, len(freshResults))
		for _, r := range freshResults {
			freshByID[r.Strat.ID()] = r
		}
		for _, s := range selectedStrategies {
			if res, ok := freshByID[s.ID()]; ok {
				if full, kept := detail.Detail(s.ID()); kept {
					res = full // the best few keep their trades and curve for the HTML report
				}
				results = append(results, res)
				continue
			}
			if c, ok := existing[s.ID()]; ok {
				results = append(results, runner.RunResult{Strat: s, Report: c.Report, DbPath: c.DbPath, Engine: c.Engine, Suspect: c.Suspect})
			}
		}

		var kept []runner.RunResult
		for _, r := range results {
			if r.Err == nil && len(r.Trades) > 0 {
				kept = append(kept, r)
			}
		}
		if len(kept) > 0 {
			fmt.Printf("\n🔎 BACKTEST RELIABILITY (strategies whose trades were kept; run one with -strategy <id> for the full report)\n")
			// Each assessment loads its own bars, so they run side by side; the lines print in order.
			findings := make([][]reliability.Finding, len(kept))
			var wg sync.WaitGroup
			slots := make(chan struct{}, max(conf.Concurrency, 1))
			for i, r := range kept {
				wg.Add(1)
				slots <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-slots }()
					cfg := runOverride(conf).Apply(r.Strat.DefaultConfig())
					findings[i] = reliability.Assess(reliability.Input{
						MarketDB: conf.Db, Table: conf.Table, Strategy: r.Strat, Config: cfg, Trades: r.Trades, Report: r.Report,
						HoldoutMonths: conf.HoldoutMonths, Phase: conf.phase, Prior: conf.prior,
					})
				}()
			}
			wg.Wait()
			for i, r := range kept {
				reliability.PrintCompact(os.Stdout, r.Strat.ID(), findings[i])
			}
			if len(kept) < len(results) {
				fmt.Printf("  %d other strategies were not assessed: their trades are not kept in a bulk run.\n", len(results)-len(kept))
			}
		}

		runner.PrintComparisonTable(results)
	}

	// Export HTML Report for standalone/concurrent mode
	if conf.Html != "" {
		symUpper := strings.ToUpper(conf.Symbol)
		reportTitle := "Multi-Strategy Quantitative Comparison Report"
		if len(selectedStrategies) == 1 {
			reportTitle = fmt.Sprintf("%s Performance Report", selectedStrategies[0].Name())
			if symUpper != "" {
				reportTitle = fmt.Sprintf("%s Performance Report for %s", selectedStrategies[0].Name(), symUpper)
			}
		}

		var stratReports []analytics.StrategyReportData
		eqCurves := make(map[string][]float64)
		ddCurves := make(map[string][]float64)
		cashFlows := make(map[string][]analytics.CashFlowPointEntry)

		var startDate, endDate string
		var totalDays int
		var totalYears float64

		for _, r := range results {
			if r.Err != nil {
				continue
			}
			if len(results) > runner.DetailKept && r.EquityCurve == nil {
				continue // only the best strategies of a bulk run are charted
			}
			sType := "Go"
			if strings.HasSuffix(r.Strat.ID(), "-sql") {
				sType = "SQL"
			}
			stratReports = append(stratReports, analytics.StrategyReportData{
				ID:     r.Strat.ID(),
				Name:   r.Strat.Name(),
				Type:   sType,
				Report: r.Report,
				Trades: r.Trades,
			})
			var eqSeries, ddSeries []float64
			var cfSeries []analytics.CashFlowPointEntry
			for _, pt := range r.EquityCurve {
				eqSeries = append(eqSeries, pt.TotalEquity)
				ddSeries = append(ddSeries, pt.DrawdownPct)
				if pt.DividendIncome > 0 || pt.MarginInterest > 0 || pt.MarginDebt > 0 {
					cfSeries = append(cfSeries, analytics.CashFlowPointEntry{
						Date:           pt.Date,
						BuyingPower:    pt.BuyingPower,
						MarginDebt:     pt.MarginDebt,
						MarginInterest: pt.MarginInterest,
						DividendIncome: pt.DividendIncome,
					})
				}
			}
			eqCurves[r.Strat.ID()] = eqSeries
			ddCurves[r.Strat.ID()] = ddSeries
			if len(cfSeries) > 0 {
				cashFlows[r.Strat.ID()] = cfSeries
			}

			if startDate == "" {
				startDate = r.Report.StartDate
				endDate = r.Report.EndDate
				totalDays = r.Report.TotalTradingDays
				totalYears = r.Report.TotalCalendarYears
			}
		}

		htmlData := analytics.MultiStrategyHTMLData{
			Title:          reportTitle,
			GeneratedAt:    time.Now().Format("2006-01-02 15:04:05 MST"),
			Symbol:         symUpper,
			StartDate:      startDate,
			EndDate:        endDate,
			TotalDays:      totalDays,
			TotalYears:     totalYears,
			InitialCap:     conf.Capital,
			Strategies:     stratReports,
			AllDates:       sortedDates,
			EquityCurves:   eqCurves,
			DrawdownCurves: ddCurves,
			CashFlows:      cashFlows,
			Params:         append(reliabilityGroups, runner.DescribeRun(runSettings(conf, "backtest", sortedDates), selectedStrategies)...),
		}

		err := analytics.GenerateComparisonHTML(conf.Html, htmlData)
		if err != nil {
			log.Printf("Warning: Failed to generate HTML report %s: %v", conf.Html, err)
		} else {
			fmt.Printf("\n✨ Interactive HTML Report generated: %s\n\n", conf.Html)
		}
	}
	return nil
}

// reliabilityTitle names the pass a reliability block belongs to.
func reliabilityTitle(conf Config, strat strategy.Strategy) string {
	title := "(" + strat.ID()
	if conf.phase != "" {
		title += ", " + conf.phase
	}
	return title + ")"
}
