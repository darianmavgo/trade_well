package scoreboard

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

const (
	tableName     = "backtest_start"
	capital       = 100000.0
	downloadYears = 5
)

// startDate is the -start flag value (default backtest window start).
var startDate string

// includeSuspect is the -include-suspect flag value of the run in progress.
var includeSuspect bool

// targetDb and outDir resolve through APP_FOLDER and reports (.env).
var (
	targetDb = appenv.MarketDB()
	outDir   = appenv.Reports()
)

// Config holds the settings of a run.
type Config struct {
	Concurrency int    // -concurrency
	Serial      bool   // -serial
	Start       string // -start
	Force       bool   // -force
	Detail      int    // -detail: strategies that keep their equity curve (-1 = all)
	Strategy    string // -strategy: comma-separated ids, a family name, or "all" (empty = all)
	RunID       int    // -run-id
	// IncludeSuspect keeps runs that traded through an unadjusted split in the ranking.
	IncludeSuspect bool     // -include-suspect
	Mode           string   // subcommand (empty = default)
	Args           []string // positional arguments
}

// DefaultConfig returns the CLI defaults.
func DefaultConfig() Config {
	return Config{
		Concurrency: runtime.NumCPU(),
		Start:       storage.DefaultStartDate,
		Force:       false,
		Detail:      -1,
	}
}

// Main is the CLI entry point.
func Main() {
	// Subcommand dispatch:
	//   scoreboard          -> skip strategies that already have a usable result, backtest what's missing, then compile
	//   scoreboard compile  -> skip backtests entirely, compile from result DBs already in reports/
	//   scoreboard status   -> like compile, but just report whether all compute is done (no table, no save)
	conf := DefaultConfig()
	d := conf
	flag.BoolVar(&conf.Serial, "serial", d.Serial, "Run one strategy at a time instead of -concurrency parallel workers")
	flag.IntVar(&conf.Concurrency, "concurrency", d.Concurrency, "Max concurrent workers (defaults to all CPU cores; bounds memory/IO use with hundreds of strategies)")
	flag.StringVar(&conf.Start, "start", d.Start, "Earliest bar date (YYYY-MM-DD) to backtest; earlier bars only warm up SMAs. Empty = full history")
	flag.BoolVar(&conf.Force, "force", d.Force, "(default mode only) redo every strategy's backtest even if a usable result already exists")
	flag.IntVar(&conf.Detail, "detail", d.Detail, "Strategies whose daily equity curve is saved: the N best by CAGR of this run, for example 100 (the others save trades, signals and the summary only, which makes a bulk run faster). -1 saves every curve")
	flag.IntVar(&conf.RunID, "run-id", d.RunID, "Run folder under the reports root to read (compile, status) or to continue (default mode). Without it compile and status read the latest run and the default mode starts a new one")
	flag.StringVar(&conf.Strategy, "strategy", d.Strategy, "Strategies to compare: comma-separated ids, a family (streak, hold, tree, markov), or all. Empty = every registered strategy")
	flag.BoolVar(&conf.IncludeSuspect, "include-suspect", d.IncludeSuspect, "Rank strategies that traded through an unadjusted split too (they are left out by default: the split books a gain or loss nobody could have had)")
	conf.Mode = cliutils.PopSubcommand(map[string]string{"compile": "compile", "status": "status"})
	flag.Parse()
	conf.Args = flag.Args()
	defer storage.CloseSharedResults()
	if err := Run(conf); err != nil {
		log.Fatal(err)
	}
}

// Run executes the command with cfg. It returns errors instead of exiting.
func Run(conf Config) error {
	if conf.Serial {
		conf.Concurrency = 1
	}
	startDate = conf.Start
	includeSuspect = conf.IncludeSuspect

	// outDir is the reports root until here, then the folder of this run. It is
	// reset each call so a process that runs several scoreboard commands in a row
	// (the pipeline) does not treat the last run's folder as the root.
	outDir = appenv.Reports()
	root := outDir
	var id int
	var err error
	switch {
	case conf.RunID > 0:
		outDir, err = storage.RunDir(root, conf.RunID)
		id = conf.RunID
	case conf.Mode == "compile" || conf.Mode == "status":
		if id, outDir = storage.LatestRunDir(root); outDir == "" {
			err = fmt.Errorf("no runs in %s yet. Run backtests first (e.g. cmd/backtest -strategy all)", root)
		}
	default:
		id, outDir, err = storage.NewRun(root)
	}
	if err != nil {
		return err
	}
	fmt.Printf("📁 Run %d: %s\n", id, outDir)

	switch conf.Mode {
	case "compile":
		if err := runCompile(conf.Concurrency, conf.Strategy); err != nil {
			return err
		}
	case "status":
		if err := runStatus(conf.Concurrency, conf.Strategy); err != nil {
			return err
		}
	default:
		if err := runAll(conf.Concurrency, conf.Force, conf.Strategy, conf.Detail); err != nil {
			return err
		}
	}
	return nil
}

