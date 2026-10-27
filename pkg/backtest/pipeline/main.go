package pipeline

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
	"github.com/darianmavgo/trade_well/pkg/backtest/datasource"
)

// Main is the pipeline command.
func Main() { os.Exit(RunArgs(os.Args[1:], os.Stdout, os.Stderr)) }

// RunArgs is Main without os.Args and os.Exit. It returns the exit code: 0 when
// every step passed or was skipped, 1 when a step failed, 2 for a bad command line.
func RunArgs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pipeline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: pipeline [flags]\n\n"+
			"Runs every stage from market data to ranked strategies for a set of stocks, in one\n"+
			"numbered run folder under data/reports. The run is scoped: only the symbols and\n"+
			"strategies it was started with are touched. Resume a stopped run with -run-id.\n\n"+
			"Steps, in order: "+strings.Join(StepNames(), ", ")+"\n\n")
		fs.PrintDefaults()
	}
	var (
		cfg                              Config
		symbols, strategies, steps, skip string
		listSteps                        bool
	)
	fs.StringVar(&symbols, "symbol", "", "stocks the run may touch, comma-separated (default GOOGL on a new run, the stored ones on a resumed run)")
	fs.StringVar(&strategies, "strategy", "", "only these strategy ids (default: every strategy tied to the symbols)")
	fs.StringVar(&cfg.Family, "family", "", "scope the run to every row of a strategy family (streak) instead of -symbol; no primary, so the stack steps skip")
	fs.IntVar(&cfg.Limit, "limit", 0, "with -family: only the first N rows by id (0 = every row)")
	fs.StringVar(&cfg.Park, "park", "SGOV", "symbol idle cash parks in for the stack_eval step (downloaded only when that step runs)")
	fs.StringVar(&cfg.Primary, "primary", "", "primary strategy of the stack steps (default streak-<symbol>-down3-<symbol>)")
	fs.IntVar(&cfg.Years, "years", 6, "years of bars to download for symbols that are missing bars")
	fs.BoolVar(&cfg.Refresh, "refresh", false, "download the whole window again for the symbols the run needs, not only the missing bars")
	fs.IntVar(&cfg.RunID, "run-id", 0, "resume this run (0 starts a new run; finished steps are skipped)")
	fs.StringVar(&steps, "steps", "", "run only these steps, comma-separated")
	fs.StringVar(&skip, "skip", "", "leave out these steps, comma-separated")
	fs.BoolVar(&cfg.Redo, "redo", false, "run steps again that already finished in a resumed run")
	fs.BoolVar(&cfg.SkipNetwork, "skip-network", false, "leave out the steps that download data")
	fs.BoolVar(&listSteps, "list-steps", false, "print the steps and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if listSteps {
		fmt.Fprintln(stdout, strings.Join(StepNames(), "\n"))
		return 0
	}
	cfg.Symbols = cliutils.SplitList(symbols)
	cfg.Strategies = cliutils.SplitList(strategies)
	cfg.Steps = cliutils.SplitList(steps)
	cfg.Skip = cliutils.SplitList(skip)
	cfg.PolygonKey = datasource.ResolvePolygonAPIKey() // CLI only: Run never reads the environment
	cfg.Out = stdout

	res, err := Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return report(stdout, res)
}

// report prints the run's summary and returns 1 if any step failed.
func report(out io.Writer, res Result) int {
	fmt.Fprintf(out, "\nRun %d: %s\n", res.RunID, res.Dir)
	for _, s := range res.Steps {
		line := fmt.Sprintf("  %-24s %s", s.Name, s.Status)
		if s.Error != "" && s.Status != StatusDone {
			line += "  " + oneLine(s.Error)
		}
		fmt.Fprintln(out, line)
	}
	if failed := res.Failed(); len(failed) > 0 {
		fmt.Fprintf(out, "\n%d step(s) failed. Fix the cause and resume with: pipeline -run-id %d\n", len(failed), res.RunID)
		return 1
	}
	fmt.Fprintln(out, "\nAll steps passed.")
	return 0
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
