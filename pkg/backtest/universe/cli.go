package universe

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
)

// Main is the CLI entrypoint for cmd/universe.
//
//	universe           discover symbols and look up the ones not yet known
//	universe avgvol    average daily share volume of the symbols in the market database
func Main() {
	mode := cliutils.PopSubcommand(map[string]string{"avgvol": "avgvol", "reclassify": "reclassify"})
	cfg := DefaultConfig()
	flag.StringVar(&cfg.DBPath, "db", cfg.DBPath, "Universe SQLite database path")
	if mode == "avgvol" {
		mainAvgVol(&cfg.DBPath)
		return
	}
	if mode == "reclassify" {
		mainReclassify(&cfg.DBPath)
		return
	}
	flag.StringVar(&cfg.PolygonKey, "polygon-key", "", "Polygon.io API key (falls back to POLYGON_API_KEY / .env)")
	flag.IntVar(&cfg.Workers, "workers", cfg.Workers, "Number of concurrent verification workers")
	flag.IntVar(&cfg.Limit, "limit", cfg.Limit, "Page size for Polygon discovery")
	flag.IntVar(&cfg.MaxChecks, "max-checks", 0, "Limit number of candidate tickers to verify (0 = all)")
	flag.BoolVar(&cfg.ETFsOnly, "etfs-only", false, "Only process ETFs")
	flag.BoolVar(&cfg.StocksOnly, "stocks-only", false, "Only process non-ETF stocks")
	flag.BoolVar(&cfg.Refresh, "refresh", false, "Look up every symbol again, including those with a stored first trade date")
	flag.IntVar(&cfg.RetryDays, "retry-days", cfg.RetryDays, "Days before a symbol that could not be verified is tried again")
	flag.Parse()

	cfg.Out = os.Stdout

	fmt.Println("==================================================")
	fmt.Println("🌌 UNIVERSE: Stock & ETF Discovery & Classification")
	fmt.Println("==================================================")

	if _, err := Run(cfg); err != nil {
		log.Fatalf("Universe error: %v", err)
	}
}

// mainReclassify is `universe reclassify`: relabel leverage and direction of the
// stored symbols from their names with the current classifier.
func mainReclassify(dbPath *string) {
	dry := flag.Bool("dry-run", false, "list the symbols whose labels would change without saving")
	flag.Parse()
	db, err := Open(*dbPath)
	if err != nil {
		log.Fatalf("reclassify: %v", err)
	}
	defer db.Close()
	changed, err := Reclassify(db, *dry)
	if err != nil {
		log.Fatalf("reclassify: %v", err)
	}
	for _, c := range changed {
		fmt.Printf("%-8s %-3s/%-7s -> %-3s/%-7s  %s\n", c.Symbol, c.OldLeverage, c.OldDirection, c.NewLeverage, c.NewDirection, c.Name)
	}
	verb := "relabelled"
	if *dry {
		verb = "would relabel"
	}
	fmt.Printf("%s %d symbols\n", verb, len(changed))
}

func mainAvgVol(dbPath *string) {
	cfg := AvgVolConfig{Out: os.Stdout}
	flag.StringVar(&cfg.MarketDB, "market-db", appenv.MarketDB(), "Market database holding the daily bars")
	days := flag.String("days", "20", "Trailing daily bars to average, comma-separated (for example 20,60)")
	flag.Parse()
	cfg.DBPath = *dbPath
	var err error
	if cfg.Windows, err = ParseWindows(*days); err != nil {
		log.Fatal(err)
	}
	if _, err := AvgVol(cfg); err != nil {
		log.Fatalf("avgvol error: %v", err)
	}
}
