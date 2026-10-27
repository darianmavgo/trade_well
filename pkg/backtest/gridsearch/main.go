// cmd/gridsearch — Parallel parameter sweep assessing baked-in strategy parameters.
//
// Usage:
//
//	# Assess and optimize a single strategy:
//	go run cmd/gridsearch/main.go voo-up3
//	go run cmd/gridsearch/main.go -strategy streak-voo-buy-tecl
//
//	# Assess and optimize many strategies at once (outer concurrency across
//	# strategies, persisted to reports/gridsearch.db, skips strategies already
//	# swept unless -force):
//	go run cmd/gridsearch/main.go -strategy mara_tree,pdd_tree,voo-up3
//	go run cmd/gridsearch/main.go -strategy all
//
//	# List all optimizable strategies:
//	go run cmd/gridsearch/main.go -list
//
//	# Print the resolved parameter grid for a strategy (or 'all') without
//	# running any backtests — no DB access required:
//	go run cmd/gridsearch/main.go params voo-up3
//	go run cmd/gridsearch/main.go params all
//
//	# Assess every strategy with a completed sweep for staleness (renamed/
//	# removed strategy, newer market data, or an edited SQL pipeline since
//	# the sweep ran) — no sweeps run:
//	go run cmd/gridsearch/main.go stale
package gridsearch

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"

	"github.com/darianmavgo/trade_well/pkg/backtest/charting"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/rotation_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/sessions"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	_ "modernc.org/sqlite"
)

type gridResult struct {
	task       sweepTask // the permutation that produced this result (for re-simulation)
	Label      string
	Report     models.PerformanceReport
	Trades     []models.Trade
	Curve      []models.DailyEquityPoint
	IsBaseline bool

	// Raw params behind Label, persisted as real columns (not just a
	// formatted string) by recordRun, so `backtest optimized` and
	// `gridsearch promote` have something structured to read back.
	Symbol       string
	SignalSymbol string
	SignalDays   int
	HoldDays     int
	TakeProfit   float64 // fractional offset, e.g. 0.05 for +5% (0 = tree_bounce/no-TP)
	StopLoss     float64 // fractional offset, e.g. 0.05 for -5% (0 = no-SL)
	Regime       string
	Allocation   float64
	Period       string  // calendar period of a period sweep ("1d".."1y"), else ""
	EntryLimit   float64 // buy limit as a fraction of the previous close in a period sweep, 0 = none
}

// sweepOutcome is everything produced by running a full parameter sweep for one
// strategy — shared between single-strategy CLI output and batch/persisted mode.
type sweepOutcome struct {
	Strat         strategy.Strategy
	ParamSpace    strategy.ParameterSpace
	SignalBars    []models.Bar
	TotalPerms    int
	Results       []gridResult
	BaselineRes   *gridResult
	TopCalmar     []gridResult
	TopResilience []gridResult
	TopProfit     []gridResult
	Elapsed       time.Duration
}

// resilienceScore rewards high CAGR while penalizing both the depth (MaxDrawdownPct)
// and duration (MaxDrawdownDuration, in trading days) of the worst drawdown. A strategy
// with the same CAGR but a shallower or briefer drawdown scores higher.
func resilienceScore(r models.PerformanceReport) float64 {
	durationYears := float64(r.MaxDrawdownDuration) / 365.0
	return r.CAGR / ((1.0 + r.MaxDrawdownPct) * (1.0 + durationYears))
}

func formatPercents(vals []float64) []string {
	res := make([]string, len(vals))
	for i, v := range vals {
		if v == 0 {
			res[i] = "None (0%)"
		} else {
			res[i] = fmt.Sprintf("%.0f%%", v*100)
		}
	}
	return res
}

// Config holds the settings of a run.
type Config struct {
	Start         string          // -start
	End           string          // -end
	HoldoutMonths int             // -holdout-months
	Db            string          // -db
	Strategy      string          // -strategy
	Strat         string          // -strat
	Mode          string          // -mode
	List          bool            // -list
	Signal        string          // -signal
	Symbol        string          // -symbol
	SymbolsFrom   string          // -symbols-from
	TopCagr       int             // -top-cagr
	Capital       float64         // -capital
	Alloc         float64         // -alloc
	Yield         float64         // -yield
	MinTrades     int             // -min-trades
	Top           int             // -top
	Html          string          // -html
	NoHtml        bool            // -no-html
	Concurrency   int             // -concurrency
	Force         bool            // -force
	IncludeStreak bool            // -include-streak
	MinWinRate    float64         // -min-win-rate (promote)
	GridsearchDb  string          // -gridsearch-db (empty = gridsearch.db in the run folder)
	RunID         int             // -run-id
	MaxPerms      int             // -max-perms
	Period        string          // -period: comma-separated calendar periods (1d,1w,1m,1q,1y) to sweep
	MinSessions   int             // -min-sessions (persymbol)
	AllSymbols    bool            // -all-symbols (persymbol): not only ETFs
	WriteRows     bool            // -write-rows (persymbol)
	Subcommand    string          // "params", "stale", "promote", or empty
	Passed        map[string]bool // flags set explicitly on the command line (nil = none)
	Args          []string        // positional arguments
}

