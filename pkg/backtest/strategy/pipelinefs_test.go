package strategy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// A pipeline named "sql/strategies/<id>" is read from disk, relative to the folder that holds the
// sql folder, whatever the working directory is below or beside it.
func TestPipelineSQLIsReadFromDiskRelativeToTheRepository(t *testing.T) {
	if _, err := sqlfiles.Root(); err != nil { // found from where the test runs, as a command would
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	for _, id := range []string{"tree_strategy", "streak_strategy", "hold_strategy"} {
		dir := "sql/strategies/" + id
		entries, err := readPipelineDir(dir)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		var first string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".sql") {
				first = e.Name()
				break
			}
		}
		if first == "" {
			t.Fatalf("%s: no .sql files found", id)
		}
		body, err := readPipelineFile(dir, first)
		if err != nil || len(body) == 0 {
			t.Fatalf("%s/%s: read failed or empty: %v", id, first, err)
		}
	}
}

// An absolute directory (a test fixture, a path a caller built) is read as it is.
func TestPipelineSQLAbsoluteDirectoryIsReadAsGiven(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "01_x.sql"), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, err := readPipelineFile(dir, "01_x.sql")
	if err != nil || string(body) != "SELECT 1;" {
		t.Fatalf("read %q, %v", body, err)
	}
}

// A pipeline that is not there is an error that names the directory and the path looked at. There is
// no other copy to fall back to.
func TestPipelineDirThatIsMissingIsAClearError(t *testing.T) {
	_, err := readPipelineDir("sql/strategies/no_such_pipeline")
	if err == nil {
		t.Fatal("expected an error for a pipeline directory that does not exist")
	}
	for _, want := range []string{"sql/strategies/no_such_pipeline", filepath.Join("sql", "strategies", "no_such_pipeline")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if _, err := readPipelineFile("sql/strategies/tree_strategy", "no_such_file.sql"); err == nil || !strings.Contains(err.Error(), "no_such_file.sql") {
		t.Fatalf("a missing file gave %v, want an error naming it", err)
	}
}
