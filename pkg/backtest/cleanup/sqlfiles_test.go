package cleanup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSQLFindsUnreferenced(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	write(t, filepath.Join(root, "sql", "stages", "bar_sma", "01.sql"), "select 1;")
	write(t, filepath.Join(root, "sql", "stages", "old_stage", "01.sql"), "select 1;")
	write(t, filepath.Join(root, "sql", "validation", "used.sql"), "select 1;")
	write(t, filepath.Join(root, "sql", "view_old.sql"), "select 1;")
	write(t, filepath.Join(root, "sql", "embed.go"), "package sqlfiles")
	write(t, filepath.Join(root, "pkg", "a.go"), `package a; var _ = storage.RunStage(db, "bar_sma", nil); var f = "validation/used.sql"`)

	var out bytes.Buffer
	cfg := Config{Root: root, Out: &out}
	stale, err := ScanSQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range stale {
		got[s.Path] = true
	}
	if len(got) != 2 || !got[filepath.Join("stages", "old_stage")] || !got["view_old.sql"] {
		t.Fatalf("stale = %v", got)
	}

	cfg.RemoveSQL, cfg.DryRun = true, true
	if _, err := SQL(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sql", "view_old.sql")); err != nil {
		t.Fatal("dry run removed a file")
	}
	cfg.DryRun = false
	if _, err := SQL(cfg); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"view_old.sql", filepath.Join("stages", "old_stage")} {
		if _, err := os.Stat(filepath.Join(root, "sql", p)); err == nil {
			t.Fatalf("%s still exists", p)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "sql", "stages", "bar_sma")); err != nil {
		t.Fatal("referenced stage was removed")
	}
}

// sql/orchestrator belongs to the orchestrator: its files are read by name through a loader no
// scan can see, so the cleanup must never list them as unreferenced, let alone delete them.
func TestSQLNeverJudgesTheOrchestratorsFolder(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	write(t, filepath.Join(root, "sql", "orchestrator", "manage_exits", "close_active_trade.sql"), "select 1;")
	write(t, filepath.Join(root, "sql", "view_old.sql"), "select 1;")
	var out bytes.Buffer
	cfg := Config{Root: root, Out: &out, RemoveSQL: true}
	stale, err := ScanSQL(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].Path != "view_old.sql" {
		t.Fatalf("stale = %+v, want only view_old.sql", stale)
	}
	if _, err := SQL(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sql", "orchestrator", "manage_exits", "close_active_trade.sql")); err != nil {
		t.Fatal("the cleanup removed an orchestrator SQL file")
	}
}