// DefaultConfig returns the CLI defaults.
func DefaultConfig() Config {
	return Config{
		Start:         storage.DefaultStartDate,
		HoldoutMonths: storage.DefaultHoldoutMonths,
		Db:            appenv.MarketDB(),
		Strategy:      "",
		List:          false,
		Signal:        "",
		Symbol:        "",
		SymbolsFrom:   "",
		TopCagr:       10,
		Capital:       100000.0,
		Alloc:         0.65,
		Yield:         0.045,
		MinTrades:     5,
		Top:           10,
		Html:          "",
		NoHtml:        false,
		Concurrency:   runtime.NumCPU(),
		Force:         false,
		IncludeStreak: false,
		MinWinRate:    0.6,
		GridsearchDb:  "",
		MaxPerms:      20000,
		MinSessions:   1000,
	}
}

// Main is the CLI entry point.
func Main() {
	// Subcommand dispatch:
	//   gridsearch <strategy>        -> run the sweep (default)
	//   gridsearch params <strategy> -> print the resolved parameter grid (incl.
	//                                   the consecutive decline/rally-day axis)
	//                                   for one strategy or 'all', with no DB
	//                                   access and no backtests run
	//   gridsearch stale              -> assess every strategy with a recorded
	//                                    sweep in -gridsearch-db for staleness
	//                                    (unregistered strategy, newer market
	//                                    data, or an edited SQL pipeline since
	//                                    the sweep ran) and exit — no sweeps run
	conf := DefaultConfig()
	d := conf
	flag.StringVar(&conf.End, "end", d.End, "Latest bar date (YYYY-MM-DD) to sweep. Set it to the end of the in-sample window so the held-out months do not tune the parameters. Empty = latest bar")
	flag.IntVar(&conf.HoldoutMonths, "holdout-months", d.HoldoutMonths, "Months at the end of history kept out of the sweep, the same window backtest holds out, so parameters are tuned in-sample only. 0 sweeps all history. An explicit -end wins")
	flag.StringVar(&conf.Start, "start", d.Start, "Earliest bar date (YYYY-MM-DD) to sweep; earlier bars only warm up SMAs. Empty = full history")
	flag.StringVar(&conf.Db, "db", d.Db, "Path to SQLite database")
	flag.StringVar(&conf.Strategy, "strategy", d.Strategy, "Strategy ID (comma-separated list, or 'all') to assess and optimize")
	flag.BoolVar(&conf.List, "list", d.List, "List registered strategies with baked-in parameters")
	flag.StringVar(&conf.Signal, "signal", d.Signal, "Signal generation symbol override (single-strategy mode only)")
	flag.StringVar(&conf.Symbol, "symbol", d.Symbol, "Trade symbol override; comma-separated list allowed (single-strategy mode only)")
	flag.StringVar(&conf.Period, "period", d.Period, "Calendar periods to sweep for a period strategy (rotation period rows), comma separated from 1d,1w,1m,1q,1y. Default: the strategy_family_param axis")
	flag.StringVar(&conf.SymbolsFrom, "symbols-from", d.SymbolsFrom, "Study report DB (e.g. data/reports/voo_up3_etf.db) whose etf_compare view supplies the trade symbols, ranked by rank_cagr (use with -top-cagr)")
	flag.IntVar(&conf.TopCagr, "top-cagr", d.TopCagr, "With -symbols-from: how many of the best rank_cagr symbols to sweep")
	flag.Float64Var(&conf.Capital, "capital", d.Capital, "Starting cash ($)")
	flag.Float64Var(&conf.Alloc, "alloc", d.Alloc, "Allocation percentage override (single-strategy mode only)")
	flag.Float64Var(&conf.Yield, "yield", d.Yield, "Cash yield on idle reserves (4.5% = 0.045)")
	flag.IntVar(&conf.MinTrades, "min-trades", d.MinTrades, "Minimum trade count filter")
	flag.IntVar(&conf.Top, "top", d.Top, "Top N results to display per strategy")
	flag.StringVar(&conf.Html, "html", d.Html, "Path to export HTML comparison report (single-strategy mode; defaults to data/reports/<strategy>_gridsearch.html)")
	flag.BoolVar(&conf.NoHtml, "no-html", d.NoHtml, "Skip per-strategy HTML export (batch mode; speeds up large sweeps)")
	flag.IntVar(&conf.Concurrency, "concurrency", d.Concurrency, "Worker goroutines. Single-strategy mode: workers within that one sweep. Multi-strategy mode: total workers shared across every strategy's tasks combined (not per-strategy — a few expensive strategies get proportionally more of the pool once cheap ones finish). Defaults to all CPU cores.")
	flag.BoolVar(&conf.Force, "force", d.Force, "Redo strategies that already have a completed sweep in data/reports/gridsearch.db")
	flag.BoolVar(&conf.IncludeStreak, "include-streak", d.IncludeStreak, "Include streak-* strategies (rows of refdata streak_strategy) in -strategy all. They are already a promoted config, so excluded by default")
	flag.Float64Var(&conf.MinWinRate, "min-win-rate", d.MinWinRate, "gridsearch promote: minimum win rate (0-1)")
	flag.StringVar(&conf.GridsearchDb, "gridsearch-db", d.GridsearchDb, "SQLite DB for the pipeline controller (gridsearch_runs) and results (gridsearch_results) tables (default: gridsearch.db in the run folder)")
	flag.IntVar(&conf.RunID, "run-id", d.RunID, "Run folder under data/reports for gridsearch.db and the HTML. A sweep without it starts a new run. stale and promote without it use the latest run")
	flag.IntVar(&conf.MaxPerms, "max-perms", d.MaxPerms, "Multi-strategy mode: skip a strategy whose generic parameter grid exceeds this many permutations (e.g. genetic-momentum's 50-symbol RequiredSymbols list balloons its generic grid to 210,000+ combos, none of which even exercise its real Python-driven signal logic). 0 disables the cap. Single-strategy mode ignores this.")
	flag.IntVar(&conf.MinSessions, "min-sessions", d.MinSessions, "persymbol: a symbol needs at least this many derived sessions to be tuned")
	flag.BoolVar(&conf.AllSymbols, "all-symbols", d.AllSymbols, "persymbol: tune every symbol with enough history, not only ETFs")
	flag.BoolVar(&conf.WriteRows, "write-rows", d.WriteRows, "persymbol: store each passing symbol's parameters as a rotation_strategy row (persym-<symbol>) in the reference database")
	conf.Subcommand = cliutils.PopSubcommand(map[string]string{"params": "params", "stale": "stale", "promote": "promote", "apply": "apply", "persymbol": "persymbol"})
	flag.Parse()
	conf.Passed = map[string]bool{}
	flag.Visit(func(f *flag.Flag) { conf.Passed[f.Name] = true })
	conf.Args = flag.Args()
	if err := appenv.RequireHourly(conf.Db); err != nil {
		log.Fatal(err)
	}
	if err := Run(conf); err != nil {
		log.Fatal(err)
	}
}

