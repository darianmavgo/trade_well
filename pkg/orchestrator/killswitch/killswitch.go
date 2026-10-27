// Package killswitch is the emergency stop for the scheduled pipeline. Engaging it
// does two things: it persists a flag that every /cron/<job> route checks (so the
// scheduler, a manual trigger and a live e2e run all get refused), and it cancels
// every execution that is running right now (their contexts, so an in-flight
// broker call is abandoned). The flag lives in the database, so it survives an
// instance restart, because it is stored in Turso with the rest of the state.
//
// It stops jobs; it does not touch positions. exit_market is the tool that sells.
package killswitch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// JobName is this capability's name everywhere: package, route, CLI subcommand.
const JobName = "killswitch"

// ErrKilled is the cause of a context cancelled by KillAll (context.Cause).
var ErrKilled = errors.New("killed by the killswitch")

// State is the persisted switch.
type State struct {
	Engaged   bool
	ChangedAt time.Time
	ChangedBy string
	Reason    string
}

// String describes the state for a log line, a refusal or a page.
func (s State) String() string {
	if !s.Engaged {
		return "killswitch is OFF: cron jobs run"
	}
	return fmt.Sprintf("killswitch is ENGAGED since %s by %s: %s", s.ChangedAt.UTC().Format(time.RFC3339), orDash(s.ChangedBy), orDash(s.Reason))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// EnsureSchema creates the single-row killswitch table.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS killswitch (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	engaged INTEGER NOT NULL DEFAULT 0,
	changed_at DATETIME,
	changed_by TEXT,
	reason TEXT
)`)
	return err
}

// Current reads the switch. A database that has never had it set is OFF.
func Current(db *sql.DB) (State, error) {
	if db == nil {
		return State{}, fmt.Errorf("killswitch: nil db")
	}
	if err := EnsureSchema(db); err != nil {
		return State{}, err
	}
	var s State
	var engaged int
	var at sql.NullTime
	var by, reason sql.NullString
	err := db.QueryRow(`SELECT engaged, changed_at, changed_by, reason FROM killswitch WHERE id = 1`).Scan(&engaged, &at, &by, &reason)
	if err == sql.ErrNoRows {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	s.Engaged, s.ChangedBy, s.Reason = engaged == 1, by.String, reason.String
	if at.Valid {
		s.ChangedAt = at.Time
	}
	return s, nil
}

// Set turns the switch on or off, recording who and why, and returns the new state.
func Set(db *sql.DB, engaged bool, by, reason string, now time.Time) (State, error) {
	if err := EnsureSchema(db); err != nil {
		return State{}, err
	}
	on := 0
	if engaged {
		on = 1
	}
	_, err := db.Exec(`
INSERT INTO killswitch (id, engaged, changed_at, changed_by, reason) VALUES (1, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET engaged = excluded.engaged, changed_at = excluded.changed_at,
	changed_by = excluded.changed_by, reason = excluded.reason`, on, now.UTC(), by, reason)
	if err != nil {
		return State{}, err
	}
	return Current(db)
}

// ---- running executions -----------------------------------------------------

type execution struct {
	name    string
	started time.Time
	cancel  context.CancelCauseFunc
}

var (
	mu    sync.Mutex
	next  int64
	execs = map[int64]*execution{}
)

// Execution is one running job, for display.
type Execution struct {
	Name    string
	Started time.Time
}

// Track derives a context from ctx that KillAll can cancel, and registers it under
// name until done is called. Every cron route runs inside one.
func Track(ctx context.Context, name string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	mu.Lock()
	next++
	id := next
	execs[id] = &execution{name: name, started: time.Now().UTC(), cancel: cancel}
	mu.Unlock()
	return ctx, func() {
		mu.Lock()
		delete(execs, id)
		mu.Unlock()
		cancel(nil)
	}
}

// Running lists the executions in flight, oldest first.
func Running() []Execution {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Execution, 0, len(execs))
	for _, e := range execs {
		out = append(out, Execution{Name: e.name, Started: e.started})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// KillAll cancels every tracked execution (cause ErrKilled) and returns their names.
// A cancelled job's in-flight broker call is abandoned; an order whose placement was
// cut off mid-request is left for submit_orders to reconcile, never blindly re-placed.
func KillAll() []string {
	mu.Lock()
	victims := make([]*execution, 0, len(execs))
	for _, e := range execs {
		victims = append(victims, e)
	}
	mu.Unlock()
	names := make([]string, 0, len(victims))
	for _, e := range victims {
		e.cancel(ErrKilled)
		names = append(names, e.name)
	}
	sort.Strings(names)
	return names
}