// registered returns the strategies this run covers: those named by -strategy,
// otherwise every registered strategy. Call it after the strategies are registered.
func registered(arg string) ([]strategy.Strategy, error) {
	if strings.TrimSpace(arg) == "" || strings.EqualFold(strings.TrimSpace(arg), "all") {
		return strategy.ListAll(), nil
	}
	return runner.ResolveStrategies(arg, "")
}

// only keeps the results of list when arg names a selection, and all of them otherwise.
func only(byStrategy map[string]runner.CompiledResult, list []strategy.Strategy, arg string) map[string]runner.CompiledResult {
	if strings.TrimSpace(arg) == "" || strings.EqualFold(strings.TrimSpace(arg), "all") {
		return byStrategy
	}
	out := make(map[string]runner.CompiledResult, len(list))
	for _, s := range list {
		if c, ok := byStrategy[s.ID()]; ok {
			out[s.ID()] = c
		}
	}
	return out
}

// missingAmong lists the ids of list that have no usable result.
func missingAmong(list []strategy.Strategy, byStrategy map[string]runner.CompiledResult) []string {
	var missing []string
	for _, s := range list {
		if _, ok := byStrategy[s.ID()]; !ok {
			missing = append(missing, s.ID())
		}
	}
	sort.Strings(missing)
	return missing
}

