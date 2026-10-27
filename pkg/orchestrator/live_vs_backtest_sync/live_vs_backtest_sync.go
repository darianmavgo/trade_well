// Package live_vs_backtest_sync keeps the backtest of every deployed strategy. For each strategy on the
// allowlist it finds the newest backtest run that has both an in-sample and an out-of-sample
// pass, copies that run's result databases into an archive folder that mirrors the reports
// layout (<run>/*.db and <run>/oos/*.db), and records which run each number came from in
// live_vs_backtest_deployed. The archive is what /live_vs_backtest_winrate and `live_vs_backtest_winrate -reports-dir` read, so the page
// keeps showing the backtest that was deployed even after the reports folder is cleaned up.
//
// It is rsync in spirit: a file whose source size and modification time match the last sync
// and whose copy still exists is skipped. The copy is a VACUUM INTO snapshot, so a database in
// the middle of a write, or with a WAL file beside it, is copied consistently.
package live_vs_backtest_sync

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtables"
)

// JobName is the CLI subcommand and the log prefix.
const JobName = "live_vs_backtest_sync"

// sqlFiles is this package's folder of SQL, sql/orchestrator/live_vs_backtest_sync.
var sqlFiles = orchestratorsql.Dir("live_vs_backtest_sync")

// syncTables are the slice tables of one sync; each run has its own copy of them in Turso.
var syncTables = []string{"bs_found", "bs_trades", "bs_run_both", "bs_best_run", "bs_newest", "bs_pick", "bs_pick_trades"}

// keepRuns is how many syncs' tables are kept for audit.
const keepRuns = 3

// Options is one sync. ReportsDir is a backtestgosqlite reports folder (data/reports). DestDir
// is the archive. IDs are the deployed strategies.
type Options struct {
	ReportsDir string
	DestDir    string
	IDs        []string
	DryRun     bool
}

// Status of one archived file after a sync.
const (
	StatusNew       = "NEW"
	StatusUpdated   = "UPDATED"
	StatusUnchanged = "UNCHANGED"
)

// Entry is one strategy and pass: where it came from, where it is kept.
type Entry struct {
	Strategy    string
	Pass        string // "is" or "oos"
	Run         int
	Source      string
	Archive     string
	Trades      int
	WinRate     float64
	WindowStart string
	WindowEnd   string
	Note        string // same_run or split_runs
	Status      string
}

// Result is what a sync found. Missing are strategies with no backtest result at all.
type Result struct {
	Entries []Entry
	Missing []string
}

// EnsureSchema creates live_vs_backtest_deployed. It only adds.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("live_vs_backtest_sync/ensure_schema"))
	return err
}

