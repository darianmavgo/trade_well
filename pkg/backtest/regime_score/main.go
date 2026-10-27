package regime_score

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

// Main is the regime_score command.
func Main() { os.Exit(RunArgs(os.Args[1:], os.Stdout, os.Stderr)) }

// RunArgs is Main without os.Args and os.Exit. It returns the exit code.
func RunArgs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("regime_score", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: regime_score [flags]\n\n"+
			"Labels every trade in the existing backtest results with the regime of a reference symbol\n"+
			"(SPY) at its entry, then scores each strategy and all strategies pooled by regime. The\n"+
			"regime is the Markov state (bull, bear, sideways) of the last session before the entry date.\n"+
			"Reads the numbered run folders and the Markov model; writes only regime_score.db in -out-dir\n"+
			"(replaced every run). Re-runs nothing and touches no account.\n\n")
		fs.PrintDefaults()
	}
	var cfg Config
	fs.StringVar(&cfg.RunsDir, "runs-dir", "", "folder of numbered run folders (default: the reports folder)")
	fs.StringVar(&cfg.ModelDB, "model-db", "", "Markov model database (default: data/markov_models.db)")
	fs.StringVar(&cfg.Symbol, "symbol", "SPY", "symbol whose Markov state is the regime")
	fs.StringVar(&cfg.OutDir, "out-dir", "", "where regime_score.db is written (default: -runs-dir)")
	fs.IntVar(&cfg.MinTrades, "min-trades", 20, "a cell with fewer trades than this is marked thin")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg.Out = stdout
	if _, err := Run(context.Background(), cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
