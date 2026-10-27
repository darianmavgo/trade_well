// Package check verifies a finished backtest after the fact, from the tables it kept, and the
// Markov models it read. Each checkpoint asks one question in SQL (sql/stages/check/*.sql) and
// answers with a count of offenders and the first few. The engine's own rules (no exit in the entry
// hour, one entry per signal) should make most of these impossible; the checks prove it on the run.
package check

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// Result is the answer to one checkpoint.
type Result struct {
	Name     string
	Question string
	Bad      int
	Detail   string
}

// OK says the checkpoint found nothing wrong.
func (r Result) OK() bool { return r.Bad == 0 }

// runChecks are the questions asked of a finished run's results database.
var runChecks = []struct{ file, name, question string }{
	{"trades_have_signal", "trade entries", "Did every trade start from a signal?"},
	{"one_entry_per_signal", "one entry per signal", "Did each signal buy at most once, even after its position exited?"},
	{"trades_are_sane", "trades", "Does every trade exit on or after its entry, with a positive size and prices?"},
	{"positions_do_not_overlap", "no overlap", "Does a strategy ever hold two positions in one symbol at once?"},
}

// modelChecks are the questions asked of a Markov model database.
var modelChecks = []struct{ file, name, question string }{
	{"model_cutoff", "model cutoff", "Did every model learn only from the in-sample period?"},
	{"model_signal_arithmetic", "signal arithmetic", "Is the signal always prob_bull minus prob_bear, and every probability a probability?"},
	{"model_rows", "model rows", "Does every model have predictions, none past its last date?"},
}

// Options says what to check. Empty ResultsDB skips the run checks, empty MarkovDB the model checks.
type Options struct {
	ResultsDB string // a family results database (data/reports/<run>/streak.db ...)
	RunID     int64  // the run in it; 0 = the newest
	MarkovDB  string
	// Through is the last in-sample session; no model may have learned past it. Blank checks only
	// that each model records its cutoff.
	Through string
}

// Run asks every checkpoint it has data for.
func Run(opt Options) ([]Result, error) {
	var out []Result
	if opt.ResultsDB != "" {
		db, err := storage.OpenSQLiteReadOnly(opt.ResultsDB)
		if err != nil {
			return nil, fmt.Errorf("results database: %w", err)
		}
		defer db.Close()
		run := opt.RunID
		if run == 0 {
			if err := db.Get(&run, `SELECT COALESCE(MAX(run_id), 0) FROM runs`); err != nil {
				return nil, fmt.Errorf("newest run: %w", err)
			}
		}
		for _, c := range runChecks {
			r, err := ask(db, c.file, c.name, c.question, map[string]any{"run": run})
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
	}
	if opt.MarkovDB != "" {
		db, err := storage.OpenSQLiteReadOnly(opt.MarkovDB)
		if err != nil {
			return nil, fmt.Errorf("markov database: %w", err)
		}
		defer db.Close()
		for _, c := range modelChecks {
			r, err := ask(db, c.file, c.name, c.question, map[string]any{"through": opt.Through})
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func ask(db *sqlx.DB, file, name, question string, binds map[string]any) (Result, error) {
	text, err := fs.ReadFile(sqlfiles.Stages, "stages/check/"+file+".sql")
	if err != nil {
		return Result{}, err
	}
	args := make([]any, 0, len(binds))
	for k, v := range binds {
		args = append(args, sql.Named(k, v))
	}
	var row struct {
		Bad    int    `db:"bad"`
		Detail string `db:"detail"`
	}
	if err := db.Get(&row, string(text), args...); err != nil {
		return Result{}, fmt.Errorf("%s: %w", name, err)
	}
	return Result{Name: name, Question: question, Bad: row.Bad, Detail: row.Detail}, nil
}

// Print writes one line per checkpoint and returns the number that failed.
func Print(w io.Writer, results []Result) int {
	failed := 0
	for _, r := range results {
		if r.OK() {
			fmt.Fprintf(w, "ok    %-20s %s\n", r.Name, r.Question)
			continue
		}
		failed++
		fmt.Fprintf(w, "FAIL  %-20s %s\n      %d found: %s\n", r.Name, r.Question, r.Bad, r.Detail)
	}
	return failed
}

// Main is the CLI entry point: check -results <db> [-run-id N] [-markov-db <db>] [-through YYYY-MM-DD].
func Main() {
	opt := Options{MarkovDB: ""}
	flag.StringVar(&opt.ResultsDB, "results", "", "a family results database to check (data/reports/<run>/<family>.db)")
	flag.Int64Var(&opt.RunID, "run-id", 0, "the run inside it (0 = the newest)")
	markov := flag.Bool("markov", false, "also check the Markov model database ("+appenv.MarkovDB()+")")
	flag.StringVar(&opt.Through, "through", "", "last in-sample session no model may have learned past")
	flag.Parse()
	if *markov {
		opt.MarkovDB = appenv.MarkovDB()
	}
	if opt.ResultsDB == "" && opt.MarkovDB == "" {
		fmt.Fprintln(os.Stderr, "check: give -results <db> and/or -markov")
		os.Exit(2)
	}
	results, err := Run(opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "check:", err)
		os.Exit(1)
	}
	if Print(os.Stdout, results) > 0 {
		os.Exit(1)
	}
}
