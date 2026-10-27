// Package validate answers one question about a strategy: is it real or overfit?
// It walks the strategy forward through rolling train/test folds and then gives
// the HOLDS / DECAYS / CURVE_FIT / INSUFFICIENT verdict from those folds.
//
//	validate                 walk the folds, then the verdict
//	validate walk            only the folds (writes walk_forward.db)
//	validate verdict         only the verdict (reads walk_forward.db)
//
// Both halves keep their files in one run folder, data/reports/<run_id>/.
// pkg/walk_forward and pkg/check_overfit hold the logic.
package validate

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/check_overfit"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/walk_forward"
)

// Config is the CLI surface of validate.
type Config struct {
	Mode  string // "", "walk" or "verdict"
	Walk  walk_forward.Config
	Check check_overfit.Config
	// RunID is the run folder for both halves. 0 starts a new run for the folds
	// and uses the latest run for a verdict alone.
	RunID int
}

// Main is the validate command.
func Main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	var conf Config
	if len(args) > 0 {
		switch args[0] {
		case "walk", "verdict":
			conf.Mode, args = args[0], args[1:]
		}
	}
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: validate [walk|verdict] -strategy id[,id...] [flags]\n\n"+
			"Is the strategy real or overfit? Walks it through rolling train/test folds,\n"+
			"then prints HOLDS, DECAYS, CURVE_FIT or INSUFFICIENT for each strategy.\n"+
			"  validate          folds, then the verdict\n"+
			"  validate walk     only the folds\n"+
			"  validate verdict  only the verdict, from an earlier run\n\n")
		fs.PrintDefaults()
	}
	var strategies string
	fs.StringVar(&strategies, "strategy", "", "strategy id, or a comma-separated list")
	fs.IntVar(&conf.RunID, "run-id", 0, "run folder under data/reports (default: a new run for the folds, the latest for a verdict alone)")
	fs.StringVar(&conf.Walk.MarketDB, "market-db", appenv.MarketDB(), "market bars SQLite")
	fs.StringVar(&conf.Walk.Table, "table", "backtest_start", "bars table")
	fs.StringVar(&conf.Walk.DB, "db", "", "walk_forward.db (default: in the run folder)")
	fs.IntVar(&conf.Walk.TrainMonths, "train-months", 24, "rolling in-sample window, calendar months")
	fs.IntVar(&conf.Walk.TestMonths, "test-months", 6, "out-of-sample window, calendar months")
	fs.IntVar(&conf.Walk.StepMonths, "step-months", 6, "how far each fold moves forward, calendar months")
	fs.Float64Var(&conf.Walk.Capital, "capital", 100000, "starting capital for each window")
	fs.BoolVar(&conf.Walk.KeepGoing, "keep-going", false, "skip strategies that cannot be walked (short history) instead of stopping")
	fs.IntVar(&conf.Check.Gates.MinOOSTrades, "min-oos-trades", 8, "below this many out-of-sample trades the verdict is INSUFFICIENT")
	fs.IntVar(&conf.Check.Gates.TrialCutoff, "trial-cutoff", 20, "parameter combinations at or above this can be CURVE_FIT when the out-of-sample Sharpe collapses")
	fs.Float64Var(&conf.Check.Gates.Decay, "decay", 0.25, "out-of-sample Sharpe below this fraction of in-sample Sharpe is DECAYS (and CURVE_FIT when trials are high)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	conf.Walk.Strategy = strategies
	conf.Check.Strategy = strategies
	return Run(context.Background(), out, conf)
}

// Run walks the folds and/or prints the verdict, according to conf.Mode. The
// folds and the verdict read and write the same walk_forward.db.
func Run(ctx context.Context, out io.Writer, conf Config) error {
	if conf.Mode != "verdict" && strings.TrimSpace(conf.Walk.Strategy) == "" {
		return fmt.Errorf("validate: -strategy is required")
	}
	if conf.Walk.DB == "" {
		create := conf.Mode != "verdict"
		path, id, err := storage.RunFile(appenv.Reports(), conf.RunID, create, "walk_forward.db")
		if err != nil {
			return fmt.Errorf("validate: %w", err)
		}
		conf.Walk.DB = path
		fmt.Fprintf(out, "📁 Run %d: %s\n", id, filepath.Dir(path))
	}
	conf.Check.DB = conf.Walk.DB
	if conf.Mode != "verdict" {
		if err := walk_forward.Run(ctx, out, conf.Walk); err != nil {
			return err
		}
	}
	if conf.Mode == "walk" {
		return nil
	}
	return check_overfit.Report(ctx, out, conf.Check)
}
