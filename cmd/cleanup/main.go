// cleanup tidies the tree and its data in one place: report, runs, sql, tables, jobs, bins, refs, data, dead, all.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cleanup"
	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

const usage = `usage: cleanup report|runs|sql|tables|jobs|bins|refs|data|dead|all [flags]
  report  one line per run folder: size, lock, step statuses (changes nothing)
  runs    remove dead locks, mark aborted steps failed, delete empty runs (-failed: runs where no step finished)
  sql     list sql/ files nothing refers to (-remove deletes them)
  tables  list tables and views in the operational database that no schema creates (-remove -yes drops them)
  jobs    list job_run rows whose job has no pkg/orchestrator package (-remove -yes deletes them)
  bins    list executables outside bin/ and bin/ files with no cmd/ behind them (-remove deletes them)
  refs    list docs, scripts and yaml that name a cmd, pkg path, /cron job or env var that is gone (-remove rewrites pkg paths that moved under pkg/backtest, pkg/orchestrator or pkg/schwaber)
  data    list data/ and refdata/ entries no Go or shell file mentions (changes nothing)
  dead    list Go packages no main package imports (changes nothing)
  all     runs, then losing strategies (needs -keep), orphan rows, old versions, report-only scans, then refresh strategy_last_backtest
Every mode takes -dry-run. tables and jobs use the Turso database (TURSO_DATABASE_URL, TURSO_AUTH_TOKEN).
Workers are kept between 10 and 32.`

var modes = map[string]bool{"report": true, "runs": true, "sql": true, "tables": true, "jobs": true,
	"bins": true, "refs": true, "data": true, "dead": true, "all": true}

// splitMode finds the mode among the arguments, so `cleanup all -dry-run`,
// `cleanup -dry-run all` and `cleanup --all --dry-run` all work. The rest are flags.
func splitMode(args []string) (mode string, flags []string) {
	for i, a := range args {
		if name := strings.TrimLeft(a, "-"); modes[name] {
			return name, append(append([]string(nil), args[:i]...), args[i+1:]...)
		}
	}
	return "", args
}

func main() {
	mode, args := splitMode(os.Args[1:])
	if mode == "" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cfg := cleanup.Config{Out: os.Stdout}
	fs := flag.NewFlagSet("cleanup "+mode, flag.ExitOnError)
	fs.StringVar(&cfg.Reports, "reports", appenv.Reports(), "reports folder")
	fs.StringVar(&cfg.StrategiesDB, "db", appenv.RefDB(), "strategies database")
	fs.StringVar(&cfg.Root, "root", appenv.Folder(), "app folder")
	fs.StringVar(&cfg.Keep, "keep", "", "greenlit strategy ids never pruned (comma or + separated)")
	fs.IntVar(&cfg.Workers, "workers", cleanup.MaxWorkers, "concurrent workers (10 to 32)")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "report what would be done without doing it")
	fs.BoolVar(&cfg.Failed, "failed", false, "runs: also delete runs where no step finished")
	fs.BoolVar(&cfg.Vacuum, "vacuum", false, "all: rewrite result DBs that are mostly free pages")
	fs.BoolVar(&cfg.RemoveSQL, "remove-sql", false, "sql: same as -remove")
	fs.BoolVar(&cfg.Remove, "remove", false, "sql, tables, jobs, bins, refs: delete or rewrite what was found")
	yes := fs.Bool("yes", false, "all, tables, jobs: confirm that it deletes data")
	fs.Parse(args)

	needYes := func(what string) {
		if !cfg.DryRun && !*yes {
			fmt.Fprintf(os.Stderr, "cleanup %s deletes %s: run it with -dry-run first, then -yes\n", mode, what)
			os.Exit(2)
		}
	}

	var err error
	switch mode {
	case "report":
		err = cleanup.Report(cfg)
	case "runs":
		_, err = cleanup.Runs(cfg)
	case "sql":
		_, err = cleanup.SQL(cfg)
	case "bins":
		_, err = cleanup.Bins(ctx, cfg)
	case "refs":
		_, err = cleanup.Refs(ctx, cfg)
	case "data":
		_, err = cleanup.Data(ctx, cfg)
	case "dead":
		_, err = cleanup.Dead(ctx, cfg)
	case "tables", "jobs":
		if cfg.Remove {
			needYes("rows or tables in the operational database")
		}
		err = onDB(ctx, cfg, mode)
	case "all":
		needYes("files")
		err = cleanup.All(ctx, cfg)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cleanup:", err)
		os.Exit(1)
	}
}

// onDB runs tables or jobs against the operational database (Turso).
func onDB(ctx context.Context, cfg cleanup.Config, mode string) error {
	db, err := dbutil.Open("")
	if err != nil {
		return err
	}
	defer db.Close()

	if mode == "jobs" {
		known, err := cleanup.JobNames(cfg.Root)
		if err != nil {
			return err
		}
		_, err = cleanup.Jobs(ctx, cfg, db, known)
		return err
	}
	known, err := cleanup.SchemaTables(cfg.Root)
	if err != nil {
		return err
	}
	_, err = cleanup.Tables(ctx, cfg, db, known)
	return err
}
