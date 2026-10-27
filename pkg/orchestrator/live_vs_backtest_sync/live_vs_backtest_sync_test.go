package live_vs_backtest_sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func result(t *testing.T, path string, rows ...[]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE performance_summary (strategy_id TEXT, total_trades INTEGER, win_rate REAL, start_date TEXT, end_date TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO performance_summary VALUES (?, ?, ?, '2021-01-01', '2025-10-12')`, r...); err != nil {
			t.Fatal(err)
		}
	}
}

func orch(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func find(r Result, id, pass string) *Entry {
	for i := range r.Entries {
		if r.Entries[i].Strategy == id && r.Entries[i].Pass == pass {
			return &r.Entries[i]
		}
	}
	return nil
}

func TestSyncKeepsTheNewestRunWithBothPasses(t *testing.T) {
	ctx := context.Background()
	reports, dest, db := t.TempDir(), t.TempDir(), orch(t)
	// mull: run 1 and 2 have both passes, run 3 has only an in-sample pass. Run 2 is the one to keep.
	result(t, filepath.Join(reports, "1", "markov.db"), []any{"mull", 10, 0.5})
	result(t, filepath.Join(reports, "1", "oos", "markov.db"), []any{"mull", 5, 0.5})
	result(t, filepath.Join(reports, "2", "markov.db"), []any{"mull", 33, 0.85}, []any{"other", 9, 0.1})
	result(t, filepath.Join(reports, "2", "oos", "markov.db"), []any{"mull", 56, 0.89})
	result(t, filepath.Join(reports, "3", "markov.db"), []any{"mull", 40, 0.7})
	// shpu: the passes come from different runs.
	result(t, filepath.Join(reports, "2", "streak.db"), []any{"shpu", 5, 1.0})
	result(t, filepath.Join(reports, "3", "oos", "streak.db"), []any{"shpu", 18, 1.0})
	opt := Options{ReportsDir: reports, DestDir: dest, IDs: []string{"mull", "shpu", "absent"}}

	dry := opt
	dry.DryRun = true
	r, err := Run(ctx, db, dry)
	if err != nil {
		t.Fatal(err)
	}
	if e := find(r, "mull", "is"); e == nil || e.Run != 2 || e.Trades != 33 || e.Status != StatusNew {
		t.Fatalf("dry run mull is = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(dest, "2", "markov.db")); err == nil {
		t.Fatal("dry run wrote the archive")
	}
	if _, err := db.Exec(`SELECT 1 FROM live_vs_backtest_deployed`); err == nil {
		t.Fatal("dry run created live_vs_backtest_deployed")
	}

	r, err = Run(ctx, db, opt)
	if err != nil {
		t.Fatal(err)
	}
	if e := find(r, "mull", "oos"); e == nil || e.Run != 2 || e.Trades != 56 || e.Note != "same_run" {
		t.Errorf("mull oos = %+v", e)
	}
	if e := find(r, "shpu", "is"); e == nil || e.Run != 2 || e.Note != "split_runs" {
		t.Errorf("shpu is = %+v", e)
	}
	if e := find(r, "shpu", "oos"); e == nil || e.Run != 3 {
		t.Errorf("shpu oos = %+v", e)
	}
	if len(r.Missing) != 1 || r.Missing[0] != "absent" {
		t.Errorf("missing = %v", r.Missing)
	}
	// The archive mirrors the reports layout and holds readable result databases.
	for _, rel := range []string{"2/markov.db", "2/oos/markov.db", "2/streak.db", "3/oos/streak.db"} {
		a, err := sql.Open("sqlite", filepath.Join(dest, rel))
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := a.QueryRow(`SELECT COUNT(*) FROM performance_summary`).Scan(&n); err != nil || n == 0 {
			t.Errorf("archive %s: %d rows, %v", rel, n, err)
		}
		a.Close()
	}
	if _, err := os.Stat(filepath.Join(dest, "1")); err == nil {
		t.Error("an older run was archived")
	}
	var recorded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM live_vs_backtest_deployed`).Scan(&recorded); err != nil || recorded != 4 {
		t.Errorf("live_vs_backtest_deployed rows = %d, %v", recorded, err)
	}

	// A second sync skips what has not changed.
	r, err = Run(ctx, db, opt)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Entries {
		if e.Status != StatusUnchanged {
			t.Errorf("%s %s = %s on the second sync", e.Strategy, e.Pass, e.Status)
		}
	}

	// A newer run replaces the old one, and the status says so.
	time.Sleep(10 * time.Millisecond)
	result(t, filepath.Join(reports, "4", "markov.db"), []any{"mull", 50, 0.8})
	result(t, filepath.Join(reports, "4", "oos", "markov.db"), []any{"mull", 60, 0.9})
	r, err = Run(ctx, db, opt)
	if err != nil {
		t.Fatal(err)
	}
	if e := find(r, "mull", "is"); e == nil || e.Run != 4 || e.Status != StatusUpdated {
		t.Errorf("after run 4: %+v", e)
	}
}

func TestSyncRefusesBadSetup(t *testing.T) {
	db := orch(t)
	dir := t.TempDir()
	for name, o := range map[string]Options{
		"no ids":         {ReportsDir: dir, DestDir: t.TempDir()},
		"missing folder": {ReportsDir: filepath.Join(dir, "nope"), DestDir: t.TempDir(), IDs: []string{"a"}},
		"same folder":    {ReportsDir: dir, DestDir: dir, IDs: []string{"a"}},
	} {
		if _, err := Run(context.Background(), db, o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
