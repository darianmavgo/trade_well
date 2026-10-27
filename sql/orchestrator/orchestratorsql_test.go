package orchestratorsql

import (
	"io/fs"
	"strings"
	"testing"
)

func TestQReadsAFileByName(t *testing.T) {
	text := Q("trade_history/count_all")
	if !strings.Contains(text, "SELECT COUNT(*) FROM trade_history") {
		t.Fatalf("Q(trade_history/count_all) = %q", text)
	}
	if again := Q("trade_history/count_all"); again != text {
		t.Fatal("a second read gave different text")
	}
}

// A name with no file is a deploy or programming error: it panics and says which file, instead of
// handing back an empty statement that would fail somewhere else.
func TestQPanicsNamingTheMissingFile(t *testing.T) {
	defer func() {
		r := recover()
		msg, _ := r.(string)
		if !strings.Contains(msg, "no_such_package/no_such_file.sql") {
			t.Fatalf("recovered %v, want a message naming the missing file", r)
		}
	}()
	Q("no_such_package/no_such_file")
	t.Fatal("Q of a missing file did not panic")
}

func TestDirListsAPackagesFiles(t *testing.T) {
	entries, err := fs.ReadDir(Dir("live_vs_backtest_winrate"), ".")
	if err != nil || len(entries) < 20 {
		t.Fatalf("Dir(live_vs_backtest_winrate) has %d entries (%v)", len(entries), err)
	}
	if b, err := fs.ReadFile(Dir("live_vs_backtest_winrate"), "08_compare.sql"); err != nil || !strings.Contains(string(b), "winrate_compare") {
		t.Fatalf("08_compare.sql: %v", err)
	}
}
