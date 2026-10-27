// Package job_runs records the run history of every orchestrator job (cron
// or CLI) so the dashboard can tell "never run" apart from "ran, zero
// results".
package job_runs

import (
	"database/sql"
	"fmt"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// Job run statuses recorded for the dashboard empty-state.
const (
	JobRunOK     = "OK"
	JobRunEmpty  = "EMPTY" // ran successfully but produced no rows for its output table
	JobRunFailed = "FAILED"
)

// JobRun is one execution of an orchestrator job (cron or CLI).
type JobRun struct {
	ID          int64
	Job         string
	Status      string
	Summary     string
	RowsWritten int
	StartedAt   time.Time
	FinishedAt  time.Time
}

// EnsureSchema creates the job_run table if it does not already exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("job_runs/ensure_schema"))
	return err
}

// RecordJobRun appends a finished job execution so the dashboard can tell
// "never run" apart from "ran, zero results".
func RecordJobRun(db *sql.DB, run JobRun) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("RecordJobRun: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return 0, err
	}
	if run.Job == "" {
		return 0, fmt.Errorf("RecordJobRun: empty job name")
	}
	if run.Status == "" {
		run.Status = JobRunOK
	}
	if run.FinishedAt.IsZero() {
		run.FinishedAt = time.Now().UTC()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = run.FinishedAt
	}
	res, err := db.Exec(orchestratorsql.Q("job_runs/record_job_run"),
		run.Job, run.Status, run.Summary, run.RowsWritten,
		run.StartedAt.UTC().Format(time.RFC3339),
		run.FinishedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// LastJobRun returns the most recent run for job, or nil if it has never run.
func LastJobRun(db *sql.DB, job string) (*JobRun, error) {
	if db == nil {
		return nil, fmt.Errorf("LastJobRun: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return nil, err
	}
	return lastRun(db, job)
}

func lastRun(db *sql.DB, job string) (*JobRun, error) {
	row := db.QueryRow(orchestratorsql.Q("job_runs/last_run"), job)
	var r JobRun
	var started, finished string
	err := row.Scan(&r.ID, &r.Job, &r.Status, &r.Summary, &r.RowsWritten, &started, &finished)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t, e := time.Parse(time.RFC3339, started); e == nil {
		r.StartedAt = t
	}
	if t, e := time.Parse(time.RFC3339, finished); e == nil {
		r.FinishedAt = t
	}
	return &r, nil
}

// MaxID returns the highest job_run id for job, 0 when it has never run. A
// caller notes it before a run and passes it to RecordIfNone afterwards.
func MaxID(db *sql.DB, job string) (int64, error) {
	if db == nil {
		return 0, fmt.Errorf("MaxID: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return 0, err
	}
	var id int64
	err := db.QueryRow(orchestratorsql.Q("job_runs/max_id"), job).Scan(&id)
	return id, err
}

// RecordIfNone records run only when the job wrote no job_run row since
// beforeID, so a package that records its own richer row is never doubled up.
// It reports whether it wrote one.
func RecordIfNone(db *sql.DB, beforeID int64, run JobRun) (bool, error) {
	now, err := MaxID(db, run.Job)
	if err != nil {
		return false, err
	}
	if now > beforeID {
		return false, nil
	}
	if _, err := RecordJobRun(db, run); err != nil {
		return false, err
	}
	return true, nil
}
