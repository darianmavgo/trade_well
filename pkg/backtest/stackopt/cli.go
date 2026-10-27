package stackopt

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
)

// Main is the CLI entry point.
func Main() {
	cfg := Config{DB: cliutils.GetDefaultMarketDB(), OutDir: appenv.Reports(), Out: os.Stdout}
	var cands, file string
	flag.StringVar(&cfg.DB, "db", cfg.DB, "Market bars SQLite")
	flag.StringVar(&cfg.Table, "table", "", "Bars table (default backtest_start)")
	flag.StringVar(&cands, "strategy", "", "Candidate strategy ids, comma separated")
	flag.StringVar(&file, "candidates-file", "", "File with one candidate id per line (added to -strategy)")
	flag.StringVar(&cfg.Start, "start", "", "Earliest simulated date; empty = full history")
	flag.IntVar(&cfg.HoldoutMonths, "holdout-months", storage.DefaultHoldoutMonths, "Months held out of selection and run once at the end (0 = none)")
	flag.Float64Var(&cfg.Capital, "capital", 100000, "Starting capital")
	flag.Float64Var(&cfg.Alloc, "alloc", 0, "Fraction of equity per position; 0 keeps each strategy's own")
	flag.Float64Var(&cfg.MaxDD, "max-dd", 0.10, "Stack max drawdown cap as a fraction (0.10 = 10%)")
	flag.Float64Var(&cfg.MinGain, "min-gain", 0.005, "Stop when the best addition lifts stack CAGR by less than this fraction")
	flag.IntVar(&cfg.MaxSleeves, "max-sleeves", 12, "Most sleeves in the stack")
	flag.IntVar(&cfg.Concurrency, "concurrency", 8, "Parallel trials")
	flag.StringVar(&cfg.OutDir, "out-dir", cfg.OutDir, "Reports root; a new numbered run folder is made inside")
	flag.BoolVar(&cfg.AllowLeaks, "allow-leaks", false, "keep tree strategies trained past the holdout cutoff (default: drop them; run `train check` first)")
	flag.StringVar(&cfg.Name, "name", "", "Save the result in the refdata stack table under this name")
	flag.Parse()

	for _, id := range strings.Split(cands, ",") {
		if id = strings.TrimSpace(id); id != "" {
			cfg.Candidates = append(cfg.Candidates, id)
		}
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			log.Fatal(err)
		}
		for _, id := range strings.Fields(string(b)) {
			cfg.Candidates = append(cfg.Candidates, id)
		}
	}
	strategy.AutoRegisterSQLStrategies(appenv.Folder(), cfg.DB)
	stratreg.RegisterFamilies()
	res, err := Run(cfg)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(res.Summary())
}
