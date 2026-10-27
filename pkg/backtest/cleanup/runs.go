package cleanup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

const lockFile = "pipeline.lock"

// Run is one numbered pipeline run folder under the reports folder.
type Run struct {
	Name       string
	Dir        string
	Bytes      int64
	Files      int // files other than the lock
	Newest     time.Time
	Lock       string         // "" none, "live" or "dead" (the owner process is gone)
	Steps      map[string]int // pipeline_step status -> count; nil without a readable pipeline.db
	Referenced bool           // a strategy's latest backtest lives in this folder
}

// Empty reports whether the folder holds nothing but possibly a lock.
func (r Run) Empty() bool { return r.Files == 0 }

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	return pid > 0 && err == nil && p.Signal(syscall.Signal(0)) == nil
}

func lockState(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, lockFile))
	if err != nil {
		return ""
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if processAlive(pid) {
		return "live"
	}
	return "dead"
}

var runDirRe = regexp.MustCompile(`(?:^|/)reports/(\d+)/`)

// referenced returns the run folder names that strategy_last_backtest points into.
func referenced(strategiesDB string) map[string]bool {
	out := map[string]bool{}
	db, err := storage.OpenSQLiteReadOnly(strategiesDB)
	if err != nil {
		return out
	}
	defer db.Close()
	var paths []string
	if err := db.Select(&paths, `SELECT DISTINCT result_db FROM strategy_last_backtest`); err != nil {
		return out
	}
	for _, p := range paths {
		if m := runDirRe.FindStringSubmatch(filepath.ToSlash(p)); m != nil {
			out[m[1]] = true
		}
	}
	return out
}

func inspect(dir, name string, refs map[string]bool) Run {
	r := Run{Name: name, Dir: dir, Lock: lockState(dir), Referenced: refs[name]}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		r.Bytes += info.Size()
		if info.ModTime().After(r.Newest) {
			r.Newest = info.ModTime()
		}
		if p != filepath.Join(dir, lockFile) {
			r.Files++
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(dir, "pipeline.db")); err == nil {
		if db, err := storage.OpenSQLiteReadOnly(filepath.Join(dir, "pipeline.db")); err == nil {
			var rows []struct {
				Status string `db:"status"`
				N      int    `db:"n"`
			}
			if db.Select(&rows, `SELECT status, count(*) AS n FROM pipeline_step GROUP BY status`) == nil {
				r.Steps = map[string]int{}
				for _, x := range rows {
					r.Steps[x.Status] = x.N
				}
			}
			db.Close()
		}
	}
	return r
}

// Scan inspects every numbered run folder in reports, in numeric order, with
// Workers goroutines.
func Scan(cfg Config) ([]Run, error) {
	entries, err := os.ReadDir(cfg.Reports)
	if err != nil {
		return nil, err
	}
	refs := referenced(cfg.StrategiesDB)
	var names []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, _ := strconv.Atoi(names[i])
		b, _ := strconv.Atoi(names[j])
		return a < b
	})
	runs := make([]Run, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, Workers(cfg.Workers))
	for i, n := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			runs[i] = inspect(filepath.Join(cfg.Reports, n), n, refs)
		}()
	}
	wg.Wait()
	return runs, nil
}

func human(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%dK", b>>10)
}

