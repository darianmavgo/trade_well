// Package cleanup is the one place to tidy data/reports: aborted and empty
// pipeline runs, losing strategies, orphan rows in result DBs and old result DB
// versions. It calls the existing packages (prune_losers, clear_older,
// lastbacktest) in the order that frees the most: rows first, files last.
package cleanup

import (
	"fmt"
	"io"

	"github.com/darianmavgo/trade_well/pkg/backtest/clear_older"
	"github.com/darianmavgo/trade_well/pkg/backtest/lastbacktest"
	"github.com/darianmavgo/trade_well/pkg/backtest/prune_losers"
)

// Concurrency bounds for every step.
const (
	MinWorkers = 10
	MaxWorkers = 32
)

// Workers keeps n within MinWorkers..MaxWorkers.
func Workers(n int) int {
	return min(max(n, MinWorkers), MaxWorkers)
}

// Config holds the settings shared by every step.
type Config struct {
	Reports      string // reports folder
	StrategiesDB string
	Root         string // app folder: result DB paths in strategy_last_backtest are relative to it
	Keep         string // greenlit strategy ids that are never pruned
	Workers      int    // kept within MinWorkers..MaxWorkers
	DryRun       bool
	Failed       bool // runs: also delete runs where no step finished
	Vacuum       bool // orphans: rewrite files that are mostly free pages
	RemoveSQL    bool // sql: delete the unreferenced files under sql/
	Out          io.Writer
}

// All runs the steps in order: aborted runs, losing strategies (only when Keep
// names the greenlit ids), orphan rows, old versions, then refreshes
// strategy_last_backtest. It stops at the first error.
func All(cfg Config) error {
	cfg.Workers = Workers(cfg.Workers)
	say := func(f string, a ...any) { fmt.Fprintf(cfg.Out, f+"\n", a...) }

	say("== runs")
	if _, err := Runs(cfg); err != nil {
		return err
	}

	say("== strategies")
	if cfg.Keep == "" {
		say("skipped: pass -keep with the greenlit strategy ids (the STRATEGY_ALLOWLIST) to prune losing strategies")
	} else if _, err := prune_losers.Run(prune_losers.Config{StrategiesDB: cfg.StrategiesDB, ReportsDir: cfg.Reports,
		MinWinRate: 0.5, Workers: cfg.Workers, DryRun: cfg.DryRun, Keep: cfg.Keep, Out: cfg.Out}); err != nil {
		return err
	}

	say("== orphans")
	if _, err := prune_losers.PruneOrphans(prune_losers.OrphansConfig{StrategiesDB: cfg.StrategiesDB, ReportsDir: cfg.Reports,
		Root: cfg.Root, Workers: cfg.Workers, DryRun: cfg.DryRun, Vacuum: cfg.Vacuum, RemoveEmpty: true, Out: cfg.Out}); err != nil {
		return err
	}

	say("== versions")
	keep, err := clear_older.Latest(cfg.Reports)
	if err != nil {
		return err
	}
	n, err := clear_older.Run(clear_older.Config{Keep: keep, Workers: cfg.Workers, DryRun: cfg.DryRun, Dir: cfg.Reports, Out: cfg.Out})
	if err != nil {
		return err
	}
	say("%d old version files", n)

	say("== sql (report only)")
	cfg.RemoveSQL = false
	if _, err := SQL(cfg); err != nil {
		return err
	}

	if cfg.DryRun {
		return nil
	}
	say("== lastrun")
	rows, err := lastbacktest.Run(lastbacktest.Config{StrategiesDB: cfg.StrategiesDB, ReportsDir: cfg.Reports,
		BaseDir: cfg.Root, Workers: cfg.Workers, Out: cfg.Out})
	if err != nil {
		return err
	}
	say("strategy_last_backtest: %d rows", rows)
	return nil
}
