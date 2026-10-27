package killswitch

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func TestSwitchDefaultsOffAndPersistsWhoAndWhy(t *testing.T) {
	db := openDB(t)
	defer db.Close()
	if s, err := Current(db); err != nil || s.Engaged {
		t.Fatalf("a fresh database must be OFF, got %+v (err %v)", s, err)
	}
	at := time.Date(2026, 9, 24, 14, 0, 0, 0, time.UTC)
	s, err := Set(db, true, "darian", "TSLL misbehaving", at)
	if err != nil || !s.Engaged || s.ChangedBy != "darian" || s.Reason != "TSLL misbehaving" || !s.ChangedAt.Equal(at) {
		t.Fatalf("engaged state = %+v (err %v)", s, err)
	}
	if got, _ := Current(db); got != s {
		t.Errorf("Current = %+v, want the persisted %+v", got, s)
	}
	if !strings.Contains(s.String(), "ENGAGED") || !strings.Contains(s.String(), "darian") {
		t.Errorf("String() = %q", s.String())
	}
	off, err := Set(db, false, "darian", "fixed", at.Add(time.Hour))
	if err != nil || off.Engaged || !strings.Contains(off.String(), "OFF") {
		t.Errorf("released state = %+v (err %v)", off, err)
	}
	var rows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM killswitch`).Scan(&rows)
	if rows != 1 {
		t.Errorf("the switch is one row, got %d", rows)
	}
}

func TestKillAllCancelsEveryRunningExecutionAndNothingElse(t *testing.T) {
	ctxA, doneA := Track(context.Background(), "scan_market")
	ctxB, doneB := Track(context.Background(), "submit_orders")
	ctxC, doneC := Track(context.Background(), "manage_exits")
	doneC() // finished before the switch was thrown

	if got := Running(); len(got) != 2 {
		t.Fatalf("Running = %+v, want the two unfinished executions", got)
	}
	killed := KillAll()
	if !reflect.DeepEqual(killed, []string{"scan_market", "submit_orders"}) {
		t.Errorf("killed = %v", killed)
	}
	for name, ctx := range map[string]context.Context{"scan_market": ctxA, "submit_orders": ctxB} {
		select {
		case <-ctx.Done():
			if cause := context.Cause(ctx); !errors.Is(cause, ErrKilled) {
				t.Errorf("%s: cause = %v, want ErrKilled", name, cause)
			}
		default:
			t.Errorf("%s was not cancelled", name)
		}
	}
	if ctxC.Err() == nil {
		// done() cancels its own context; what matters is that it was not killed.
		t.Log("finished execution context still live")
	} else if errors.Is(context.Cause(ctxC), ErrKilled) {
		t.Error("an execution that had already finished must not be reported as killed")
	}
	doneA()
	doneB()
	if len(Running()) != 0 {
		t.Errorf("nothing should be running now: %+v", Running())
	}
	if got := KillAll(); len(got) != 0 {
		t.Errorf("killing with nothing running returned %v", got)
	}
}

func TestTrackDerivesFromTheParentContext(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, done := Track(parent, "check_health")
	defer done()
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancelling the parent must cancel the tracked context")
	}
}