// Report prints one line per run folder: size, age, lock, step statuses and
// whether a strategy's latest backtest depends on it. It changes nothing.
func Report(cfg Config) error {
	runs, err := Scan(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(cfg.Out, "%-5s %7s %-10s %-5s %-24s %s\n", "run", "size", "modified", "lock", "steps", "notes")
	for _, r := range runs {
		var steps []string
		for _, s := range []string{"done", "failed", "running", "skipped"} {
			if n := r.Steps[s]; n > 0 {
				steps = append(steps, fmt.Sprintf("%s:%d", s, n))
			}
		}
		var notes []string
		if r.Empty() {
			notes = append(notes, "empty")
		}
		if r.Lock == "dead" {
			notes = append(notes, "dead lock")
		}
		if r.Steps["running"] > 0 && r.Lock != "live" {
			notes = append(notes, "aborted")
		}
		if r.Referenced {
			notes = append(notes, "latest backtest of a strategy")
		}
		mod := "-"
		if !r.Newest.IsZero() {
			mod = r.Newest.Format("2006-01-02")
		}
		fmt.Fprintf(cfg.Out, "%-5s %7s %-10s %-5s %-24s %s\n", r.Name, human(r.Bytes), mod, r.Lock, strings.Join(steps, " "), strings.Join(notes, ", "))
	}
	fmt.Fprintln(cfg.Out)
	cfg.RemoveSQL = false
	_, err = SQL(cfg)
	return err
}

// RunsResult is what Runs did, or would do.
type RunsResult struct {
	Locks, Resets, Removed int
	Freed                  int64
}

// Runs repairs aborted pipeline runs: a lock whose process is gone is removed,
// steps stuck in "running" become "failed" (so `pipeline -run-id N` retries
// them), and an empty folder is deleted. With cfg.Failed it also deletes a
// folder where steps ran but none finished, unless a strategy's latest
// backtest lives in it. A folder held by a live pipeline is never touched.
func Runs(cfg Config) (RunsResult, error) {
	var res RunsResult
	runs, err := Scan(cfg)
	if err != nil {
		return res, err
	}
	verb, total := "", "found"
	if cfg.DryRun {
		verb, total = "would ", "would handle"
	} else {
		total = "handled"
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		sem      = make(chan struct{}, Workers(cfg.Workers))
	)
	for _, r := range runs {
		if r.Lock == "live" {
			fmt.Fprintf(cfg.Out, "  run %s: in use by a live pipeline, left alone\n", r.Name)
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var lines []string
			var out RunsResult
			fail := func(err error) {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("run %s: %w", r.Name, err)
				}
				mu.Unlock()
			}
			doomed := ""
			switch {
			case r.Empty():
				doomed = "empty"
			case cfg.Failed && !r.Referenced && len(r.Steps) > 0 && r.Steps["done"] == 0:
				doomed = "no step finished"
			}
			if doomed != "" {
				lines = append(lines, fmt.Sprintf("  run %s: %sdelete folder (%s, %s)", r.Name, verb, doomed, human(r.Bytes)))
				if !cfg.DryRun {
					if err := os.RemoveAll(r.Dir); err != nil {
						fail(err)
						return
					}
				}
				out.Removed, out.Freed = 1, r.Bytes
			} else {
				if r.Lock == "dead" {
					lines = append(lines, fmt.Sprintf("  run %s: %sremove dead lock", r.Name, verb))
					if !cfg.DryRun {
						os.Remove(filepath.Join(r.Dir, lockFile))
					}
					out.Locks = 1
				}
				if n := r.Steps["running"]; n > 0 {
					lines = append(lines, fmt.Sprintf("  run %s: %smark %d aborted step(s) failed", r.Name, verb, n))
					if !cfg.DryRun {
						if err := failRunning(filepath.Join(r.Dir, "pipeline.db")); err != nil {
							fail(err)
							return
						}
					}
					out.Resets = n
				}
			}
			mu.Lock()
			for _, l := range lines {
				fmt.Fprintln(cfg.Out, l)
			}
			res.Locks += out.Locks
			res.Resets += out.Resets
			res.Removed += out.Removed
			res.Freed += out.Freed
			mu.Unlock()
		}()
	}
	wg.Wait()
	fmt.Fprintf(cfg.Out, "%s: %d dead locks, %d aborted steps, %d folders (%s)\n", total, res.Locks, res.Resets, res.Removed, human(res.Freed))
	return res, firstErr
}

func failRunning(path string) error {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`UPDATE pipeline_step SET status = 'failed', finished_at = ?, error = 'aborted (process gone)' WHERE status = 'running'`,
		time.Now().UTC().Format(time.RFC3339))
	return err
}
