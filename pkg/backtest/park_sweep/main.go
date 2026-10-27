package park_sweep

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
)

// Main is the park_sweep command.
func Main() {
	dbPath := flag.String("db", "", "sweep database (default data/reports/park_googl.db)")
	market := flag.String("market-db", "", "market history database (default data/market_history.db)")
	settings := flag.String("settings-db", "", "reference database (default refdata/strategies.db)")
	concurrency := flag.Int("concurrency", runtime.NumCPU(), "worker count")
	reportDB := flag.String("report-db", "", "report snapshot database (default data/reports/park_googl_report.db)")
	htmlPath := flag.String("html", "", "report HTML (default data/reports/park_googl.html)")
	strategies := flag.String("strategy", "", "comma-separated streak_strategy or markov_strategy ids to compare (default: every row). Applies to seed, run, rank and report")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: park_sweep [flags] seed|run|rank|report\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *dbPath == "" {
		*dbPath = filepath.Join(appenv.Reports(), "park_googl.db")
	}
	if *market == "" {
		*market = appenv.MarketDB()
	}
	if *settings == "" {
		*settings = appenv.RefDB()
	}
	ids := ParseIDs(*strategies)
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	var err error
	switch flag.Arg(0) {
	case "seed":
		counts, seedErr := Seed(*dbPath, *settings, *market, ids)
		err = seedErr
		if err == nil {
			fmt.Printf("seed %s\n", *dbPath)
			for _, c := range counts {
				fmt.Printf("  %-14s %6d rows   %6d pending   %6d skipped\n", c.Kind, c.Rows, c.Pending, c.Skipped)
			}
		}
	case "run":
		err = Run(*dbPath, *market, *concurrency, ids)
	case "rank":
		err = Rank(*dbPath, ids)
	case "report":
		if *reportDB == "" {
			*reportDB = filepath.Join(appenv.Reports(), "park_googl_report.db")
		}
		if *htmlPath == "" {
			*htmlPath = filepath.Join(appenv.Reports(), "park_googl.html")
		}
		err = WriteReport(*dbPath, *reportDB, *htmlPath, ids)
		if err == nil {
			fmt.Printf("report %s\nhtml %s\n", *reportDB, *htmlPath)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "park_sweep: %v\n", err)
		os.Exit(1)
	}
}
