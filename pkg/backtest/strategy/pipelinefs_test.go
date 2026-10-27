package strategy

import (
	"strings"
	"testing"
)

// Library callers (trade_orchestrator on App Engine) run with a working
// directory that has no sql/strategies. Every SQL pipeline must still resolve,
// from the copy embedded in the binary.
func TestPipelineSQLResolvesWithoutRepoCheckout(t *testing.T) {
	t.Chdir(t.TempDir()) // no sql/ here

	for _, id := range []string{"tree_strategy", "streak_strategy", "voo_up3"} {
		dir := "sql/strategies/" + id
		if pipelineOnDisk(dir) {
			t.Fatalf("%s unexpectedly on disk in the temp cwd", dir)
		}
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
			t.Fatalf("%s: no .sql files found in embedded pipeline", id)
		}
		body, err := readPipelineFile(dir, first)
		if err != nil || len(body) == 0 {
			t.Fatalf("%s/%s: read failed or empty: %v", id, first, err)
		}
	}
}

func TestPipelineDirMissingEverywhereIsAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := readPipelineDir("sql/strategies/no_such_pipeline"); err == nil {
		t.Fatal("expected an error for a pipeline that is neither on disk nor embedded")
	}
}