// runAll ensures every registered strategy has a usable backtest result. It does
// NOT blindly redo everything: it first checks reports/ (pkg/runner.ScanAndValidate
// — the same highest-increment-first, skip-if-compromised logic used by
// compile/status, and by cmd/backtest's multi-strategy mode) and only actually
// backtests strategies that are missing a usable result. Pass -force to ignore
// existing results and redo everything anyway. The final table/scoreboard.db
// always covers every strategy — freshly run ones plus whatever was already valid.
func runAll(concurrency int, force bool, arg string, detail int) error {
	fmt.Println("🚀 RUNNING SCOREBOARD: All Strategies (5 Years, $100k Capital)")

	strategy.AutoRegisterSQLStrategies(appenv.Folder(), targetDb) // so -sql strategies are included, matching compile/status
	stratreg.RegisterFamilies()

	allStrategies, err := registered(arg)
	if err != nil {
		return err
	}
	if len(allStrategies) == 0 {
		return fmt.Errorf("No strategies registered.")
	}

	existing := map[string]runner.CompiledResult{}
	if !force {
		fmt.Println("🔎 Checking data/reports/ for strategies that already have a usable result...")
		existing, _, _, _, _ = runner.ScanAndValidate(outDir, concurrency)
		if n := runner.DropStale(existing); n > 0 {
			fmt.Printf("   %d earlier results were made by the daily simulator and are redone on the hourly one.\n", n)
		}
	}

	var toRun []strategy.Strategy
	for _, s := range allStrategies {
		if _, ok := existing[s.ID()]; !ok {
			toRun = append(toRun, s)
		}
	}

	if force {
		fmt.Printf("   -force set: redoing all %d strategies regardless of existing results.\n\n", len(allStrategies))
	} else {
		fmt.Printf("   %d/%d strategies already have a usable result and will be skipped; %d need backtesting.\n\n",
			len(allStrategies)-len(toRun), len(allStrategies), len(toRun))
	}

	freshResults := make(map[string]runner.RunResult)
	if len(toRun) > 0 {
		// 1. Detect and Download missing data for the strategies we're actually running.
		if err := runner.DetectAndDownloadMissingData(targetDb, tableName, toRun, "", true, downloadYears); err != nil {
			return fmt.Errorf("Market data resolution error: %v", err)
		}

		// 2. Open market DB
		db, err := storage.OpenSQLite(targetDb)
		if err != nil {
			return fmt.Errorf("Failed to open market DB: %v", err)
		}
		defer db.Close()

		// One bar load per batch, only for the symbols that batch declares (see
		// runner.RunBatched). Loading every symbol once here and sharing the map
		// across all workers is what ran this out of memory with hundreds of
		// registered strategies.
		fmt.Printf("\n⚙️ Backtesting %d strategies: bars loaded per batch of up to %d, %d workers\n\n", len(toRun), runner.DefaultBatchSize, concurrency)

		var mu sync.Mutex
		_, err = runner.RunBatched(runner.BatchOptions{
			DB: db, Table: tableName, Start: startDate, Workers: concurrency,
		}, toRun, func(s strategy.Strategy, barsBySymbol map[string][]models.Bar, sortedDates []string) runner.RunResult {
			cfg := runner.BuildConfig(s, 0.0, 0.0, 0, 0)
			cfg.SkipEquityCurve = detail >= 0
			res := runner.ExecuteStrategy(s, cfg, barsBySymbol, sortedDates, capital, "", outDir, targetDb)
			if res.Err != nil {
				log.Printf("❌ [%s] Error: %v\n", s.ID(), res.Err)
			} else {
				log.Printf("✅ [%s] Completed (CAGR: %.2f%%)", s.ID(), res.Report.CAGR*100)
			}
			slim := res.Slim()
			mu.Lock()
			freshResults[s.ID()] = slim
			mu.Unlock()
			return slim
		})
		if err != nil {
			return fmt.Errorf("Error loading bars: %v", err)
		}
		if err := saveCurves(db, toRun, freshResults, detail); err != nil {
			return err
		}
	} else {
		fmt.Println("✅ Nothing to backtest — every registered strategy already has a usable result. (Use -force to redo everything.)")
	}

	// 4. Merge freshly-run results with whatever was already valid so the final
	// table/scoreboard.db covers every registered strategy, not just the ones we
	// just ran.
	results := make([]runner.RunResult, 0, len(allStrategies))
	for _, s := range allStrategies {
		if res, ok := freshResults[s.ID()]; ok {
			results = append(results, res)
			continue
		}
		if c, ok := existing[s.ID()]; ok {
			results = append(results, runner.RunResult{Strat: s, Report: c.Report, DbPath: c.DbPath, Engine: c.Engine, Suspect: c.Suspect})
		}
	}

	results = rankable(results)
	runner.PrintComparisonTable(results)
	if err := saveScoreboardToSQLite(results, filepath.Join(outDir, "scoreboard.db")); err != nil {
		return err
	}

	return nil
}

// saveCurves runs the detail best strategies of this run again with their daily
// equity curve saved. The bulk pass leaves the curves out (writing one row per
// session for tens of thousands of strategies costs more than running them), so
// the ones worth opening in a report get theirs here. The run is deterministic, so
// the saved summary equals the one already ranked. detail < 0 means every curve
// was saved in the bulk pass.
func saveCurves(db *sqlx.DB, ran []strategy.Strategy, results map[string]runner.RunResult, detail int) error {
	if detail <= 0 {
		return nil
	}
	var ok []strategy.Strategy
	for _, s := range ran {
		if r, found := results[s.ID()]; found && r.Err == nil {
			ok = append(ok, s)
		}
	}
	sort.SliceStable(ok, func(a, b int) bool {
		return results[ok[a].ID()].Report.CAGR > results[ok[b].ID()].Report.CAGR
	})
	if len(ok) > detail {
		ok = ok[:detail]
	}
	if len(ok) == 0 {
		return nil
	}
	fmt.Printf("\n📈 Saving equity curves of the %d best strategies\n", len(ok))
	_, err := runner.RunBatched(runner.BatchOptions{
		DB: db, Table: tableName, Start: startDate, Workers: runtime.NumCPU(),
	}, ok, func(s strategy.Strategy, barsBySymbol map[string][]models.Bar, sortedDates []string) runner.RunResult {
		cfg := runner.BuildConfig(s, 0.0, 0.0, 0, 0)
		return runner.ExecuteStrategy(s, cfg, barsBySymbol, sortedDates, capital, "", outDir, targetDb).Slim()
	})
	if err != nil {
		return fmt.Errorf("Error loading bars: %v", err)
	}
	return nil
}

