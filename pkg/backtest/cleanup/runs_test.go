package cleanup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// makeRun builds a run folder with a real pipeline.db holding the given steps
// (name -> status) and a lock naming pid (0 = no lock).
func makeRun(t *testing.T, reports, name string, steps map[string]string, pid int) string {
	t.Helper()
	dir := filepath.Join(reports, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if steps != nil {
		db, err := storage.OpenSQLite(filepath.Join(dir, "pipeline.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := storage.RunStage(db, "pipeline", nil); err != nil {
			t.Fatal(err)
		}
		i := 0
		for n, s := range steps {
			i++
			if _, err := db.Exec(`INSERT INTO pipeline_step (seq, name, status, error) VALUES (?, ?, ?, '')`, i, n, s); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
	}
	if pid > 0 {
		os.WriteFile(filepath.Join(dir, lockFile), []byte(fmt.Sprintf("%d\n", pid)), 0o644)
	}
	return dir
}

func TestRuns(t *testing.T) {
	reports := t.TempDir()
	empty := makeRun(t, reports, "1", nil, 0)
	dead := makeRun(t, reports, "2", map[string]string{"backtest": "done", "validate": "running"}, 999999999)
	live := makeRun(t, reports, "3", map[string]string{"backtest": "running"}, os.Getpid())
	allFailed := makeRun(t, reports, "4", map[string]string{"backtest": "failed"}, 0)
	var out bytes.Buffer
	cfg := Config{Reports: reports, StrategiesDB: filepath.Join(reports, "none.db"), Workers: 1, Out: &out}

	cfg.DryRun = true
	if _, err := Runs(cfg); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{empty, dead, live, allFailed} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("dry run removed %s", d)
		}
	}

	cfg.DryRun, cfg.Failed = false, true
	res, err := Runs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Locks != 1 || res.Resets != 1 || res.Removed != 2 {
		t.Fatalf("locks/resets/removed = %d/%d/%d\n%s", res.Locks, res.Resets, res.Removed, out.String())
	}
	if _, err := os.Stat(empty); err == nil {
		t.Error("empty run kept")
	}
	if _, err := os.Stat(allFailed); err == nil {
		t.Error("run with no finished step kept")
	}
	if _, err := os.Stat(filepath.Join(dead, lockFile)); err == nil {
		t.Error("dead lock kept")
	}
	if _, err := os.Stat(filepath.Join(live, lockFile)); err != nil {
		t.Error("live lock removed")
	}
	runs, _ := Scan(cfg)
	for _, r := range runs {
		switch r.Name {
		case "2":
			if r.Steps["failed"] != 1 || r.Steps["running"] != 0 || r.Steps["done"] != 1 {
				t.Errorf("dead run steps = %v", r.Steps)
			}
		case "3":
			if r.Steps["running"] != 1 {
				t.Errorf("live run steps changed: %v", r.Steps)
			}
		}
	}
}

func TestWorkersBounds(t *testing.T) {
	for in, want := range map[int]int{-5: 10, 0: 10, 3: 10, 10: 10, 20: 20, 32: 32, 100: 32} {
		if got := Workers(in); got != want {
			t.Errorf("Workers(%d) = %d, want %d", in, got, want)
		}
	}
}
