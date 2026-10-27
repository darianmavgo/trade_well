package refdb

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/realbars"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// Every column of every strategy table in the real strategies.db must be
// described in strategy_family_param, and each gridsearchable column must carry
// a grid or be supplied on the command line.
func TestEveryFamilyColumnIsDescribed(t *testing.T) {
	real, err := sqlx.Open("sqlite", "file:"+realbars.StrategiesDB(t)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer real.Close()

	// The table comes from refdb.Open, on a fresh copy, so the test never writes the real file.
	fresh, err := Open(filepath.Join(t.TempDir(), "strategies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()

	for family, table := range map[string]string{
		"streak": "streak_strategy", "hold": "hold_strategy",
		"tree": "tree_strategy", "markov": "markov_strategy",
	} {
		var cols []string
		if err := real.Select(&cols, "SELECT name FROM pragma_table_info('"+table+"')"); err != nil || len(cols) == 0 {
			t.Fatalf("%s: columns %v err %v", table, cols, err)
		}
		params, err := FamilyParams(fresh, family)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]FamilyParam{}
		for _, p := range params {
			have[p.Param] = p
		}
		for _, c := range cols {
			p, ok := have[c]
			if !ok {
				t.Errorf("%s.%s has no strategy_family_param row", family, c)
				continue
			}
			if p.Gridsearchable && p.GridValues == nil {
				t.Errorf("%s.%s is gridsearchable with no grid", family, c)
			}
			if !p.Gridsearchable && (p.WhyNot == nil || *p.WhyNot == "") {
				t.Errorf("%s.%s is not gridsearchable and does not say why", family, c)
			}
		}
		if len(have) != len(cols) {
			t.Errorf("%s: %d described columns, table has %d", family, len(have), len(cols))
		}
	}
}