// runCompile assumes every strategy has already been backtested (e.g. via
// `cmd/backtest -strategy all`) and just reads each per-strategy SQLite DB's
// performance_summary table already sitting in reports/, instead of re-running
// anything. Much cheaper: no bar loading, no simulation, no tree fitting.
func runCompile(concurrency int, arg string) error {
	fmt.Println("📖 COMPILING SCOREBOARD from existing per-strategy result databases (no backtests run)")

	strategy.AutoRegisterSQLStrategies(appenv.Folder(), targetDb) // so -sql strategy names/descriptions resolve too
	stratreg.RegisterFamilies()

	list, err := registered(arg)
	if err != nil {
		return err
	}
	byStrategy, _, totalGroups, usedFallback, allCompromised := runner.ScanAndValidate(outDir, concurrency)
	byStrategy = only(byStrategy, list, arg)
	if totalGroups == 0 {
		return fmt.Errorf("No result databases found in %s/*.db. Run backtests first (e.g. cmd/backtest -strategy all).", outDir)
	}

	fmt.Printf("⚡ %d strategies compiled (%d fell back to a lower run increment after finding corruption, %d had every increment compromised and were skipped).\n",
		len(byStrategy), usedFallback, allCompromised)

	if missing := missingAmong(list, byStrategy); len(missing) > 0 {
		fmt.Printf("⚠️  %d currently-registered strategies have NO usable result at all (never backtested, or every run compromised) — compute is NOT fully done:\n",
			len(missing))
		runner.PrintMissingList(missing)
	} else {
		fmt.Println("✅ Every currently-registered strategy has a usable result. Compute is fully done.")
	}

	if len(byStrategy) == 0 {
		return fmt.Errorf("No usable performance_summary rows found.")
	}

	results := make([]runner.RunResult, 0, len(byStrategy))
	for id, c := range byStrategy {
		results = append(results, runner.RunResult{
			Strat:  runner.ResolveStrategy(id),
			Report: c.Report,
			DbPath: c.DbPath,
			Engine: c.Engine, Suspect: c.Suspect,
		})
	}

	results = rankable(results)
	runner.PrintComparisonTable(results)
	if err := saveScoreboardToSQLite(results, filepath.Join(outDir, "scoreboard.db")); err != nil {
		return err
	}

	return nil
}

// runStatus answers "is all the necessary compute already done?" without
// printing the full comparison table or touching scoreboard.db — it just
// validates every existing result DB (same as compile) and reports which
// currently-registered strategies are covered vs. missing/compromised.
func runStatus(concurrency int, arg string) error {
	fmt.Println("🔎 SCOREBOARD STATUS — checking whether every registered strategy has a usable backtest result")

	strategy.AutoRegisterSQLStrategies(appenv.Folder(), targetDb)
	stratreg.RegisterFamilies()

	list, err := registered(arg)
	if err != nil {
		return err
	}
	total := len(list)
	byStrategy, _, totalGroups, usedFallback, allCompromised := runner.ScanAndValidate(outDir, concurrency)
	byStrategy = only(byStrategy, list, arg)

	fmt.Printf("\n📋 %d strategies currently registered.\n", total)
	fmt.Printf("   %d result-DB groups found in %s/, %d validated successfully (%d needed a fallback to an older run increment).\n",
		totalGroups, outDir, len(byStrategy), usedFallback)
	if allCompromised > 0 {
		fmt.Printf("   %d strategies have result files but every increment is compromised.\n", allCompromised)
	}

	missing := missingAmong(list, byStrategy)
	if len(missing) == 0 {
		fmt.Println("\n✅ All necessary compute is done — every registered strategy has a usable result. Safe to run `scoreboard compile`.")
		return nil
	}

	fmt.Printf("\n❌ Compute is NOT done — %d of %d registered strategies have no usable result:\n", len(missing), total)
	runner.PrintMissingList(missing)
	shown := missing
	if len(shown) > 3 {
		shown = shown[:3]
	}
	fmt.Printf("\nRun backtests for these (e.g. `go run cmd/backtest/main.go -strategy %s` or `-strategy all`) before compiling.\n",
		strings.Join(shown, ","))

	return nil
}

