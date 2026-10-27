package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

const (
	stateFile = "pipeline.db"
	lockFile  = "pipeline.lock"
)

// Step statuses as stored in pipeline_step.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// lock claims a run folder for this process. A second pipeline cannot work in
// the same run while the first is alive. A lock left by a process that is gone
// is taken over.
type lock struct{ path string }

func acquireLock(dir string) (*lock, error) {
	path := filepath.Join(dir, lockFile)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return &lock{path: path}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		data, _ := os.ReadFile(path)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 && processAlive(pid) {
			return nil, fmt.Errorf("pipeline: run folder %s is in use by process %d", dir, pid)
		}
		os.Remove(path) // the owner is gone
	}
	return nil, fmt.Errorf("pipeline: could not take the lock in %s", dir)
}

func (l *lock) release() { os.Remove(l.path) }

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// openState opens pipeline.db in the run folder and makes sure its tables exist.
func openState(dir string) (*sqlx.DB, error) {
	db, err := storage.OpenSQLite(filepath.Join(dir, stateFile))
	if err != nil {
		return nil, err
	}
	if err := storage.RunStage(db, "pipeline", nil); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// runRow is the pipeline_run row.
type runRow struct {
	RunID      int    `db:"run_id"`
	Park       string `db:"park"`
	Bench      string `db:"bench"`
	PrimaryID  string `db:"primary_id"`
	Explicit   int    `db:"explicit_strategies"`
	FinishedAt string `db:"finished_at"`
}

func loadRun(db *sqlx.DB) (runRow, bool, error) {
	var r runRow
	err := db.Get(&r, `SELECT run_id, park, bench, primary_id, explicit_strategies, COALESCE(finished_at, '') AS finished_at FROM pipeline_run LIMIT 1`)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return r, false, nil
		}
		return r, false, err
	}
	return r, true, nil
}

func saveRun(db *sqlx.DB, r runRow) error {
	_, err := db.Exec(`INSERT INTO pipeline_run (run_id, started_at, park, bench, primary_id, explicit_strategies) VALUES (?, ?, ?, ?, ?, ?)`,
		r.RunID, now(), r.Park, r.Bench, r.PrimaryID, r.Explicit)
	return err
}

func finishRun(db *sqlx.DB) error {
	_, err := db.Exec(`UPDATE pipeline_run SET finished_at = ?`, now())
	return err
}

// loadScope returns the ids stored under kind, sorted.
func loadScope(db *sqlx.DB, kind string) ([]string, error) {
	var ids []string
	err := db.Select(&ids, `SELECT id FROM pipeline_scope WHERE kind = ? ORDER BY id`, kind)
	return ids, err
}

// addScope records ids under kind and returns how many were new.
func addScope(db *sqlx.DB, kind string, ids []string) (int, error) {
	added := 0
	for _, id := range ids {
		res, err := db.Exec(`INSERT OR IGNORE INTO pipeline_scope (kind, id) VALUES (?, ?)`, kind, id)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, nil
}

// stepStatus is the stored status of a step, or "" when it has not run in this run.
func stepStatus(db *sqlx.DB, name string) (string, error) {
	var s string
	err := db.Get(&s, `SELECT status FROM pipeline_step WHERE name = ?`, name)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return "", nil
		}
		return "", err
	}
	return s, nil
}

func markStep(db *sqlx.DB, seq int, name, status, errText string) error {
	if status == StatusRunning {
		_, err := db.Exec(`INSERT INTO pipeline_step (seq, name, status, started_at, error) VALUES (?, ?, ?, ?, '')
			ON CONFLICT(name) DO UPDATE SET status = excluded.status, started_at = excluded.started_at, finished_at = NULL, error = ''`,
			seq, name, status, now())
		return err
	}
	_, err := db.Exec(`INSERT INTO pipeline_step (seq, name, status, finished_at, error) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET status = excluded.status, finished_at = excluded.finished_at, error = excluded.error`,
		seq, name, status, now(), errText)
	return err
}