// Run executes the command with cfg. It returns errors instead of exiting.
func Run(conf Config) error {
	ensureSessions(conf.Db)
	strategy.AutoRegisterSQLStrategies(appenv.Folder(), conf.Db)
	stratreg.RegisterFamilies()

	if conf.GridsearchDb == "" && conf.Subcommand != "params" && !conf.List {
		create := conf.Subcommand == "" || conf.Subcommand == "persymbol"
		path, id, err := storage.RunFile(appenv.Reports(), conf.RunID, create, "gridsearch.db")
		if err != nil {
			return fmt.Errorf("gridsearch: %w", err)
		}
		conf.GridsearchDb = path
		conf.RunID = id
		fmt.Printf("📁 Run %d: %s\n", id, filepath.Dir(path))
	}

	if conf.Subcommand == "apply" {
		return runApply(conf)
	}

	if conf.Subcommand == "stale" {
		runStaleCommand(conf.GridsearchDb, conf.Db, conf.Strategy)
		return nil
	}

	if conf.List {
		fmt.Println("\n=======================================================================================================================")
		fmt.Println("📋 REGISTERED STRATEGIES AVAILABLE FOR GRID SEARCH OPTIMIZATION:")
		fmt.Println("=======================================================================================================================")
		strategy.PrintFamilyCounts(os.Stdout)
		for _, s := range strategy.List() {
			space := strategy.AssessParameterSpace(s)
			fmt.Printf("  • %-20s %s\n", s.ID(), s.Name())
			fmt.Printf("    Baked-in: Asset=%s | Hold=%dd | TP=%.1f%% | SL=%.1f%% | Alloc=%.0f%%\n",
				space.SignalSymbol, space.Baseline.HoldDays, space.Baseline.TakeProfit*100, space.Baseline.StopLoss*100, space.Baseline.Allocation*100)
		}
		fmt.Println("=======================================================================================================================")
		fmt.Println()
		return nil
	}

	if conf.Subcommand == "persymbol" {
		return runPerSymbol(conf)
	}

	// Resolve strategy selection from flag or positional argument
	stratArg := strings.TrimSpace(conf.Strategy)
	if stratArg == "" && len(conf.Args) > 0 {
		stratArg = strings.TrimSpace(conf.Args[0])
	}
	if stratArg == "" {
		fmt.Println()
		fmt.Println("⚠️  No strategy specified! Please specify a strategy to optimize.")
		fmt.Println("Usage:   go run cmd/gridsearch/main.go <strategy_id>")
		fmt.Println("         go run cmd/gridsearch/main.go -strategy strat1,strat2")
		fmt.Println("         go run cmd/gridsearch/main.go -strategy all")
		fmt.Println()
		fmt.Println("Run with -list to view all available strategies:")
		fmt.Println("         go run cmd/gridsearch/main.go -list")
		fmt.Println()
		fmt.Println("Run with the 'params' subcommand to print the parameter grid without sweeping:")
		fmt.Println("         go run cmd/gridsearch/main.go params <strategy_id>")
		fmt.Println("         go run cmd/gridsearch/main.go params all")
		fmt.Println()
		fmt.Println("Run with the 'stale' subcommand to see which completed sweeps are stale:")
		fmt.Println("         go run cmd/gridsearch/main.go stale")
		fmt.Println()
		fmt.Println("Run with the 'promote' subcommand to copy winning rows into streak_strategy:")
		fmt.Println("         go run cmd/gridsearch/main.go promote -strategy voo-up3 -min-win-rate 0.6 -min-trades 30 -top 5")
		fmt.Println()
		return fmt.Errorf("no strategy specified")
	}

	var targets []strategy.Strategy
	if strings.EqualFold(stratArg, "all") {
		for _, s := range strategy.ListAll() {
			if !conf.IncludeStreak && strings.HasPrefix(s.ID(), "streak-") {
				continue
			}
			targets = append(targets, s)
		}
	} else {
		for _, tok := range strings.Split(stratArg, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			s, found := strategy.Get(tok)
			if !found {
				return fmt.Errorf("Unknown strategy '%s'. Run with -list to see available strategies.", tok)
			}
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("No valid strategies selected.")
	}

	if conf.Subcommand == "promote" {
		minTrades := 30
		if conf.Passed["min-trades"] {
			minTrades = conf.MinTrades
		}
		top := 5
		if conf.Passed["top"] {
			top = conf.Top
		}
		rep, err := Promote(PromoteConfig{
			Parents:    targets,
			GridDB:     conf.GridsearchDb,
			RefDB:      refdb.DefaultPath,
			MinWinRate: conf.MinWinRate,
			MinTrades:  minTrades,
			Top:        top,
		})
		if err != nil {
			return err
		}
		printPromoteReport(rep)
		return nil
	}

	if conf.Subcommand != "params" {
		runner.PrintMarketSourcesFor(os.Stdout, conf.Db, "backtest_start", true)
	}

	if conf.End == "" {
		end, ok, err := storage.InSampleEnd(conf.Db, "backtest_start", conf.Start, "", conf.HoldoutMonths)
		if err != nil {
			return fmt.Errorf("gridsearch: holdout window: %w", err)
		}
		if ok {
			conf.End = end
			fmt.Printf("Sweeping the in-sample window %s → %s (last %d months held out)\n", conf.Start, end, conf.HoldoutMonths)
		}
	}

	sweepOpts := sweepOptions{
		Capital:   conf.Capital,
		MinTrades: conf.MinTrades,
		TopN:      conf.Top,
		StartDate: conf.Start,
		EndDate:   conf.End,
		MarketDB:  conf.Db,
	}
	if conf.Passed["alloc"] {
		v := conf.Alloc
		sweepOpts.AllocOverride = &v
	}
	if conf.Passed["yield"] {
		v := conf.Yield
		sweepOpts.CashYieldOverride = &v
	} else {
		sweepOpts.CashYieldOverride = nil
	}
	if conf.Passed["symbol"] && conf.Symbol != "" {
		for _, sym := range strings.Split(conf.Symbol, ",") {
			if sym = strings.ToUpper(strings.TrimSpace(sym)); sym != "" {
				sweepOpts.SymbolOverride = append(sweepOpts.SymbolOverride, sym)
			}
		}
	}
	if conf.SymbolsFrom != "" {
		syms, err := symbolsFromReport(conf.SymbolsFrom, conf.TopCagr)
		if err != nil {
			return fmt.Errorf("-symbols-from %s: %v", conf.SymbolsFrom, err)
		}
		fmt.Printf("📄 Sweeping top %d rank_cagr symbols from %s: %v\n", len(syms), conf.SymbolsFrom, syms)
		sweepOpts.SymbolOverride = syms
	}
	if conf.Signal != "" && conf.Passed["signal"] {
		sweepOpts.SignalOverride = conf.Signal
	}
	if conf.Period != "" {
		for _, p := range strings.Split(conf.Period, ",") {
			p = strings.ToLower(strings.TrimSpace(p))
			if _, ok := rotation_strategy.Periods[p]; !ok {
				return fmt.Errorf("-period %q: not one of %v", p, rotation_strategy.PeriodIDs)
			}
			sweepOpts.PeriodOverride = append(sweepOpts.PeriodOverride, p)
		}
	}

	// `params` subcommand: just print the resolved parameter grid for each
	// target strategy and exit — no DB access, no backtests.
	if conf.Subcommand == "params" {
		printParamsCommand(targets, sweepOpts)
		return nil
	}

	db, err := storage.OpenSQLite(conf.Db)
	if err != nil {
		return fmt.Errorf("Failed to open DB: %v", err)
	}
	defer db.Close()

	gdb, err := storage.OpenSQLite(conf.GridsearchDb)
	if err != nil {
		return fmt.Errorf("Failed to open gridsearch pipeline DB %s: %v", conf.GridsearchDb, err)
	}
	defer gdb.Close()
	if err := ensureGridSearchSchema(gdb); err != nil {
		return fmt.Errorf("Failed to initialize gridsearch pipeline schema: %v", err)
	}
	// All runs and results (gridsearch_runs, gridsearch_results) land in this
	// file; print it up front and again at exit so it's easy to find.
	gridDBURL := fileURL(conf.GridsearchDb)
	fmt.Printf("🗄️  Gridsearch DB (calculations + results): %s\n", gridDBURL)
	defer fmt.Printf("\n🗄️  Gridsearch DB (calculations + results): %s\n", gridDBURL)

	// --- Single strategy: preserve the original rich, single-target CLI experience. ---
	if len(targets) == 1 {
		strat := targets[0]
		if !conf.Force && isStrategyDone(gdb, strat.ID()) {
			fmt.Printf("✅ %s already has a completed sweep in %s — skipping. Use -force to redo.\n", strat.ID(), conf.GridsearchDb)
			printCachedResults(gdb, strat)
			return nil
		}

		sweepOpts.InnerWorkers = conf.Concurrency
		printSweepHeader(strat, sweepOpts)

		outcome, err := runSweep(db, strat, sweepOpts)
		recordRun(gdb, strat, outcome, err)
		if err != nil {
			return fmt.Errorf("Grid search failed for %s: %v", strat.ID(), err)
		}
		if len(outcome.Results) == 0 {
			fmt.Println("No configurations met the minimum trade count filter.")
			return nil
		}

		printSweepReport(strat, outcome)

		if !conf.NoHtml {
			reportFile := defaultReportPath(strat, conf.Html, filepath.Dir(conf.GridsearchDb))
			if err := exportSweepHTML(strat, outcome, reportFile, conf.Capital, sweepOpts); err != nil {
				log.Printf("Warning: Failed to save HTML report: %v", err)
			} else {
				fmt.Printf("\n✨ Interactive Grid Search Chart saved to: %s\n\n", reportFile)
			}
		}
		return nil
	}

	// --- Multiple strategies: outer bounded pool across strategies, persisted. ---
	runBatchSweep(db, gdb, targets, sweepOpts, conf.Concurrency, conf.Force, conf.NoHtml, conf.GridsearchDb, conf.MaxPerms)
	return nil
}

// defaultReportPath is the HTML path of one strategy's sweep: the -html override
// under the reports root, otherwise <id>_gridsearch.html in dir (the run folder).
func defaultReportPath(strat strategy.Strategy, override, dir string) string {
	if override != "" {
		return appenv.ReportFile(override)
	}
	cleanID := strings.ReplaceAll(strat.ID(), "-", "_")
	return filepath.Join(dir, fmt.Sprintf("%s_gridsearch.html", cleanID))
}

func printSweepHeader(strat strategy.Strategy, opts sweepOptions) {
	paramSpace := strategy.AssessParameterSpace(strat)
	if opts.AllocOverride != nil {
		paramSpace.Allocations = []float64{*opts.AllocOverride}
	}
	if len(opts.PeriodOverride) > 0 && len(paramSpace.Periods) > 0 {
		paramSpace.Periods = opts.PeriodOverride
	}
	totalPerms := paramSpace.Perms()

	fmt.Printf("\n=======================================================================================================================\n")
	fmt.Printf("⚡ STRATEGY GRID SEARCH OPTIMIZER\n")
	fmt.Printf("=======================================================================================================================\n")
	fmt.Printf("Strategy:      %s (ID: %s)\n", strat.Name(), strat.ID())
	fmt.Printf("Description:   %s\n\n", strat.Description())
	fmt.Printf("📋 Baked-In Baseline Parameters:\n")
	fmt.Printf("   • Signal / Asset:     %s (Direction: %s)\n", paramSpace.SignalSymbol, paramSpace.Direction)
	fmt.Printf("   • Consecutive Days:   %d days\n", paramSpace.Baseline.SignalDays)
	fmt.Printf("   • Max Holding Window: %d days\n", paramSpace.Baseline.HoldDays)
	fmt.Printf("   • Take-Profit Target: +%.2f%%\n", paramSpace.Baseline.TakeProfit*100)
	fmt.Printf("   • Stop-Loss Limit:    %.2f%%\n", paramSpace.Baseline.StopLoss*100)
	fmt.Printf("   • Allocation / APY:   %.1f%% capital | %.1f%% idle APY\n\n", paramSpace.Allocations[0]*100, paramSpace.CashYield*100)
	fmt.Printf("🎯 Evaluated Parameter Grid (%d Permutations):\n", totalPerms)
	if len(paramSpace.Periods) > 0 {
		fmt.Printf("   • Periods:            %v\n", paramSpace.Periods)
	}
	fmt.Printf("   • Tradable Assets:    %v\n", paramSpace.Symbols)
	fmt.Printf("   • Consecutive Days:   %v\n", paramSpace.SignalDays)
	fmt.Printf("   • Holding Windows:    %v days\n", paramSpace.HoldDays)
	fmt.Printf("   • Take-Profit:        %v\n", formatPercents(paramSpace.TakeProfits))
	fmt.Printf("   • Stop-Loss:          %v\n", formatPercents(paramSpace.StopLosses))
	fmt.Printf("   • Regime Filters:     %v\n", paramSpace.Regimes)
	fmt.Printf("=======================================================================================================================\n\n")
	fmt.Printf("⚙️  Concurrency: %d workers\n", opts.InnerWorkers)
}

func printSweepReport(strat strategy.Strategy, outcome sweepOutcome) {
	fmt.Printf("⚡ Evaluated %d valid configurations in %v\n\n", len(outcome.Results), outcome.Elapsed)

	if outcome.BaselineRes != nil {
		fmt.Println("📌 BAKED-IN STRATEGY BASELINE:")
		fmt.Printf("   %-50s  Net Profit=+$%.2f  CAGR=%.2f%%  MaxDD=%.2f%%  Calmar=%.2f  WR=%.1f%%  Trades=%d  Idle=%s\n\n",
			outcome.BaselineRes.Label, outcome.BaselineRes.Report.NetProfit, outcome.BaselineRes.Report.CAGR*100, outcome.BaselineRes.Report.MaxDrawdownPct*100,
			outcome.BaselineRes.Report.CalmarRatio, outcome.BaselineRes.Report.WinRate*100, outcome.BaselineRes.Report.TotalTrades, formatIdle(outcome.BaselineRes.Report))
	}

	fmt.Printf("⭐ TOP %d BY CALMAR RATIO (Risk-Adjusted):\n", len(outcome.TopCalmar))
	for i, r := range outcome.TopCalmar {
		comp := ""
		if outcome.BaselineRes != nil && outcome.BaselineRes.Report.CalmarRatio > 0 {
			diff := (r.Report.CalmarRatio - outcome.BaselineRes.Report.CalmarRatio) / outcome.BaselineRes.Report.CalmarRatio * 100.0
			comp = fmt.Sprintf(" (%+.0f%% vs base)", diff)
		}
		fmt.Printf("  #%d  %-50s  CAGR=%.2f%%  DD=%.2f%%  Calmar=%.2f  WR=%.1f%%  Trades=%d  Idle=%s%s\n",
			i+1, r.Label, r.Report.CAGR*100, r.Report.MaxDrawdownPct*100, r.Report.CalmarRatio, r.Report.WinRate*100, r.Report.TotalTrades, formatIdle(r.Report), comp)
	}

	var baselineScore float64
	if outcome.BaselineRes != nil {
		baselineScore = resilienceScore(outcome.BaselineRes.Report)
	}
	fmt.Printf("\n🛡️  TOP %d BY RESILIENCE (High CAGR, Shallow & Brief Drawdowns):\n", len(outcome.TopResilience))
	for i, r := range outcome.TopResilience {
		comp := ""
		score := resilienceScore(r.Report)
		if outcome.BaselineRes != nil && baselineScore > 0 {
			diff := (score - baselineScore) / baselineScore * 100.0
			comp = fmt.Sprintf(" (%+.0f%% vs base)", diff)
		}
		fmt.Printf("  #%d  %-50s  CAGR=%.2f%%  DD=%.2f%%  DDdays=%d  Score=%.4f  Trades=%d  Idle=%s%s\n",
			i+1, r.Label, r.Report.CAGR*100, r.Report.MaxDrawdownPct*100, r.Report.MaxDrawdownDuration, score, r.Report.TotalTrades, formatIdle(r.Report), comp)
	}

	fmt.Printf("\n💰 TOP %d BY NET PROFIT:\n", len(outcome.TopProfit))
	for i, r := range outcome.TopProfit {
		comp := ""
		if outcome.BaselineRes != nil && outcome.BaselineRes.Report.NetProfit > 0 {
			diff := (r.Report.NetProfit - outcome.BaselineRes.Report.NetProfit) / outcome.BaselineRes.Report.NetProfit * 100.0
			comp = fmt.Sprintf(" (%+.0f%% vs base)", diff)
		}
		fmt.Printf("  #%d  %-50s  Profit=$%.2f  CAGR=%.2f%%  DD=%.2f%%  DDdays=%d  Calmar=%.2f  WR=%.1f%%  Trades=%d  Idle=%s%s\n",
			i+1, r.Label, r.Report.NetProfit, r.Report.CAGR*100, r.Report.MaxDrawdownPct*100, r.Report.MaxDrawdownDuration,
			r.Report.CalmarRatio, r.Report.WinRate*100, r.Report.TotalTrades, formatIdle(r.Report), comp)
	}
}

func exportSweepHTML(strat strategy.Strategy, outcome sweepOutcome, reportFile string, capital float64, opts sweepOptions) error {
	var multiResults []charting.MultiResult
	if outcome.BaselineRes != nil {
		multiResults = append(multiResults, charting.MultiResult{
			Label:      fmt.Sprintf("⭐ [BASELINE] %s", outcome.BaselineRes.Label),
			Report:     outcome.BaselineRes.Report,
			DailyCurve: outcome.BaselineRes.Curve,
		})
	}
	for _, r := range outcome.TopResilience {
		multiResults = append(multiResults, charting.MultiResult{
			Label:      "🛡️ " + r.Label,
			Report:     r.Report,
			DailyCurve: r.Curve,
		})
	}
	for _, r := range outcome.TopCalmar {
		multiResults = append(multiResults, charting.MultiResult{
			Label:      r.Label,
			Report:     r.Report,
			DailyCurve: r.Curve,
		})
	}

	view := charting.FromMultiReports(
		fmt.Sprintf("Grid Search Optimization: %s", strat.Name()),
		fmt.Sprintf("Parameter sweep centered on baked-in defaults — top %d by Resilience Score, top %d by Calmar Ratio", len(outcome.TopResilience), len(outcome.TopCalmar)),
		multiResults, outcome.SignalBars, capital,
	)
	view.Params = sweepParams(strat, outcome.ParamSpace, capital, opts)
	return charting.GenerateHTML(reportFile, view)
}

// sweepParams states what was searched and what was held fixed: the symbol
// list, every grid axis (hold, take-profit, stop, allocation, ...), and the
// baseline the sweep is centred on.
func sweepParams(strat strategy.Strategy, ps strategy.ParameterSpace, capital float64, opts sweepOptions) []models.ParamGroup {
	pct := func(v []float64) string {
		out := make([]string, len(v))
		for i, x := range v {
			out[i] = fmt.Sprintf("%.2f%%", x*100)
		}
		return strings.Join(out, ", ")
	}
	ints := func(v []int) string {
		out := make([]string, len(v))
		for i, x := range v {
			out[i] = fmt.Sprintf("%d", x)
		}
		return strings.Join(out, ", ")
	}
	orNone := func(v string) string {
		if v == "" {
			return "none"
		}
		return v
	}

	run := &models.ParamGroup{Title: "Run"}
	run.Add("Strategy", strat.ID())
	run.Add("Symbols (trade list)", orNone(strings.Join(ps.Symbols, ", ")))
	if ps.SignalSymbol != "" {
		run.Add("Signal symbol", ps.SignalSymbol+" ("+ps.Direction+")")
	}
	run.Add("Window", orNone(opts.StartDate)+" to "+orNone(opts.EndDate))
	if opts.MarketDB != "" {
		srcs := runner.MarketSourcesFor(opts.MarketDB, true)
		run.Add("Market DB", srcs[0].Path+" ("+srcs[0].Detail+"; "+srcs[0].Use+")")
	}
	run.Add("Starting capital", fmt.Sprintf("$%.2f", capital))
	run.Add("Minimum trades", fmt.Sprintf("%d", opts.MinTrades))
	run.Add("Grid points", fmt.Sprintf("%d", ps.Perms()))
	if ps.EntriesFixed() {
		run.Add("Entries", "fixed by the strategy's own pipeline; only exits searched")
	}

	axes := &models.ParamGroup{Title: "Grid search axes (searched)"}
	if len(ps.Periods) > 0 {
		axes.Add("Period", strings.Join(ps.Periods, ", "))
	}
	if !ps.EntriesFixed() && len(ps.SignalDays) > 0 {
		axes.Add("Signal days", ints(ps.SignalDays))
	}
	if len(ps.Regimes) > 0 {
		axes.Add("Regime", strings.Join(ps.Regimes, ", "))
	}
	if len(ps.EntryLimits) > 0 {
		axes.Add("Buy limit (of previous close)", pct(ps.EntryLimits))
	}
	axes.Add("Hold days", ints(ps.HoldDays))
	axes.Add("Profit taker", pct(ps.TakeProfits))
	axes.Add("Stop loss", pct(ps.StopLosses))
	axes.Add("Allocation", pct(ps.Allocations))

	b := ps.Baseline
	base := &models.ParamGroup{Title: "Baseline (strategy defaults)"}
	base.Add("Hold days", fmt.Sprintf("%d", b.HoldDays))
	base.Add("Profit taker", fmt.Sprintf("%.2f%%", b.TakeProfit*100))
	base.Add("Stop loss", fmt.Sprintf("%.2f%%", b.StopLoss*100))
	if !ps.EntriesFixed() {
		base.Add("Signal days", fmt.Sprintf("%d", b.SignalDays))
		base.Add("Regime", orNone(b.Regime))
	}
	if ps.CashYield > 0 {
		base.Add("Cash yield (annual)", fmt.Sprintf("%.2f%%", ps.CashYield*100))
	}
	if opts.AllocOverride != nil {
		run.Add("Allocation override", fmt.Sprintf("%.2f%%", *opts.AllocOverride*100))
	}
	if opts.CashYieldOverride != nil {
		run.Add("Cash yield override", fmt.Sprintf("%.2f%%", *opts.CashYieldOverride*100))
	}
	return []models.ParamGroup{*run, *axes, *base}
}

// fileURL returns a file:// URL for path (absolute when resolvable).
func fileURL(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return "file://" + filepath.ToSlash(path)
}

// symbolsFromReport returns the n best symbols by rank_cagr from a study
// report DB's etf_compare view.
func symbolsFromReport(path string, n int) ([]string, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var syms []string
	if err := db.Select(&syms, `SELECT symbol FROM etf_compare ORDER BY rank_cagr, symbol LIMIT ?`, n); err != nil {
		return nil, err
	}
	if len(syms) == 0 {
		return nil, fmt.Errorf("etf_compare returned no symbols")
	}
	return syms, nil
}

// ensureSessions brings the derived session rows of the hourly database up to
// date before a sweep reads them. A database that is not there is left alone.
func ensureSessions(marketDB string) {
	path := appenv.BarDB(marketDB, "1h")
	if _, err := os.Stat(path); err != nil {
		return
	}
	if _, err := sessions.Ensure(path); err != nil {
		log.Printf("Warning: deriving sessions in %s: %v", path, err)
	}
}