// saveScoreboardToSQLite writes a fresh scoreboard table to dbPath. If dbPath is
// locked or otherwise unwritable (e.g. the user has it open in a DB browser —
// SQLite's WAL/SHM sidecar files can be left in an inconsistent state after
// os.Remove deletes the main file out from under another process's open handle,
// surfacing as "disk I/O error: no such file or directory"), it falls back to
// writing an incremented filename (scoreboard_2.db, scoreboard_3.db, ...) via
// storage.CreateUniqueDB instead of silently dropping the write.
func saveScoreboardToSQLite(results []runner.RunResult, dbPath string) error {
	os.Remove(dbPath) // Fresh scoreboard every time
	// The WAL/SHM sidecars belong to whatever connection(s) currently have the
	// file open (e.g. an external DB browser); removing only the main file and
	// leaving these behind is exactly what produces the "disk I/O error" above.
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	if err := writeScoreboard(dbPath, results); err != nil {
		dir := filepath.Dir(dbPath)
		fallbackPath, fallbackDB, cerr := storage.CreateUniqueDB(dir, "scoreboard")
		if cerr != nil {
			log.Printf("Warning: Failed to write scoreboard to %s (%v), and failed to create a fallback: %v", dbPath, err, cerr)
			return nil
		}
		fallbackDB.Close()
		log.Printf("Warning: %s appears locked (%v) — probably open in another program (DB browser, etc). Writing to %s instead.", dbPath, err, fallbackPath)
		if err2 := writeScoreboard(fallbackPath, results); err2 != nil {
			log.Printf("Warning: Fallback write to %s also failed: %v", fallbackPath, err2)
			return nil
		}
		fmt.Printf("\n💾 Scoreboard saved to SQLite: %s (fallback — %s was locked; close it and rerun to update the original)\n", fallbackPath, dbPath)
		return nil
	}

	fmt.Printf("\n💾 Scoreboard saved to SQLite: %s\n", dbPath)

	return nil
}

// writeScoreboard creates the scoreboard schema and writes results to dbPath,
// returning any error encountered rather than logging-and-continuing, so the
// caller can decide whether to fall back to a different path.
func writeScoreboard(dbPath string, results []runner.RunResult) error {
	db, err := storage.OpenSQLite(dbPath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	schema := `
	CREATE TABLE IF NOT EXISTS scoreboard (
		rank INTEGER PRIMARY KEY AUTOINCREMENT,
		strategy_id TEXT,
		name TEXT,
		cagr REAL,
		total_return REAL,
		sharpe REAL,
		max_drawdown REAL,
		max_drawdown_days INTEGER,
		win_rate REAL,
		trades INTEGER,
		idle_days INTEGER,
		run_date TEXT
	);`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO scoreboard (strategy_id, name, cagr, total_return, sharpe, max_drawdown, max_drawdown_days, win_rate, trades, idle_days, run_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	// Make sure we iterate through results that have been sorted by PrintComparisonTable
	// PrintComparisonTable mutates the slice

	runDate := time.Now().Format(time.RFC3339)
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		var idleDays interface{}
		if r.Report.IdleKnown {
			idleDays = r.Report.IdleDays
		}
		if _, err := stmt.Exec(
			r.Strat.ID(),
			r.Strat.Name(),
			r.Report.CAGR,
			r.Report.TotalReturnPct,
			r.Report.SharpeRatio,
			r.Report.MaxDrawdownPct,
			r.Report.MaxDrawdownDuration,
			r.Report.WinRate,
			r.Report.TotalTrades,
			idleDays,
			runDate,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert row for %s: %w", r.Strat.ID(), err)
		}
	}
	return tx.Commit()
}

// rankable drops the runs flagged suspect (a traded symbol has an unadjusted split) unless
// -include-suspect was given, and says which it dropped.
func rankable(in []runner.RunResult) []runner.RunResult {
	if includeSuspect {
		return in
	}
	out := make([]runner.RunResult, 0, len(in))
	for _, r := range in {
		if r.Suspect != "" {
			fmt.Printf("   left out of the ranking: %s (%s)\n", r.Strat.ID(), r.Suspect)
			continue
		}
		out = append(out, r)
	}
	return out
}
