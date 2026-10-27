package prune_losers

// `prune_losers untrainable`: a tree can only be tested out of sample if it can
// be fitted on the bars before the holdout cutoff. A symbol with fewer than
// train.MinSamples labelled bars or fewer than train.MinTarget class 2 bars by
// the cutoff (mostly recent listings) can only have a tree fitted on the
// held-out months, so that tree and every tree_strategy row that reads it
// cannot be validated. This deletes both. The rule is train's own
// (train.TreeConfig.DryRun), so the two commands cannot disagree.

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

// UntrainableConfig holds the settings of `prune_losers untrainable`.
type UntrainableConfig struct {
	StrategiesDB  string // -db
	MarketDB      string // -market-db
	TreeDB        string // -tree-db
	Table         string
	HoldoutMonths int
	DryRun        bool
	Out           io.Writer
}

// UntrainableResult is what was (or would be) deleted.
type UntrainableResult struct {
	Cutoff     string
	Symbols    []string       // symbols whose model is deleted
	Reasons    map[string]int // skip reason (digits removed) -> symbols
	Models     int            // tree_model_meta rows deleted
	Nodes      int            // tree_node rows deleted
	Strategies int            // tree_strategy rows deleted
}

func runUntrainable(args []string, stdout, stderr io.Writer) int {
	cfg := UntrainableConfig{Out: stdout}
	fs := flag.NewFlagSet("prune_losers untrainable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.StrategiesDB, "db", appenv.RefDB(), "strategies database to prune")
	fs.StringVar(&cfg.MarketDB, "market-db", appenv.MarketDB(), "market database")
	fs.StringVar(&cfg.TreeDB, "tree-db", appenv.TreeDB(), "persisted tree database to prune")
	fs.IntVar(&cfg.HoldoutMonths, "holdout-months", storage.DefaultHoldoutMonths, "months held out")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "report what would be deleted without deleting")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := PruneUntrainable(context.Background(), cfg); err != nil {
		fmt.Fprintln(stderr, "prune_losers untrainable:", err)
		return 1
	}
	return 0
}

// PruneUntrainable finds the saved trees that cannot be fitted before the
// cutoff and deletes them and their strategy rows.
func PruneUntrainable(ctx context.Context, cfg UntrainableConfig) (UntrainableResult, error) {
	res := UntrainableResult{Reasons: map[string]int{}}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if cfg.Table == "" {
		cfg.Table = "backtest_start"
	}
	if cfg.HoldoutMonths <= 0 {
		cfg.HoldoutMonths = storage.DefaultHoldoutMonths
	}
	cutoff, ok, err := storage.InSampleEnd(cfg.MarketDB, cfg.Table, "", "", cfg.HoldoutMonths)
	if err != nil {
		return res, err
	}
	if !ok {
		return res, fmt.Errorf("not enough history to hold out %d months", cfg.HoldoutMonths)
	}
	res.Cutoff = cutoff

	tdb, err := sql.Open("sqlite", cfg.TreeDB)
	if err != nil {
		return res, err
	}
	defer tdb.Close()
	rows, err := tdb.Query(`SELECT symbol FROM tree_model_meta ORDER BY symbol`)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", cfg.TreeDB, err)
	}
	var saved []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return res, err
		}
		saved = append(saved, s)
	}
	rows.Close()

	scratch, err := os.MkdirTemp("", "untrainable-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(scratch)
	tc := train.DefaultTreeConfig()
	tc.MarketDB, tc.ModelDB, tc.Symbols, tc.Through, tc.DryRun = cfg.MarketDB, filepath.Join(scratch, "m.db"), saved, cutoff, true
	tres, err := train.TrainTree(ctx, tc)
	if err != nil {
		return res, err
	}
	for sym, why := range tres.Skipped {
		res.Symbols = append(res.Symbols, sym)
		res.Reasons[digitRun.ReplaceAllString(why, "N")]++
	}
	sort.Strings(res.Symbols)
	fmt.Fprintf(cfg.Out, "cutoff %s: %d saved trees, %d can be fitted before it, %d cannot\n", cutoff, len(saved), len(tres.Trained), len(res.Symbols))
	for why, n := range res.Reasons {
		fmt.Fprintf(cfg.Out, "  %d: %s\n", n, why)
	}
	if len(res.Symbols) == 0 {
		return res, nil
	}

	rdb, err := sql.Open("sqlite", cfg.StrategiesDB)
	if err != nil {
		return res, err
	}
	defer rdb.Close()
	rdb.SetMaxOpenConns(1)
	rtx, err := rdb.Begin()
	if err != nil {
		return res, err
	}
	defer rtx.Rollback()
	ttx, err := tdb.Begin()
	if err != nil {
		return res, err
	}
	defer ttx.Rollback()
	if _, err := rtx.Exec(`CREATE TEMP TABLE untrainable(symbol TEXT PRIMARY KEY)`); err != nil {
		return res, err
	}
	if _, err := ttx.Exec(`CREATE TEMP TABLE untrainable(symbol TEXT PRIMARY KEY)`); err != nil {
		return res, err
	}
	for _, s := range res.Symbols {
		if _, err := rtx.Exec(`INSERT INTO untrainable VALUES (?)`, s); err != nil {
			return res, err
		}
		if _, err := ttx.Exec(`INSERT INTO untrainable VALUES (?)`, s); err != nil {
			return res, err
		}
	}
	count := func(tx *sql.Tx, table, col string) (int, error) {
		q := fmt.Sprintf(`DELETE FROM %s WHERE %s IN (SELECT symbol FROM untrainable)`, table, col)
		if cfg.DryRun {
			q = fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s IN (SELECT symbol FROM untrainable)`, table, col)
			var n int
			err := tx.QueryRow(q).Scan(&n)
			return n, err
		}
		r, err := tx.Exec(q)
		if err != nil {
			return 0, err
		}
		n, err := r.RowsAffected()
		return int(n), err
	}
	if res.Strategies, err = count(rtx, "tree_strategy", "signal_symbol"); err != nil {
		return res, err
	}
	if res.Models, err = count(ttx, "tree_model_meta", "symbol"); err != nil {
		return res, err
	}
	if res.Nodes, err = count(ttx, "tree_node", "symbol"); err != nil {
		return res, err
	}
	verb := "deleted"
	if cfg.DryRun {
		verb = "would delete"
	} else {
		if err := rtx.Commit(); err != nil {
			return res, err
		}
		if err := ttx.Commit(); err != nil {
			return res, err
		}
	}
	fmt.Fprintf(cfg.Out, "%s %d tree models (%d nodes) from %s and %d tree strategies from %s\n",
		verb, res.Models, res.Nodes, cfg.TreeDB, res.Strategies, cfg.StrategiesDB)
	return res, nil
}

var digitRun = regexp.MustCompile(`[0-9]+`)
