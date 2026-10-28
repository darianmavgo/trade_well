package train

// `train check`: for every family with a fitted model, finds the models that
// were trained on bars after the holdout cutoff. Those models have seen the
// out-of-sample months, so any held-out result built on them is not held out.
//
//	tree    a static tree fitted on bars up to tree_model_meta.last_date: a leak
//	        when that date is after the cutoff.
//	markov  needs no check: markov_cumulative's frame ends 1 PRECEDING, so the
//	        prediction on a date uses only transitions known by that date.
//
// The leaking symbols are saved to training_leak in the report file (default
// reports/training_leaks.db). With -fix each one is retrained through the cutoff.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// Leak is one model trained on bars after the cutoff.
type Leak struct {
	Family         string `db:"family"`
	Symbol         string `db:"symbol"`
	TrainedThrough string `db:"trained_through"`
	Cutoff         string `db:"cutoff"`
}

// CheckConfig holds every setting of a `train check` run.
type CheckConfig struct {
	MarketDB      string
	Table         string
	TreeDB        string
	HoldoutMonths int    // months held out; 0 = storage.DefaultHoldoutMonths
	Report        string // where training_leak is saved; empty = appenv.ReportFile("training_leaks.db")
	Fix           bool   // retrain each leaking tree through the cutoff
	Out           io.Writer
}

// CheckLeaks returns the cutoff and every model trained past it.
func CheckLeaks(cfg CheckConfig) (string, []Leak, error) {
	if cfg.MarketDB == "" {
		cfg.MarketDB = appenv.MarketDB()
	}
	if cfg.TreeDB == "" {
		cfg.TreeDB = appenv.TreeDB()
	}
	if cfg.Table == "" {
		cfg.Table = "backtest_start"
	}
	if cfg.HoldoutMonths <= 0 {
		cfg.HoldoutMonths = storage.DefaultHoldoutMonths
	}
	cutoff, ok, err := storage.InSampleEnd(cfg.MarketDB, cfg.Table, "", "", cfg.HoldoutMonths)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, fmt.Errorf("train check: not enough history to hold out %d months", cfg.HoldoutMonths)
	}
	var leaks []Leak
	db, err := storage.OpenSQLite(cfg.TreeDB)
	if err != nil {
		return "", nil, err
	}
	defer db.Close()
	err = db.Select(&leaks, `SELECT 'tree' AS family, symbol, last_date AS trained_through, ? AS cutoff
		FROM tree_model_meta WHERE last_date > ? ORDER BY symbol`, cutoff, cutoff)
	if err != nil {
		return "", nil, fmt.Errorf("train check: read %s: %w", cfg.TreeDB, err)
	}
	return cutoff, leaks, nil
}

// SaveLeaks replaces training_leak in the report file with leaks.
func SaveLeaks(path string, leaks []Leak) error {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE IF EXISTS training_leak;
		CREATE TABLE training_leak (family TEXT NOT NULL, symbol TEXT NOT NULL, trained_through TEXT NOT NULL,
			cutoff TEXT NOT NULL, checked_at TEXT NOT NULL, PRIMARY KEY (family, symbol))`); err != nil {
		return err
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	for _, l := range leaks {
		if _, err := db.Exec(`INSERT INTO training_leak VALUES (?,?,?,?,?)`, l.Family, l.Symbol, l.TrainedThrough, l.Cutoff, now); err != nil {
			return err
		}
	}
	return nil
}

var digits = regexp.MustCompile(`[0-9]+`)

func runCheck(args []string, stdout, stderr io.Writer) int {
	cfg := CheckConfig{Out: stdout}
	fs := flag.NewFlagSet("train check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.MarketDB, "db", appenv.MarketDB(), "market database")
	fs.StringVar(&cfg.TreeDB, "tree-db", appenv.TreeDB(), "persisted tree database")
	fs.IntVar(&cfg.HoldoutMonths, "holdout-months", storage.DefaultHoldoutMonths, "months held out")
	fs.StringVar(&cfg.Report, "report", appenv.ReportFile("training_leaks.db"), "where training_leak is saved")
	fs.BoolVar(&cfg.Fix, "fix", false, "retrain each leaking tree through the cutoff")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cutoff, leaks, err := CheckLeaks(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := SaveLeaks(cfg.Report, leaks); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "cutoff %s (last %d months held out)\ntree: %d models trained past the cutoff, saved to training_leak in %s\n",
		cutoff, cfg.HoldoutMonths, len(leaks), cfg.Report)
	fmt.Fprintln(stdout, "markov: walk-forward by construction, not checked.")
	if !cfg.Fix || len(leaks) == 0 {
		return 0
	}
	syms := make([]string, len(leaks))
	for i, l := range leaks {
		syms[i] = l.Symbol
	}
	tc := DefaultTreeConfig()
	tc.MarketDB, tc.ModelDB, tc.Symbols, tc.Through, tc.Out = cfg.MarketDB, cfg.TreeDB, syms, cutoff, stdout
	res, err := TrainTree(context.Background(), tc)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "retrained %d of %d through %s; %d skipped\n", len(res.Trained), len(syms), cutoff, len(res.Skipped))
	why := map[string]int{}
	for _, r := range res.Skipped {
		why[digits.ReplaceAllString(r, "N")]++
	}
	for r, n := range why {
		fmt.Fprintf(stdout, "  skipped %d: %s\n", n, r)
	}
	_, left, err := CheckLeaks(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := SaveLeaks(cfg.Report, left); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "still past the cutoff after retraining: %d\n", len(left))
	if len(left) > 0 {
		return 1
	}
	return 0
}
