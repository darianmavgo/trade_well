// Package prune_losers deletes strategies from strategies.db whose most
// recent backtest has a win_rate below a threshold. The backtest results are
// the performance_summary tables of the result DBs in the reports folder (the
// top-level <id>.db files and the numbered pipeline run folders; oos/ is not
// read). "Most recent" is the row with the latest created_at per strategy id.
//
// Subcommand `untrainable` prunes tree strategies and models whose signal symbol
// cannot be trained on the bars before the holdout cutoff (see untrainable.go).
// Subcommand `symbols` takes the losing names out of a rotation strategy's list
// (see symbols.go).
package prune_losers

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/lastbacktest"
	_ "modernc.org/sqlite"
)

// MaxWorkers caps the number of result DBs read concurrently.
const MaxWorkers = lastbacktest.MaxWorkers

// familyTables are the row-backed strategy tables of strategies.db.
var familyTables = []string{
	"streak_strategy", "hold_strategy",
	"tree_strategy", "markov_strategy", "hmm_strategy", "rotation_strategy",
}

// Config holds the settings of a run.
type Config struct {
	StrategiesDB string  // -db
	ReportsDir   string  // -reports
	MinWinRate   float64 // -min-win-rate: strictly below this is a loser
	Workers      int     // -workers, capped at MaxWorkers
	DryRun       bool    // -dry-run
	Keep         string  // -keep: greenlit ids (comma or + separated) that are never deleted
	Out          io.Writer
}

// Main parses flags and runs.
func Main() {
	if len(os.Args) > 1 && os.Args[1] == "untrainable" {
		os.Exit(runUntrainable(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "orphans" {
		os.Exit(runOrphans(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "symbols" {
		os.Exit(runSymbols(os.Args[2:], os.Stdout, os.Stderr))
	}
	cfg := Config{Out: os.Stdout}
	flag.StringVar(&cfg.StrategiesDB, "db", appenv.RefDB(), "strategies database to prune")
	flag.StringVar(&cfg.ReportsDir, "reports", appenv.Reports(), "reports folder holding the result DBs")
	flag.Float64Var(&cfg.MinWinRate, "min-win-rate", 0.5, "delete strategies whose latest win_rate is below this")
	flag.IntVar(&cfg.Workers, "workers", MaxWorkers, "concurrent result DB readers (max 32)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "report what would be deleted without deleting")
	flag.StringVar(&cfg.Keep, "keep", "", "greenlit strategy ids (comma or + separated, e.g. the STRATEGY_ALLOWLIST) that are never deleted")
	flag.Parse()
	if flag.NArg() > 0 {
		// A stray word (a mistyped subcommand) must never fall through to a real prune.
		fmt.Fprintf(os.Stderr, "prune_losers: unexpected argument %q (subcommands: untrainable, symbols, orphans)\n", flag.Arg(0))
		os.Exit(2)
	}
	if _, err := Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "prune_losers:", err)
		os.Exit(1)
	}
}

// Losers returns the ids whose latest backtest made no trades, or made trades
// and had a win_rate below min. A strategy with no backtest never appears in
// latest, so it is never pruned.
func Losers(latest map[string]lastbacktest.Result, min float64) []string {
	var out []string
	for id, r := range latest {
		if r.Trades == 0 || r.WinRate < min {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// norm makes ids comparable the way strategy.Get does: case and -/_ ignored.
func norm(id string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(id))
}

// withoutKept splits losers into those still to delete and those named in keep.
func withoutKept(losers []string, keep string) (del, kept []string) {
	protect := map[string]bool{}
	for _, id := range strings.FieldsFunc(keep, func(r rune) bool { return r == ',' || r == '+' || r == ' ' }) {
		protect[norm(id)] = true
	}
	for _, id := range losers {
		if protect[norm(id)] {
			kept = append(kept, id)
		} else {
			del = append(del, id)
		}
	}
	return del, kept
}

func describe(r lastbacktest.Result) string {
	return fmt.Sprintf("win_rate %.2f, %d trades", r.WinRate, r.Trades)
}

// Run prunes strategies.db and returns the number of strategy rows deleted
// (or that would be, on a dry run).
func Run(cfg Config) (int, error) {
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	paths, err := lastbacktest.ResultDBs(cfg.ReportsDir)
	if err != nil {
		return 0, err
	}
	latest, err := lastbacktest.Latest(paths, cfg.Workers)
	if err != nil {
		return 0, err
	}
	losers, kept := withoutKept(Losers(latest, cfg.MinWinRate), cfg.Keep)
	for _, id := range kept {
		fmt.Fprintf(cfg.Out, "  keeping greenlit loser %s (%s)\n", id, describe(latest[id]))
	}
	fmt.Fprintf(cfg.Out, "read %d result DBs, %d strategies with a latest backtest, %d losers (no trades, or win_rate below %.2f); only those with a row in strategies.db are deleted\n",
		len(paths), len(latest), len(losers), cfg.MinWinRate)

	db, err := sql.Open("sqlite", cfg.StrategiesDB)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // the temp table lives on one connection
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TEMP TABLE loser(id TEXT PRIMARY KEY)`); err != nil {
		return 0, err
	}
	for _, id := range losers {
		if _, err := tx.Exec(`INSERT INTO loser(id) VALUES (?)`, id); err != nil {
			return 0, err
		}
	}
	total := 0
	for _, t := range familyTables {
		var exists int
		if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, t).Scan(&exists); err != nil {
			return 0, err
		}
		if exists == 0 {
			continue
		}
		var n int
		if cfg.DryRun {
			err = tx.QueryRow(`SELECT count(*) FROM ` + t + ` WHERE id IN (SELECT id FROM loser)`).Scan(&n)
		} else {
			var res sql.Result
			if res, err = tx.Exec(`DELETE FROM ` + t + ` WHERE id IN (SELECT id FROM loser)`); err == nil {
				var c int64
				c, err = res.RowsAffected()
				n = int(c)
			}
		}
		if err != nil {
			return 0, fmt.Errorf("%s: %w", t, err)
		}
		fmt.Fprintf(cfg.Out, "  %-20s %d\n", strings.TrimSuffix(t, "_strategy"), n)
		total += n
	}
	if cfg.DryRun {
		fmt.Fprintf(cfg.Out, "would delete %d of %d losers: the rest have no row in strategies.db\n", total, len(losers))
		return total, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	fmt.Fprintf(cfg.Out, "deleted %d of %d losers from %s: the rest have no row in strategies.db\n", total, len(losers), cfg.StrategiesDB)
	return total, nil
}

var _ = strconv.Itoa
