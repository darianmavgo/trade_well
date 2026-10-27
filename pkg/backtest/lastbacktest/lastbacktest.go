// Package lastbacktest finds each strategy's most recent backtest in the result
// DBs and records where it lives in strategies.db (table strategy_last_backtest).
package lastbacktest

import (
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"sync"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

// MaxWorkers caps the number of result DBs read concurrently.
const MaxWorkers = 32

// Result is one strategy's most recent backtest.
type Result struct {
	Created  string
	WinRate  float64
	Trades   int64
	ResultDB string // path as scanned
}

// ResultDBs lists the result DBs to read: reports/*.db and reports/<N>/*.db.
func ResultDBs(dir string) ([]string, error) {
	top, err := filepath.Glob(filepath.Join(dir, "*.db"))
	if err != nil {
		return nil, err
	}
	runs, err := filepath.Glob(filepath.Join(dir, "[0-9]*", "*.db"))
	if err != nil {
		return nil, err
	}
	out := append(top, runs...)
	sort.Strings(out)
	return out, nil
}

// Latest reads every result DB with up to workers goroutines and returns the
// most recent backtest per strategy id.
func Latest(paths []string, workers int) (map[string]Result, error) {
	if workers < 1 {
		workers = 1
	}
	if workers > MaxWorkers {
		workers = MaxWorkers
	}
	var (
		mu     sync.Mutex
		latest = map[string]Result{}
		errs   []error
		wg     sync.WaitGroup
	)
	jobs := make(chan string)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				rows, err := readSummary(p)
				mu.Lock()
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", p, err))
				}
				for id, r := range rows {
					if cur, ok := latest[id]; !ok || r.Created > cur.Created {
						latest[id] = r
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	if len(errs) > 0 {
		return latest, errs[0]
	}
	return latest, nil
}

// readSummary returns the newest performance_summary row per strategy in one
// result DB. A DB without the table (a scoreboard, say) yields nothing.
func readSummary(path string) (map[string]Result, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='performance_summary'`).Scan(&n); err != nil || n == 0 {
		return nil, err
	}
	rows, err := db.Query(`SELECT strategy_id, COALESCE(created_at,''), win_rate, COALESCE(total_trades,0) FROM performance_summary`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Result{}
	for rows.Next() {
		var id string
		var wr sql.NullFloat64
		r := Result{ResultDB: path}
		if err := rows.Scan(&id, &r.Created, &wr, &r.Trades); err != nil {
			return nil, err
		}
		r.WinRate = wr.Float64
		if !wr.Valid {
			r.Trades = 0 // no win rate: treated as no trades
		}
		if cur, ok := out[id]; !ok || r.Created > cur.Created {
			out[id] = r
		}
	}
	return out, rows.Err()
}

// Config holds the settings of Run.
type Config struct {
	StrategiesDB string // strategies.db to write
	ReportsDir   string // reports folder holding the result DBs
	BaseDir      string // result DB paths are stored relative to this (the app folder)
	Workers      int
	Out          io.Writer
}

// Run scans the reports folder and replaces strategy_last_backtest with the
// latest backtest per strategy. It returns the number of rows written.
func Run(cfg Config) (int, error) {
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	paths, err := ResultDBs(cfg.ReportsDir)
	if err != nil {
		return 0, err
	}
	latest, err := Latest(paths, cfg.Workers)
	if err != nil {
		return 0, err
	}
	base, err := filepath.Abs(cfg.BaseDir)
	if err != nil {
		return 0, err
	}
	db, err := refdb.Open(cfg.StrategiesDB)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM strategy_last_backtest`); err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`INSERT INTO strategy_last_backtest (strategy_id, result_db, created_at, win_rate, total_trades) VALUES (?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for id, r := range latest {
		abs, err := filepath.Abs(r.ResultDB)
		if err != nil {
			return 0, err
		}
		rel, err := filepath.Rel(base, abs)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", r.ResultDB, err)
		}
		if _, err := stmt.Exec(id, filepath.ToSlash(rel), r.Created, r.WinRate, r.Trades); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	fmt.Fprintf(cfg.Out, "read %d result DBs, wrote %d strategies to strategy_last_backtest in %s\n", len(paths), len(latest), cfg.StrategiesDB)
	return len(latest), nil
}