// Run syncs. With DryRun it reads the reports folder and the archive, and changes neither
// the archive nor db.
func Run(ctx context.Context, db *sql.DB, opt Options) (Result, error) {
	var res Result
	if opt.ReportsDir == "" || opt.DestDir == "" {
		return res, fmt.Errorf("live_vs_backtest_sync: a reports folder and an archive folder are both required")
	}
	if len(opt.IDs) == 0 {
		return res, fmt.Errorf("live_vs_backtest_sync: no deployed strategies: set STRATEGY_ALLOWLIST")
	}
	if st, err := os.Stat(opt.ReportsDir); err != nil || !st.IsDir() {
		return res, fmt.Errorf("live_vs_backtest_sync: reports folder %s: not a folder", opt.ReportsDir)
	}
	if samePath(opt.ReportsDir, opt.DestDir) {
		return res, fmt.Errorf("live_vs_backtest_sync: the archive folder is the reports folder; pick a different one")
	}
	files, err := resultFiles(opt.ReportsDir)
	if err != nil {
		return res, err
	}
	// The calculation runs in Turso, in tables of its own (see runtables); the newest few runs are
	// kept for audit and older ones dropped.
	calc := runtables.Begin(db, syncTables)
	defer func() { _ = runtables.Prune(context.WithoutCancel(ctx), db, syncTables, keepRuns) }()
	want := map[string]bool{}
	for _, id := range opt.IDs {
		want[id] = true
	}
	schema, err := fs.ReadFile(sqlFiles, "00_schema.sql")
	if err != nil {
		return res, fmt.Errorf("live_vs_backtest_sync: %w", err)
	}
	if err := calc.Exec(ctx, string(schema)); err != nil {
		return res, fmt.Errorf("live_vs_backtest_sync: %w", err)
	}
	for _, f := range files {
		if err := scanBacktestResultAssetPerformanceSummary(ctx, calc, f, want); err != nil {
			return res, err
		}
	}
	for _, f := range []string{"01_best_run.sql", "02_newest_pass.sql", "03_pick.sql", "04_pick_trades.sql"} {
		b, err := fs.ReadFile(sqlFiles, f)
		if err != nil {
			return res, fmt.Errorf("live_vs_backtest_sync: %w", err)
		}
		if err := calc.Exec(ctx, string(b)); err != nil {
			return res, fmt.Errorf("live_vs_backtest_sync: %s: %w", f, err)
		}
	}
	rows, err := calc.Query(ctx, orchestratorsql.Q("live_vs_backtest_sync/run"))
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var e Entry
		var rel string
		if err := rows.Scan(&e.Strategy, &e.Pass, &e.Run, &e.Source, &rel, &e.Trades, &e.WinRate, &e.WindowStart, &e.WindowEnd, &e.Note); err != nil {
			rows.Close()
			return res, err
		}
		e.Archive = filepath.Join(opt.DestDir, rel)
		res.Entries = append(res.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	rows.Close()
	have := map[string]bool{}
	for _, e := range res.Entries {
		have[e.Strategy] = true
	}
	for _, id := range opt.IDs {
		if !have[id] {
			res.Missing = append(res.Missing, id)
		}
	}
	sort.Strings(res.Missing)

	if !opt.DryRun {
		if err := EnsureSchema(db); err != nil {
			return res, err
		}
	}
	copied := map[string]bool{} // a result file holds several strategies; copy it once
	for i := range res.Entries {
		e := &res.Entries[i]
		st, err := os.Stat(e.Source)
		if err != nil {
			return res, fmt.Errorf("live_vs_backtest_sync: %w", err)
		}
		mtime := st.ModTime().UTC().Format(time.RFC3339Nano)
		e.Status, err = status(ctx, db, e, st.Size(), mtime, opt.DryRun)
		if err != nil {
			return res, err
		}
		if opt.DryRun {
			continue
		}
		if e.Status != StatusUnchanged && !copied[e.Archive] {
			if err := copyBacktestResultAssetSnapshot(ctx, e.Source, e.Archive); err != nil {
				return res, err
			}
			copied[e.Archive] = true
		}
		if _, err := db.ExecContext(ctx, orchestratorsql.Q("live_vs_backtest_sync/run_2"),
			e.Strategy, e.Pass, e.Run, e.Source, st.Size(), mtime, e.Archive, e.Trades, e.WinRate,
			e.WindowStart, e.WindowEnd, e.Note, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return res, err
		}
	}
	if opt.DryRun {
		return res, nil
	}
	return res, saveTrades(ctx, db, calc, res.Entries)
}

// saveTrades replaces the stored backtest trades of each synced strategy and pass.
func saveTrades(ctx context.Context, db *sql.DB, calc *runtables.Run, entries []Entry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range entries {
		if _, err := tx.ExecContext(ctx, orchestratorsql.Q("live_vs_backtest_sync/save_trades"), e.Strategy, e.Pass); err != nil {
			return err
		}
		rows, err := calc.Query(ctx, orchestratorsql.Q("live_vs_backtest_sync/save_trades_2"), e.Strategy, e.Pass)
		if err != nil {
			return err
		}
		var batch [][]any
		for rows.Next() {
			var sym, date string
			var win int
			var ret sql.NullFloat64
			if err := rows.Scan(&sym, &date, &win, &ret); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, []any{e.Strategy, e.Pass, sym, date, win, ret})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := runtables.InsertRows(ctx, tx, "live_vs_backtest_deployed_trade", []string{"strategy_id", "pass", "symbol", "exit_date", "win", "return_pct"}, batch); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return tx.Commit()
}

// Format writes the sync as a text table.
func Format(w io.Writer, r Result) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STRATEGY\tPASS\tRUN\tWINDOW\tTRADES\tWIN RATE\tSTATUS\tNOTE")
	for _, e := range r.Entries {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s to %s\t%d\t%.0f%%\t%s\t%s\n", e.Strategy, e.Pass, e.Run, e.WindowStart, e.WindowEnd, e.Trades, e.WinRate*100, e.Status, e.Note)
	}
	tw.Flush()
	for _, id := range r.Missing {
		fmt.Fprintf(w, "[%s] %s: no backtest result found in the reports folder; run `backtest -strategy %s` first\n", JobName, id, id)
	}
	for _, e := range r.Entries {
		if e.Note == "split_runs" {
			fmt.Fprintf(w, "[%s] %s: no single run has both passes; in-sample and out-of-sample come from different runs\n", JobName, e.Strategy)
			break
		}
	}
}

// status says whether the archive already holds this exact source file.
func status(ctx context.Context, db *sql.DB, e *Entry, size int64, mtime string, dry bool) (string, error) {
	var path, m string
	var sz int64
	var run int
	err := db.QueryRowContext(ctx, orchestratorsql.Q("live_vs_backtest_sync/status"), e.Strategy, e.Pass).Scan(&path, &sz, &m, &run)
	if err != nil {
		// No row (or no table yet): the strategy has never been synced.
		return StatusNew, nil
	}
	if _, serr := os.Stat(e.Archive); serr == nil && path == e.Source && sz == size && m == mtime && run == e.Run {
		return StatusUnchanged, nil
	}
	return StatusUpdated, nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func samePath(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && x == y
}

type resultFile struct {
	path, rel, pass string
	run             int
}

// resultFiles lists <reports>/<run>/*.db (in-sample) and <reports>/<run>/oos/*.db (held out).
func resultFiles(dir string) ([]resultFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []resultFile
	for _, e := range entries {
		run, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		for _, k := range []struct{ sub, pass string }{{"", "is"}, {"oos", "oos"}} {
			dbs, _ := filepath.Glob(filepath.Join(dir, e.Name(), k.sub, "*.db"))
			for _, p := range dbs {
				out = append(out, resultFile{path: p, rel: filepath.Join(e.Name(), k.sub, filepath.Base(p)), pass: k.pass, run: run})
			}
		}
	}
	return out, nil
}
