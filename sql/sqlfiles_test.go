package sqlfiles

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// searchUp finds the sql folder from any folder below it, and finds nothing where there is none.
func TestSearchUpFindsTheSQLFolderFromBelow(t *testing.T) {
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "sql", "stages"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(tree, "bin", "x", "y")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := searchUp(deep); got != filepath.Join(tree, "sql") {
		t.Fatalf("searchUp(%s) = %q", deep, got)
	}
	// a folder named sql that is not this repository's (no stages/, no orchestrator/) does not count
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, "sql", "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := searchUp(filepath.Join(other, "sql")); got != "" {
		t.Fatalf("a foreign sql folder was taken for ours: %q", got)
	}
}

// With no sql folder to be found there is no other place to look: every read fails, and the error
// says what was searched from and what to do. Nothing is returned that looks like a statement.
func TestRootWithoutASQLFolderIsAClearError(t *testing.T) {
	empty := t.TempDir()
	t.Chdir(empty)
	mu.Lock()
	saved := root
	root = ""
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); root = saved; mu.Unlock() })

	_, err := Root()
	if err == nil {
		t.Fatal("Root found a sql folder where there is none")
	}
	// the working directory may be reported through a symlink-resolved path, so match on its base
	for _, want := range []string{"cannot find the sql folder", filepath.Base(empty), "not embedded in the binary", ".gcloudignore"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, err := fs.ReadFile(FS, "stages/check/01.sql"); err == nil || !strings.Contains(err.Error(), "cannot find the sql folder") {
		t.Fatalf("reading through FS gave %v, want the same clear error", err)
	}
	if _, err := Resolve("sql/strategies/x"); err == nil {
		t.Fatal("a relative path resolved without a sql folder")
	}
	if p, err := Resolve("/abs/dir"); err != nil || p != "/abs/dir" {
		t.Fatalf("an absolute path must pass through untouched: %q, %v", p, err)
	}
}

// From a test the working directory is below the repository, so Root finds the real sql folder and
// FS reads real files from it, with the paths the backtest layer uses.
func TestFSReadsTheRealSQLFolder(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.ToSlash(root), "/sql") {
		t.Fatalf("Root = %q", root)
	}
	for _, f := range []string{"view_symbol_stats.sql", "validation/check_overfit.sql", "stages/check"} {
		if _, err := fs.Stat(FS, f); err != nil {
			t.Errorf("FS cannot open %s: %v", f, err)
		}
	}
	if v, err := SymbolStatsView(); err != nil || !strings.Contains(strings.ToLower(v), "view") {
		t.Fatalf("SymbolStatsView = %q, %v", v, err)
	}
	if _, err := fs.ReadFile(FS, "stages/nope/missing.sql"); err == nil {
		t.Fatal("a file that does not exist was read")
	}
}
