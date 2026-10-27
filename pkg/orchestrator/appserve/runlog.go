package appserve

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

// statusRecorder remembers the HTTP status and the first line of the body a
// cron handler wrote, which is where every handler puts its one-line outcome
// ("OK manage_exits accounts=3") or its error (http.Error).
type statusRecorder struct {
	http.ResponseWriter
	status int
	first  strings.Builder
	done   bool
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.done {
		s := string(b)
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s, r.done = s[:i], true
		}
		if r.first.Len() < 500 {
			r.first.WriteString(s)
		}
	}
	return r.ResponseWriter.Write(b)
}

// recordRun makes sure every cron invocation leaves a job_run row, success or
// failure, so "did the job run, and what happened" is always answerable. It is
// a fallback: a job whose package already records its own row (scan_market,
// stage_orders, submit_orders) is not recorded twice. It records inside
// the job, so the row is written to the same Turso database as the run itself.
func recordRun(job string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		started := time.Now().UTC()
		var beforeID int64
		cfg, cfgErr := appconfig.Load()
		if cfgErr == nil {
			if db, err := bootstrap.OpenDB(cfg.DBPath); err == nil {
				beforeID, _ = job_runs.MaxID(db, job)
				_ = db.Close()
			}
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		finish := func(status, summary string) {
			if cfgErr != nil {
				log.Printf("[%s] job_run not recorded: config: %v", job, cfgErr)
				return
			}
			db, err := bootstrap.OpenDB(cfg.DBPath)
			if err != nil {
				log.Printf("[%s] job_run not recorded: open db: %v", job, err)
				return
			}
			defer db.Close()
			wrote, err := job_runs.RecordIfNone(db, beforeID, job_runs.JobRun{
				Job: job, Status: status, Summary: summary, StartedAt: started, FinishedAt: time.Now().UTC(),
			})
			if err != nil {
				log.Printf("[%s] job_run write failed: %v", job, err)
			} else if wrote {
				log.Printf("[%s] job_run recorded status=%s summary=%q", job, status, summary)
			}
		}

		defer func() {
			if p := recover(); p != nil {
				finish(job_runs.JobRunFailed, fmt.Sprintf("panic: %v", p))
				panic(p) // cronOnly turns it into the failure response and email
			}
		}()
		next(rec, r)

		summary := strings.TrimSpace(rec.first.String())
		if rec.status >= 400 {
			finish(job_runs.JobRunFailed, fmt.Sprintf("HTTP %d: %s", rec.status, summary))
			return
		}
		finish(job_runs.JobRunOK, summary)
	}
}
