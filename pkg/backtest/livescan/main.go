// cmd/livescan — thin wrapper around runner.RunSignalScan: the same
// GenerateSignals path backtest uses, with the time window fixed to the
// market DB tip (last completed session). ENTER means a buy signal as of
// that tip for the next equity session (today if after tip close, else the
// upcoming session).
//
// Flags mirror cmd/backtest where they mean the same thing; -bars is
// livescan-specific. Market history is refreshed for the live window by
// default; refresh failure aborts — never scan a stale tip.
//
// Usage:
//
//	go run ./cmd/livescan -strategy <strategy id>
//	go run ./cmd/livescan -strategy <id>,<id>
//	go run ./cmd/livescan -strategy "<stack name>"
//	go run ./cmd/livescan all
//	go run ./cmd/livescan -list
package livescan

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	"github.com/olekukonko/tablewriter"
)

// Config holds every setting of a live scan.
type Config struct {
	DB            string // market DB
	Table         string
	Strategy      string // ID, comma-separated IDs, a stack name, or "all"; required
	Symbol        string // optional comma-separated symbol filter
	OutDir        string // directory for livescan.db
	AutoDownload  bool
	DownloadYears int
	Concurrency   int
	Bars          int
	StrategyRoot  string    // repo root holding sql/strategies; empty skips SQL-pipeline registration
	Out           io.Writer // progress output; nil discards
}

// DefaultConfig returns the same defaults the livescan CLI uses.
func DefaultConfig() Config {
	return Config{
		DB:           cliutils.GetDefaultMarketDB(),
		Table:        runner.DefaultBarsTable,
		OutDir:       appenv.Reports(),
		AutoDownload: true,
		Concurrency:  runtime.NumCPU(),
		StrategyRoot: appenv.Folder(),
	}
}

// Run resolves the strategies and scans the live window. It uses only cfg (no
// environment variables or app-folder paths), returns errors rather than
// exiting, writes progress to cfg.Out (nil discards), and stops when ctx is
// cancelled.
func Run(ctx context.Context, cfg Config) (*runner.SignalScanResult, error) {
	if cfg.DB == "" {
		return nil, fmt.Errorf("livescan: DB is required")
	}
	if cfg.StrategyRoot != "" {
		strategy.AutoRegisterSQLStrategies(cfg.StrategyRoot, cfg.DB)
	}
	stratreg.RegisterFamilies()

	// "a+b+c" is backtest's shared-account stack syntax; a signal scan just
	// needs each component's signals, so treat "+" like ",".
	// A stack's friendly name (refdata stack table) stands for its members.
	spec := cfg.Strategy
	if db, err := refdb.OpenExisting(refdb.DefaultPath); err == nil && db != nil {
		if st, ok, _ := refdb.StackByName(db, strings.TrimSpace(spec)); ok {
			spec = st.ID
		}
		db.Close()
	}
	selected, err := runner.ResolveStrategies(strings.ReplaceAll(spec, "+", ","), "")
	if err != nil {
		return nil, fmt.Errorf("%w. Run with -list to view available strategies", err)
	}
	res, err := runner.RunSignalScan(runner.SignalScanOptions{
		MarketDB:      cfg.DB,
		Table:         cfg.Table,
		Strategies:    selected,
		SymbolFilter:  cfg.Symbol,
		BarsLimit:     cfg.Bars,
		AutoDownload:  cfg.AutoDownload,
		DownloadYears: cfg.DownloadYears,
		Concurrency:   cfg.Concurrency,
		OutDir:        cfg.OutDir,
		Context:       ctx,
		Out:           cfg.Out,
	})
	if err != nil {
		return nil, fmt.Errorf("livescan: %w", err)
	}
	return res, nil
}

// Main is the CLI entry point.
func Main() {
	cfg := DefaultConfig()
	d := cfg
	flag.StringVar(&cfg.DB, "db", d.DB, "Path to source SQLite DB containing historical market bars")
	flag.StringVar(&cfg.Table, "table", d.Table, "Table name containing historical bars")
	flag.StringVar(&cfg.Strategy, "strategy", "", "Strategy ID, comma-separated IDs, or \"all\"")
	flag.StringVar(&cfg.Symbol, "symbol", "", "Optional comma-separated symbol filter")
	flag.StringVar(&cfg.OutDir, "out-dir", d.OutDir, "Directory for livescan.db")
	flag.BoolVar(&cfg.AutoDownload, "auto-download", d.AutoDownload, "Refresh market history for the live window before scanning (default on; failure aborts)")
	flag.IntVar(&cfg.DownloadYears, "download-years", 0, "Years of history to download (0 = derive from -bars / strategy min)")
	flag.IntVar(&cfg.Concurrency, "concurrency", d.Concurrency, "Parallel strategy workers")
	flag.IntVar(&cfg.Bars, "bars", 0, "Recent bars per symbol; 0 = strategy MinHistoryBars")
	listStrategies := flag.Bool("list", false, "List registered strategies and exit")
	jsonOut := flag.Bool("json", false, "Emit machine-readable SignalScanResult JSON to stdout (for trade_orchestrator)")
	flag.Parse()

	if *listStrategies {
		strategy.AutoRegisterSQLStrategies(appenv.Folder(), cfg.DB)
		stratreg.RegisterFamilies()
		fmt.Println("Available Strategies:")
		for _, s := range strategy.List() {
			fmt.Printf("  %-28s %s\n", s.ID(), s.Name())
		}
		strategy.PrintFamilyCounts(os.Stdout)
		return
	}

	if cfg.Strategy == "" && flag.NArg() > 0 {
		cfg.Strategy = flag.Arg(0)
	}

	fmt.Printf("\n📡 livescan = backtest signal phase (live window)\n")
	cfg.Out = os.Stdout
	res, err := Run(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\n📅 Tip / as-of        : %s\n", res.AsOf)
	fmt.Printf("🎯 Next session (entry): %s\n", res.NextSession)
	fmt.Printf("📊 Symbols loaded     : %d\n\n", res.SymbolsLoaded)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			log.Fatalf("json encode: %v", err)
		}
		return
	}

	printScanTable(res.Rows)
	fmt.Printf("\n💾 Wrote %s\n", res.OutDBPath)
	if n := len(res.Signals); n > 0 {
		fmt.Printf("📌 %d priced ENTER signal(s) in livescan_signals\n", n)
	}
}

func printScanTable(results []runner.SignalScanRow) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Strategy", "Status", "Symbols", "Signal Date"})
	table.SetBorder(true)
	table.SetAutoWrapText(false)

	entering := 0
	for _, r := range results {
		status := "⚪ NO_SIGNAL"
		if r.Status == "ENTER" {
			status = "🟢 ENTER"
			entering++
		}
		table.Append([]string{r.StrategyID, status, truncateSymbols(r.Symbols, 8), r.SignalDate})
	}
	table.Render()
	fmt.Printf("\n%d/%d strategies have an entry signal as of the tip bar (for next session).\n", entering, len(results))
}

func truncateSymbols(symbols string, maxShown int) string {
	if symbols == "" {
		return ""
	}
	parts := strings.Split(symbols, ", ")
	if len(parts) <= maxShown {
		return symbols
	}
	return fmt.Sprintf("%s, ... (+%d more, see livescan.db)", strings.Join(parts[:maxShown], ", "), len(parts)-maxShown)
}
