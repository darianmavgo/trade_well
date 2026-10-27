package strategy_cmd

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/rotation_strategy"
)

// rotationUniverseMain is `strategy rotation_universe`: rank the liquid universe point in time,
// store the candidate symbols as a symbol list in the reference database, and print the calendar
// year return of the equal weight top-K curves. It reads the market database and writes only the
// symbol list; -dry-run writes nothing.
func rotationUniverseMain() {
	cfg := rotation_strategy.UniverseConfig{Out: os.Stdout}
	flag.StringVar(&cfg.MarketDB, "market-db", appenv.MarketDB(), "market database whose daily bars are ranked (read only)")
	flag.StringVar(&cfg.RefDB, "ref", appenv.RefDB(), "reference database whose symbol_lists row is written")
	flag.StringVar(&cfg.ListID, "list", rotation_strategy.CandidatesListID, "symbol_lists id the candidate symbols are stored under")
	flag.BoolVar(&cfg.KeepCalc, "keep-calc", false, "keep the scratch database (reports/calc/rotation_universe.db) for inspection")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "compute and print, write no symbol list")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := rotation_strategy.Universe(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%d candidate symbols (%s). Calendar year return in percent of each equal weight top-K curve, no costs:\n", res.Candidates, res.ListID)
	fmt.Printf("  %-12s %-6s %8s %10s\n", "curve", "year", "sessions", "return %")
	for _, y := range res.Yearly {
		fmt.Printf("  %-12s %-6s %8d %10.1f\n", y.Curve, y.Year, y.Sessions, y.Percent)
	}
}
