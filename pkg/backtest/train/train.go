// Package train is the one training command: `train <family> [flags] [symbols]`.
// Backtests never train a model; they read what this command saved. A family
// with a fitted model trains here, on the daily bars in the market database. A
// family whose strategies are fully described by their row columns has nothing
// to fit and says so.
//
// Families today:
//
//	markov     trained: per-symbol Markov regime model -> data/markov_models.db
//	streak     nothing to train (rule parameters are row columns)
//	hold       nothing to train
//	tree       trained: per-symbol 5 bucket CloudForest tree -> data/tree_models.db
//	hmm        trained: Gaussian hidden Markov regime model (QQQ) -> data/hmm_models.db, not part of `all`
//
// markov_hmm strategies read hmm_regime.db, which `study hmm_regime` writes.
package train

import (
	"fmt"
	"io"
	"os"
	"sort"
)

// family is one strategy family's training entry.
type family struct {
	run  func(args []string, stdout, stderr io.Writer) int
	note string // shown by `train` with no arguments
}

func noModel(name, why string) family {
	return family{
		note: "nothing to train: " + why,
		run: func(args []string, stdout, stderr io.Writer) int {
			fmt.Fprintf(stdout, "%s: nothing to train. %s\n", name, why)
			return 0
		},
	}
}

var families = map[string]family{
	"markov": {run: runMarkov, note: "trains the per-symbol Markov regime model on the market database"},
	"streak": noModel("streak", "a streak strategy has no fitted model; its parameters are the columns of its streak_strategy row."),
	"hold":   noModel("hold", "a hold strategy has no fitted model; its parameters are the columns of its hold_strategy row."),
	"tree":   {run: runTree, note: "trains the per-symbol 5 bucket decision tree on the market database"},
	"hmm":    {run: runHMM, note: "fits the hidden Markov regime model of QQQ on the daily bars database (subcommands: compare, extend, report)"},
}

// Main is the CLI entry point.
func Main() { os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr)) }

// Run is Main without os.Args/os.Exit. It returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "-help" || args[0] == "--help" {
		usage(stderr)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	if args[0] == "all" {
		code := 0
		for _, name := range sortedFamilies() {
			if name == "hmm" {
				continue // reads daily_bars.db, not the market database, and replaces a saved model: train it deliberately
			}
			if c := families[name].run(nil, stdout, stderr); c != 0 {
				code = c
			}
		}
		return code
	}
	if args[0] == "check" {
		return runCheck(args[1:], stdout, stderr)
	}
	f, ok := families[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "train: unknown family %q\n", args[0])
		usage(stderr)
		return 2
	}
	return f.run(args[1:], stdout, stderr)
}

func sortedFamilies() []string {
	names := make([]string, 0, len(families))
	for n := range families {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: train <family|all> [flags] [symbols]")
	fmt.Fprintln(w, "families:")
	for _, n := range sortedFamilies() {
		fmt.Fprintf(w, "  %-10s %s\n", n, families[n].note)
	}
	fmt.Fprintf(w, "  %-10s %s\n", "check", "lists models trained on bars after the holdout cutoff; -fix retrains them")
	fmt.Fprintln(w, "markov_hmm strategies read hmm_regime.db, written by `study hmm_regime`.")
}
