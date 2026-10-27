package orchestratorsql

import (
	"io/fs"
	"strings"
	"testing"
)

func TestParseSplitsNamedStatements(t *testing.T) {
	got, err := parse("p", "-- a header comment before any statement\n\n-- name: first\nSELECT 1;\n\n-- name: second\n-- a comment inside the statement\nSELECT 2\nFROM t;\n")
	if err != nil {
		t.Fatal(err)
	}
	if got["first"] != "SELECT 1;" {
		t.Fatalf("first = %q", got["first"])
	}
	if got["second"] != "-- a comment inside the statement\nSELECT 2\nFROM t;" {
		t.Fatalf("second = %q", got["second"])
	}
	if len(got) != 2 {
		t.Fatalf("got %d statements: %v", len(got), got)
	}
	if _, err := parse("p", "-- name: x\nSELECT 1;\n-- name: x\nSELECT 2;\n"); err == nil || !strings.Contains(err.Error(), "two statements named x") {
		t.Fatalf("a repeated name gave %v", err)
	}
}

// Q reads a real statement by "<package>/<name>" from sql/orchestrator/<package>.sql.
func TestQReadsAStatementByName(t *testing.T) {
	text := Q("job_runs/max_id")
	if !strings.Contains(strings.ToUpper(text), "SELECT") || strings.Contains(text, "-- name:") {
		t.Fatalf("Q(job_runs/max_id) = %q", text)
	}
	if again := Q("job_runs/max_id"); again != text {
		t.Fatal("a second read gave different text")
	}
}

// A name with no statement is a deploy or programming error: it panics and says which statement in
// which file, instead of handing back an empty statement that would fail somewhere else.
func TestQPanicsNamingWhatIsMissing(t *testing.T) {
	for name, want := range map[string]string{
		"job_runs/no_such_statement": `sql/orchestrator/job_runs.sql has no statement named "no_such_statement"`,
		"no_such_package/anything":   "sql/orchestrator/no_such_package.sql",
		"missing_the_slash":          `want "<package>/<name>"`,
	} {
		func() {
			defer func() {
				msg, _ := recover().(string)
				if !strings.Contains(msg, want) {
					t.Errorf("Q(%q) recovered %q, want it to mention %q", name, msg, want)
				}
			}()
			Q(name)
			t.Errorf("Q(%q) did not panic", name)
		}()
	}
}

func TestFileAndDirReadAPackagesStageFiles(t *testing.T) {
	if text := File("trade_history", "count_all"); !strings.Contains(text, "SELECT COUNT(*) FROM trade_history") {
		t.Fatalf("File(trade_history, count_all) = %q", text)
	}
	entries, err := fs.ReadDir(Dir("live_vs_backtest_winrate"), ".")
	if err != nil || len(entries) < 20 {
		t.Fatalf("Dir(live_vs_backtest_winrate) has %d entries (%v)", len(entries), err)
	}
	if b, err := fs.ReadFile(Dir("live_vs_backtest_winrate"), "08_compare.sql"); err != nil || !strings.Contains(string(b), "winrate_compare") {
		t.Fatalf("08_compare.sql: %v", err)
	}
	names, err := Names("manage_exits")
	if err != nil || len(names) < 10 {
		t.Fatalf("Names(manage_exits) = %v, %v", names, err)
	}
}
