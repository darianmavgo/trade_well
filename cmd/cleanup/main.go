// cleanup tidies data/reports in one place: report, runs, all.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cleanup"
)

const usage = `usage: cleanup report|runs|sql|all [flags]
  report  one line per run folder: size, lock, step statuses (changes nothing)
  runs    remove dead locks, mark aborted steps failed, delete empty runs (-failed: runs where no step finished)
  sql     list sql/ files nothing refers to (-remove-sql deletes them)
  all     runs, then losing strategies (needs -keep), orphan rows, old versions, then refresh strategy_last_backtest
Every mode takes -dry-run. Workers are kept between 10 and 32.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cfg := cleanup.Config{Out: os.Stdout}
	fs := flag.NewFlagSet("cleanup "+os.Args[1], flag.ExitOnError)
	fs.StringVar(&cfg.Reports, "reports", appenv.Reports(), "reports folder")
	fs.StringVar(&cfg.StrategiesDB, "db", appenv.RefDB(), "strategies database")
	fs.StringVar(&cfg.Root, "root", appenv.Folder(), "app folder")
	fs.StringVar(&cfg.Keep, "keep", "", "greenlit strategy ids never pruned (comma or + separated)")
	fs.IntVar(&cfg.Workers, "workers", cleanup.MaxWorkers, "concurrent workers (10 to 32)")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "report what would be done without doing it")
	fs.BoolVar(&cfg.Failed, "failed", false, "runs: also delete runs where no step finished")
	fs.BoolVar(&cfg.Vacuum, "vacuum", false, "all: rewrite result DBs that are mostly free pages")
	fs.BoolVar(&cfg.RemoveSQL, "remove-sql", false, "sql: delete the unreferenced files")
	yes := fs.Bool("yes", false, "all: confirm that it deletes files")
	fs.Parse(os.Args[2:])

	var err error
	switch os.Args[1] {
	case "report":
		err = cleanup.Report(cfg)
	case "runs":
		_, err = cleanup.Runs(cfg)
	case "sql":
		_, err = cleanup.SQL(cfg)
	case "all":
		if !cfg.DryRun && !*yes {
			fmt.Fprintln(os.Stderr, "cleanup all deletes files: run it with -dry-run first, then -yes")
			os.Exit(2)
		}
		err = cleanup.All(cfg)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cleanup:", err)
		os.Exit(1)
	}
}
