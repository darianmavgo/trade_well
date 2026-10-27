package cleanup

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func putFile(t *testing.T, root, rel, body string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestTablesAndJobs(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	for _, q := range []string{
		`CREATE TABLE job_run (job TEXT, finished_at TEXT)`,
		`CREATE TABLE kept (a)`, `CREATE TABLE dead_one (a)`, `CREATE VIEW dead_view AS SELECT a FROM dead_one`,
		`INSERT INTO dead_one VALUES (1),(2)`,
		`INSERT INTO job_run VALUES ('risk','2026-01-01'),('old_job','2026-01-02'),('old_job','2026-01-03')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	cfg := Config{Out: &out, DryRun: true, Remove: true}
	n, err := Tables(ctx, cfg, db, []string{"kept", "job_run"})
	if err != nil || n != 2 {
		t.Fatalf("Tables dry run = %d, %v\n%s", n, err, out.String())
	}
	if !strings.Contains(out.String(), "would drop") || !strings.Contains(out.String(), "2 rows") {
		t.Fatalf("output: %s", out.String())
	}
	if _, err := db.Exec(`SELECT 1 FROM dead_one`); err != nil {
		t.Fatalf("dry run dropped a table: %v", err)
	}
	cfg.DryRun = false
	if _, err := Tables(ctx, cfg, db, []string{"kept", "job_run"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`SELECT 1 FROM dead_one`); err == nil {
		t.Fatal("dead_one should be dropped")
	}
	if _, err := db.Exec(`SELECT 1 FROM kept`); err != nil {
		t.Fatal("kept should remain")
	}

	out.Reset()
	cfg.DryRun = true
	if n, err := Jobs(ctx, cfg, db, []string{"risk"}); err != nil || n != 1 {
		t.Fatalf("Jobs = %d, %v", n, err)
	}
	cfg.DryRun = false
	if _, err := Jobs(ctx, cfg, db, []string{"risk"}); err != nil {
		t.Fatal(err)
	}
	var left int
	db.QueryRow(`SELECT COUNT(*) FROM job_run`).Scan(&left)
	if left != 1 {
		t.Fatalf("job_run rows left = %d, want 1", left)
	}
}

func TestBins(t *testing.T) {
	root := t.TempDir()
	elf := "\x7fELF....."
	putFile(t, root, "cmd/live/main.go", "package main", 0o644)
	putFile(t, root, "bin/live", elf, 0o755)
	putFile(t, root, "bin/gone", elf, 0o755)
	putFile(t, root, "bin/.gitkeep", "", 0o644)
	putFile(t, root, "live", elf, 0o755)
	putFile(t, root, "run.sh", "#!/bin/sh\n", 0o755)
	var out bytes.Buffer
	stray, err := ScanBins(context.Background(), Config{Root: root, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(stray) != 2 || stray[0].Path != "bin/gone" || stray[1].Path != "live" {
		t.Fatalf("stray = %+v", stray)
	}
	cfg := Config{Root: root, Out: &out, Remove: true}
	if _, err := Bins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"bin/gone", "live"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			t.Errorf("%s should be removed", rel)
		}
	}
	for _, rel := range []string{"bin/live", "run.sh"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s should remain", rel)
		}
	}
}

func TestRefs(t *testing.T) {
	root := t.TempDir()
	putFile(t, root, "cmd/live/main.go", "package main\nvar _ = \"KEPT_VAR\"\n", 0o644)
	putFile(t, root, "pkg/orchestrator/risk/risk.go", "package risk", 0o644)
	putFile(t, root, "docs/a.md", "run ./bin/live and ./bin/gone\nsee pkg/orchestrator/risk and pkg/orchestrator/dashboard\nKEPT_VAR=1 OLD_VAR=2\n", 0o644)
	putFile(t, root, "deploy/cron.yaml", "url: /cron/risk\nurl: /cron/runtimeenv\n", 0o644)
	refs, err := ScanRefs(context.Background(), Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, r.Text)
	}
	want := []string{
		`names /cron/runtimeenv but there is no pkg/orchestrator/runtimeenv`,
		`names bin/cmd "gone" but there is no cmd/gone`,
		`names pkg/orchestrator/dashboard which does not exist`,
		`sets OLD_VAR which no Go file mentions`,
	}
	if len(got) != len(want) {
		t.Fatalf("refs = %q", got)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("missing %q in %q", w, got)
		}
	}
}

func TestDataAndDead(t *testing.T) {
	root := t.TempDir()
	putFile(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n", 0o644)
	putFile(t, root, "cmd/app/main.go", "package main\nimport _ \"example.com/m/pkg/used\"\nvar f = \"live.db\"\n", 0o644)
	putFile(t, root, "pkg/used/u.go", "package used\nimport _ \"example.com/m/pkg/deep\"\n", 0o644)
	putFile(t, root, "pkg/deep/d.go", "package deep", 0o644)
	putFile(t, root, "pkg/orphan/o.go", "package orphan", 0o644)
	putFile(t, root, "pkg/orphan/o_test.go", "package orphan\nimport _ \"example.com/m/pkg/used\"\n", 0o644)
	putFile(t, root, "data/live.db", "x", 0o644)
	putFile(t, root, "data/live.db-wal", "x", 0o644)
	putFile(t, root, "data/old.db", "xx", 0o644)
	putFile(t, root, "data/reports/1/a", "x", 0o644)

	ctx := context.Background()
	dead, err := ScanDead(ctx, Config{Root: root})
	if err != nil || len(dead) != 1 || dead[0] != filepath.Join("pkg", "orphan") {
		t.Fatalf("dead = %v, %v", dead, err)
	}
	orphans, err := ScanData(ctx, Config{Root: root})
	if err != nil || len(orphans) != 1 || orphans[0].Path != filepath.Join("data", "old.db") {
		t.Fatalf("orphans = %+v, %v", orphans, err)
	}
}

func TestRefsFixMovedPkgPaths(t *testing.T) {
	root := t.TempDir()
	putFile(t, root, "pkg/backtest/strategy/s.go", "package strategy", 0o644)
	putFile(t, root, "pkg/orchestrator/risk/r.go", "package risk", 0o644)
	putFile(t, root, "docs/a.md", "uses pkg/strategy and pkg/orchestrator/risk\nold pkg/nothing here\nkeep me\n", 0o644)
	putFile(t, root, "run.sh", "echo pkg/nothing\n", 0o755)
	var out bytes.Buffer
	cfg := Config{Root: root, Out: &out, Remove: true, DryRun: true}
	if _, err := Refs(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "docs/a.md"))
	if !strings.Contains(string(b), "uses pkg/strategy and") || !strings.Contains(string(b), "pkg/nothing") {
		t.Fatalf("dry run changed the file: %s", b)
	}
	cfg.DryRun = false
	out.Reset()
	if _, err := Refs(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, "docs/a.md"))
	want := "uses pkg/backtest/strategy and pkg/orchestrator/risk\nkeep me\n" // the pkg/nothing line is gone
	if sh, _ := os.ReadFile(filepath.Join(root, "run.sh")); string(sh) != "echo pkg/nothing\n" {
		t.Fatalf("a script line was deleted: %q", sh)
	}
	if string(b) != want {
		t.Fatalf("got %q want %q\n%s", b, want, out.String())
	}
}
